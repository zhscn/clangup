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

type Client struct{ HTTP *http.Client }

func NewClient() *Client {
	return &Client{HTTP: &http.Client{Timeout: 30 * time.Minute, CheckRedirect: func(request *http.Request, _ []*http.Request) error {
		if request.URL.Scheme != "https" {
			return fmt.Errorf("redirect is not HTTPS")
		}
		return nil
	}}}
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
		return fmt.Errorf("JSON exceeds %d bytes", maxJSONSize)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("JSON has trailing data")
	}
	return nil
}

func BaseURL(indexURL string) (string, error) {
	parsed, err := url.Parse(indexURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return "", fmt.Errorf("invalid HTTPS index URL %q", indexURL)
	}
	parsed.RawQuery, parsed.Fragment = "", ""
	parsed.Path = strings.TrimSuffix(parsed.Path, pathpkg.Base(parsed.Path))
	return parsed.String(), nil
}

func Resolve(base, relative string) (string, error) {
	root, err := url.Parse(base)
	if err != nil || root.Scheme != "https" || root.Host == "" {
		return "", fmt.Errorf("invalid HTTPS base URL %q", base)
	}
	reference, err := url.Parse(relative)
	if err != nil || reference.IsAbs() || reference.RawQuery != "" || reference.Fragment != "" || pathpkg.Clean(relative) != relative || !objectKeyPattern.MatchString(relative) {
		return "", fmt.Errorf("invalid object path %q", relative)
	}
	resolved := root.ResolveReference(reference)
	if resolved.Scheme != root.Scheme || resolved.Host != root.Host {
		return "", fmt.Errorf("object escapes base URL")
	}
	return resolved.String(), nil
}

func (client *Client) SyncIndex() (*Index, error) {
	var index Index
	if err := client.JSON(IndexURL(), &index); err != nil {
		return nil, err
	}
	if err := ValidateIndex(&index); err != nil {
		return nil, err
	}
	contents, err := json.Marshal(index)
	if err != nil {
		return nil, err
	}
	path, err := IndexPath()
	if err != nil {
		return nil, err
	}
	if err := writeFileAtomic(path, append(contents, '\n')); err != nil {
		return nil, err
	}
	return &index, nil
}

func LoadIndex() (*Index, error) {
	path, err := IndexPath()
	if err != nil {
		return nil, err
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("channel index is not cached; run clangup update: %w", err)
	}
	var index Index
	if err := json.Unmarshal(contents, &index); err != nil {
		return nil, err
	}
	if err := ValidateIndex(&index); err != nil {
		return nil, err
	}
	return &index, nil
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
	size, actual := offset+written, hex.EncodeToString(digest.Sum(nil))
	if size != object.Size || actual != object.SHA256 {
		return fmt.Errorf("download identity mismatch: expected %d bytes sha256:%s, got %d bytes sha256:%s", object.Size, object.SHA256, size, actual)
	}
	return nil
}

func (client *Client) Object(base string, object Object) (string, error) {
	digestBytes, digestErr := hex.DecodeString(object.SHA256)
	if digestErr != nil || len(digestBytes) != sha256.Size || object.Size < 0 || !objectKeyPattern.MatchString(object.Key) {
		return "", fmt.Errorf("invalid object identity")
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
	temporary := path + ".partial"
	location, err := Resolve(base, object.Key)
	if err != nil {
		return "", err
	}
	if err := client.Download(location, object, temporary); err != nil {
		return "", err
	}
	if err := os.Rename(temporary, path); err != nil {
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
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(contents); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
