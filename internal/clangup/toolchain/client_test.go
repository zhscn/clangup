package toolchain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestSyncCatalogCachesReleaseMetadata(t *testing.T) {
	t.Setenv("CLANGUP_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	t.Setenv("CLANGUP_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))
	identity := ReleaseIdentity{Channel: "default", Version: "1", Release: 1}
	payload := testObject("objects/artifact.tar.zst", []byte("artifact"))
	manifest := Manifest{Schema: "clangup.artifact/v1", Release: identity}
	manifest.RuntimeRequirements.Triple = "x86_64-unknown-linux-gnu"
	manifest.Artifact.Name = "artifact.tar.zst"
	manifest.Artifact.Size = payload.Size
	manifest.Artifact.SHA256 = payload.SHA256
	manifest.Artifact.Compression = "tar.zst"
	manifest.Artifact.PayloadRoot = "prefix"
	manifest.Artifact.Relocatable = true
	source := testObject("objects/source.tar.xz", []byte("source"))
	manifest.Source.Archive.SHA256 = source.SHA256
	manifestContents, _ := json.Marshal(manifest)
	manifestObject := testObject("objects/manifest.json", manifestContents)
	releaseValue := Release{Schema: "clangup.release/v1", Release: identity, Inputs: ReleaseInputs{Source: source}}
	releaseValue.Artifacts = []Artifact{{Target: "x86_64-unknown-linux-gnu", Manifest: manifestObject, Artifact: payload}}
	releaseContents, _ := json.Marshal(releaseValue)
	releaseObject := testObject("releases/default/1-1/release.json", releaseContents)
	catalog := Catalog{Schema: "clangup.catalog/v1", Channels: map[string]CatalogChannel{
		"default": {Current: "1-1", Releases: []CatalogRelease{{Version: "1", Release: 1, Descriptor: releaseObject}}},
	}}
	catalog.Repository.Namespace = "example.com/llvm"
	catalogContents, _ := json.Marshal(catalog)
	objects := map[string][]byte{
		"/catalog-v1.json":                   catalogContents,
		"/releases/default/1-1/release.json": releaseContents,
		"/objects/manifest.json":             manifestContents,
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		contents, found := objects[request.URL.Path]
		if !found {
			http.NotFound(response, request)
			return
		}
		_, _ = response.Write(contents)
	}))
	defer server.Close()
	client := &Client{HTTP: server.Client()}
	repository := Repository{Namespace: "example.com/llvm", URL: server.URL + "/catalog-v1.json"}
	if _, err := client.SyncCatalog(repository); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCatalog(repository); err != nil {
		t.Fatal(err)
	}
	cache, err := CacheRoot()
	if err != nil {
		t.Fatal(err)
	}
	for _, object := range []Object{releaseObject, manifestObject} {
		if _, err := os.Stat(filepath.Join(cache, "objects", "sha256", object.SHA256)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDownloadResumesPartialObject(t *testing.T) {
	contents := []byte("complete artifact contents")
	object := testObject("objects/artifact", contents)
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Range") != "bytes=8-" {
			t.Errorf("range = %q", request.Header.Get("Range"))
		}
		response.WriteHeader(http.StatusPartialContent)
		_, _ = response.Write(contents[8:])
	}))
	defer server.Close()
	destination := filepath.Join(t.TempDir(), "partial")
	if err := os.WriteFile(destination, contents[:8], 0o600); err != nil {
		t.Fatal(err)
	}
	client := &Client{HTTP: server.Client()}
	if err := client.Download(server.URL, object, destination); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(actual) != string(contents) {
		t.Fatalf("downloaded %q", actual)
	}
}

func testObject(key string, contents []byte) Object {
	digest := sha256.Sum256(contents)
	return Object{Key: key, Size: int64(len(contents)), SHA256: hex.EncodeToString(digest[:])}
}
