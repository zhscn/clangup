package toolchain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const maxJSONSize = 8 << 20

var objectKeyPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]+(?:/[A-Za-z0-9._~-]+)*$`)

type Client struct {
	HTTP *http.Client
}

func NewClient() *Client {
	return &Client{HTTP: &http.Client{
		Timeout: 30 * time.Minute,
		CheckRedirect: func(request *http.Request, _ []*http.Request) error {
			if request.URL.Scheme != "https" {
				return fmt.Errorf("repository redirect is not HTTPS")
			}
			return nil
		},
	}}
}

func (client *Client) JSON(location string, destination any) error {
	response, err := client.HTTP.Get(location)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", location, response.Status)
	}
	limited := &io.LimitedReader{R: response.Body, N: maxJSONSize + 1}
	decoder := json.NewDecoder(limited)
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if limited.N <= 0 {
		return fmt.Errorf("repository JSON exceeds %d bytes", maxJSONSize)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("repository JSON has trailing data")
	}
	return nil
}

func Resolve(base, relative string) (string, error) {
	root, err := url.Parse(base)
	if err != nil || root.Scheme != "https" || root.Host == "" {
		return "", fmt.Errorf("invalid HTTPS repository URL %q", base)
	}
	reference, err := url.Parse(relative)
	if err != nil || reference.IsAbs() || reference.RawQuery != "" || reference.Fragment != "" || pathpkg.Clean(relative) != relative || !objectKeyPattern.MatchString(relative) {
		return "", fmt.Errorf("invalid repository object key %q", relative)
	}
	resolved := root.ResolveReference(reference)
	if resolved.Scheme != root.Scheme || resolved.Host != root.Host {
		return "", fmt.Errorf("repository object escapes base URL")
	}
	return resolved.String(), nil
}

func CatalogBase(catalogURL string) (string, error) {
	parsed, err := url.Parse(catalogURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return "", fmt.Errorf("invalid HTTPS catalog URL %q", catalogURL)
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.Path = strings.TrimSuffix(parsed.Path, filepath.Base(parsed.Path))
	return parsed.String(), nil
}

func (client *Client) Download(location string, object Object, destination string) error {
	offset := int64(0)
	if info, err := os.Stat(destination); err == nil {
		offset = info.Size()
		if offset == object.Size {
			digest, size, identityErr := fileIdentity(destination)
			if identityErr == nil && size == object.Size && digest == object.SHA256 {
				return nil
			}
			if err := os.Remove(destination); err != nil {
				return err
			}
			offset = 0
		}
		if offset > object.Size {
			if err := os.Remove(destination); err != nil {
				return err
			}
			offset = 0
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	request, err := http.NewRequest(http.MethodGet, location, nil)
	if err != nil {
		return err
	}
	if offset > 0 {
		request.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	response, err := client.HTTP.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("GET %s: %s", location, response.Status)
	}
	flags := os.O_CREATE | os.O_WRONLY
	if response.StatusCode == http.StatusPartialContent && offset > 0 {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
		offset = 0
	}
	file, err := os.OpenFile(destination, flags, 0o600)
	if err != nil {
		return err
	}
	digest := sha256.New()
	if offset > 0 {
		existing, err := os.Open(destination)
		if err != nil {
			file.Close()
			return err
		}
		if _, err := io.Copy(digest, existing); err != nil {
			existing.Close()
			file.Close()
			return err
		}
		existing.Close()
	}
	written, copyErr := io.Copy(io.MultiWriter(file, digest), response.Body)
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	size := offset + written
	actual := hex.EncodeToString(digest.Sum(nil))
	if size != object.Size || actual != object.SHA256 {
		return fmt.Errorf("download identity mismatch: expected %d bytes sha256:%s, got %d bytes sha256:%s", object.Size, object.SHA256, size, actual)
	}
	return nil
}

func (client *Client) SyncCatalog(repository Repository) (*Catalog, error) {
	var catalog Catalog
	if err := client.JSON(repository.URL, &catalog); err != nil {
		return nil, err
	}
	if catalog.Schema != "clangup.catalog/v1" || catalog.Repository.Namespace != repository.Namespace {
		return nil, fmt.Errorf("catalog identity mismatch for %s", repository.Namespace)
	}
	if err := client.CacheCatalogObjects(repository, &catalog); err != nil {
		return nil, err
	}
	if err := StoreCatalog(repository, &catalog); err != nil {
		return nil, err
	}
	return &catalog, nil
}

func (client *Client) CacheCatalogObjects(repository Repository, catalog *Catalog) error {
	base, err := CatalogBase(repository.URL)
	if err != nil {
		return err
	}
	for channelName, channel := range catalog.Channels {
		for _, catalogRelease := range channel.Releases {
			path, err := client.Object(base, catalogRelease.Descriptor)
			if err != nil {
				return err
			}
			contents, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			var release Release
			if err := json.Unmarshal(contents, &release); err != nil {
				return err
			}
			if release.Schema != "clangup.release/v1" || release.Release.Channel != channelName || release.Release.Version != catalogRelease.Version || release.Release.Release != catalogRelease.Release {
				return fmt.Errorf("release descriptor identity mismatch for %s@%s-%d", channelName, catalogRelease.Version, catalogRelease.Release)
			}
			for _, artifact := range release.Artifacts {
				manifestPath, err := client.Object(base, artifact.Manifest)
				if err != nil {
					return err
				}
				contents, err := os.ReadFile(manifestPath)
				if err != nil {
					return err
				}
				var manifest Manifest
				if err := json.Unmarshal(contents, &manifest); err != nil {
					return err
				}
				if err := ValidateManifest(&release, &artifact, &manifest); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func StoreCatalog(repository Repository, catalog *Catalog) error {
	if catalog.Schema != "clangup.catalog/v1" || catalog.Repository.Namespace != repository.Namespace {
		return fmt.Errorf("catalog identity mismatch for %s", repository.Namespace)
	}
	path, err := CatalogPath(repository)
	if err != nil {
		return err
	}
	contents, err := json.Marshal(catalog)
	if err != nil {
		return err
	}
	contents = append(contents, '\n')
	if err := writeFileAtomic(path, contents); err != nil {
		return err
	}
	return nil
}

func LoadCatalog(repository Repository) (*Catalog, error) {
	path, err := CatalogPath(repository)
	if err != nil {
		return nil, err
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("repository metadata is not cached; run clangup repo update %s: %w", repository.Namespace, err)
	}
	var catalog Catalog
	if err := json.Unmarshal(contents, &catalog); err != nil {
		return nil, err
	}
	if catalog.Schema != "clangup.catalog/v1" || catalog.Repository.Namespace != repository.Namespace {
		return nil, fmt.Errorf("cached catalog identity mismatch for %s", repository.Namespace)
	}
	return &catalog, nil
}

func (client *Client) Object(base string, object Object) (string, error) {
	digestBytes, digestErr := hex.DecodeString(object.SHA256)
	if digestErr != nil || len(digestBytes) != sha256.Size || object.Size < 0 || !objectKeyPattern.MatchString(object.Key) {
		return "", fmt.Errorf("invalid repository object identity")
	}
	root, err := CacheRoot()
	if err != nil {
		return "", err
	}
	path := filepath.Join(root, "objects", "sha256", object.SHA256)
	if digest, size, err := fileIdentity(path); err == nil && digest == object.SHA256 && size == object.Size {
		return path, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	temporaryPath := path + ".partial"
	location, err := Resolve(base, object.Key)
	if err != nil {
		return "", err
	}
	if err := client.Download(location, object, temporaryPath); err != nil {
		return "", err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		if digest, size, identityErr := fileIdentity(path); identityErr != nil || digest != object.SHA256 || size != object.Size {
			return "", err
		}
	}
	return path, nil
}

func fileIdentity(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	digest := sha256.New()
	size, err := io.Copy(digest, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(digest.Sum(nil)), size, nil
}

func FileIdentity(path string) (string, int64, error) { return fileIdentity(path) }

func writeFileAtomic(path string, contents []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".metadata-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if err := temporary.Chmod(0o644); err != nil {
		return err
	}
	if _, err := temporary.Write(contents); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
