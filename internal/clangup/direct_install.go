package clangup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/zhscn/clangup/internal/clangup/toolchain"
)

func installDirect(file, location, prefix, explicitTarget string, force bool) (*installResult, error) {
	var archive string
	var manifestContents []byte
	var err error
	if file != "" {
		archive, err = filepath.Abs(file)
		if err != nil {
			return nil, err
		}
		manifestContents, err = readSiblingManifest(archive)
	} else {
		parsed, parseErr := url.Parse(location)
		if parseErr != nil || parsed.Scheme != "https" || parsed.Host == "" {
			return nil, fmt.Errorf("artifact URL must be HTTPS")
		}
		manifestContents, err = fetchSiblingManifest(location)
	}
	if err != nil {
		return nil, err
	}
	var manifest toolchain.Manifest
	decoder := json.NewDecoder(strings.NewReader(string(manifestContents)))
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("decode artifact manifest: %w", err)
	}
	if manifest.Schema != "clangup.artifact/v1" || manifest.Release.Channel == "" || manifest.Release.Version == "" || manifest.Release.Release < 1 || manifest.Artifact.Size < 0 || len(manifest.Artifact.SHA256) != 64 {
		return nil, fmt.Errorf("invalid artifact manifest")
	}
	if explicitTarget != "" && explicitTarget != manifest.RuntimeRequirements.Triple {
		return nil, fmt.Errorf("manifest target is %s, not %s", manifest.RuntimeRequirements.Triple, explicitTarget)
	}
	object := toolchain.Object{Size: manifest.Artifact.Size, SHA256: manifest.Artifact.SHA256}
	if location != "" {
		cache, err := toolchain.CacheRoot()
		if err != nil {
			return nil, err
		}
		archive = filepath.Join(cache, "objects", "sha256", object.SHA256+".partial")
		if err := os.MkdirAll(filepath.Dir(archive), 0o755); err != nil {
			return nil, err
		}
		client := toolchain.NewClient()
		if err := client.Download(location, object, archive); err != nil {
			return nil, err
		}
	}
	digest, size, err := toolchain.FileIdentity(archive)
	if err != nil {
		return nil, err
	}
	if digest != object.SHA256 || size != object.Size {
		return nil, fmt.Errorf("artifact identity differs from manifest")
	}
	manifestDigest := sha256.Sum256(manifestContents)
	channel := "local/" + manifest.Release.Channel
	exact := fmt.Sprintf("%s-%d", manifest.Release.Version, manifest.Release.Release)
	if prefix == "" {
		root, err := toolchain.DataRoot()
		if err != nil {
			return nil, err
		}
		prefix = filepath.Join(root, "toolchains", "local", manifest.Release.Channel, exact, manifest.RuntimeRequirements.Triple)
	}
	prefix, err = filepath.Abs(prefix)
	if err != nil {
		return nil, err
	}
	if err := toolchain.InstallArchive(archive, prefix, force); err != nil {
		return nil, err
	}
	record := toolchain.InstallRecord{
		Channel: channel, Version: manifest.Release.Version, Release: manifest.Release.Release,
		Target: manifest.RuntimeRequirements.Triple, Prefix: prefix,
		ManifestSHA256: hex.EncodeToString(manifestDigest[:]), ArtifactSHA256: object.SHA256,
		DriverRequirements: manifest.DriverRequirements.ExternalComponents,
	}
	if err := toolchain.RecordInstall(record); err != nil {
		_ = os.RemoveAll(prefix)
		return nil, err
	}
	if err := ensureFirstDefault(prefix); err != nil {
		return nil, err
	}
	release := toolchain.CatalogRelease{Version: record.Version, Release: record.Release}
	artifact := &toolchain.Artifact{Target: record.Target, Artifact: object, Manifest: toolchain.Object{SHA256: record.ManifestSHA256}}
	return installationResult("local", manifest.Release.Channel, release, artifact, &manifest, prefix), nil
}

func siblingManifestNames(value string) []string {
	return []string{value + ".manifest.json", strings.TrimSuffix(value, ".tar.zst") + ".manifest.json"}
}

func readSiblingManifest(archive string) ([]byte, error) {
	if !strings.HasSuffix(archive, ".tar.zst") {
		return nil, fmt.Errorf("artifact must have a .tar.zst suffix")
	}
	for _, path := range siblingManifestNames(archive) {
		contents, err := os.ReadFile(path)
		if err == nil {
			return contents, nil
		}
	}
	return nil, fmt.Errorf("artifact manifest not found beside %s", archive)
}

func fetchSiblingManifest(location string) ([]byte, error) {
	if !strings.HasSuffix(location, ".tar.zst") {
		return nil, fmt.Errorf("artifact URL must have a .tar.zst suffix")
	}
	client := toolchain.NewClient().HTTP
	for _, candidate := range siblingManifestNames(location) {
		response, err := client.Get(candidate)
		if err != nil {
			continue
		}
		if response.StatusCode == http.StatusOK {
			contents, readErr := io.ReadAll(io.LimitReader(response.Body, 8<<20))
			response.Body.Close()
			if readErr != nil {
				return nil, readErr
			}
			return contents, nil
		}
		response.Body.Close()
	}
	return nil, fmt.Errorf("artifact manifest not found beside %s", location)
}
