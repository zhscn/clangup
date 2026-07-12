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

func TestSyncIndexCachesMetadata(t *testing.T) {
	t.Setenv("CLANGUP_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	index := Index{Schema: "clangup.index/v1", DefaultChannel: "default", Channels: map[string]IndexChannel{"default": {Current: "1-1", Releases: []IndexRelease{{Version: "1", Release: 1, Path: "releases/default/1-1/release.json"}}}}}
	indexContents, _ := json.Marshal(index)
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_, _ = response.Write(indexContents)
	}))
	defer server.Close()
	t.Setenv("CLANGUP_INDEX_URL", server.URL+"/index.json")
	client := &Client{HTTP: server.Client()}
	if _, err := client.SyncIndex(); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadIndex()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Channels["default"].Current != "1-1" {
		t.Fatal("cached index differs")
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
