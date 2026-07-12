package authoring

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type memoryObject struct {
	contents []byte
	info     ObjectInfo
}

type memoryStore struct {
	objects map[string]memoryObject
}

func (store *memoryStore) Head(key string) (*ObjectInfo, error) {
	object, ok := store.objects[key]
	if !ok {
		return nil, nil
	}
	info := object.info
	return &info, nil
}

func (store *memoryStore) Get(key string) ([]byte, error) {
	return append([]byte(nil), store.objects[key].contents...), nil
}

func (store *memoryStore) Put(key string, contents []byte, options PutOptions) error {
	store.objects[key] = memoryObject{contents: append([]byte(nil), contents...), info: ObjectInfo{
		Size: int64(len(contents)), SHA256: options.SHA256, ETag: "next",
	}}
	return nil
}

func TestPublishRelease(t *testing.T) {
	lock := strings.Repeat("a", 64)
	source := strings.Repeat("b", 64)
	release := []byte(fmt.Sprintf(`{"schema":"clangup.release/v1","release":{"channel":"default","version":"22.1.8","release":1},"locked_spec":{"key":"objects/sha256/%s/spec.lock.json","size":1,"sha256":"%s"},"source":{"key":"objects/sha256/%s/source.tar.xz","size":1,"sha256":"%s"},"patches":[],"artifacts":[]}`+"\n", lock, lock, source, source))
	path := filepath.Join(t.TempDir(), "release.json")
	if err := os.WriteFile(path, release, 0o644); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(release)
	descriptorKey := "releases/default/22.1.8-1/release.json"
	store := &memoryStore{objects: map[string]memoryObject{descriptorKey: {
		contents: release,
		info:     ObjectInfo{Size: int64(len(release)), SHA256: hex.EncodeToString(digest[:]), ETag: "release"},
	}}}
	result, err := Publish(store, path, PublishOptions{
		Namespace: "example.com/llvm", DisplayName: "Example LLVM",
		DefaultChannel: "default", CatalogKey: "catalog-v1.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Written || result.Exact != "22.1.8-1" {
		t.Fatalf("unexpected result: %#v", result)
	}
	var catalog struct {
		Channels map[string]struct {
			Current  string `json:"current"`
			Releases []struct {
				Descriptor struct {
					Key string `json:"key"`
				} `json:"descriptor"`
			} `json:"releases"`
		} `json:"channels"`
	}
	if err := json.Unmarshal(store.objects["catalog-v1.json"].contents, &catalog); err != nil {
		t.Fatal(err)
	}
	channel := catalog.Channels["default"]
	if channel.Current != "22.1.8-1" || channel.Releases[0].Descriptor.Key != descriptorKey {
		t.Fatalf("unexpected catalog: %#v", channel)
	}
	second, err := Publish(store, path, PublishOptions{
		Namespace: "example.com/llvm", DisplayName: "Example LLVM",
		DefaultChannel: "default", CatalogKey: "catalog-v1.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.Written {
		t.Fatal("unchanged catalog was rewritten")
	}
}

func TestPublishRejectsRemoteDescriptorMismatch(t *testing.T) {
	lock := strings.Repeat("a", 64)
	source := strings.Repeat("b", 64)
	release := []byte(fmt.Sprintf(`{"schema":"clangup.release/v1","release":{"channel":"default","version":"1.0","release":1},"locked_spec":{"key":"objects/sha256/%s/lock","size":1,"sha256":"%s"},"source":{"key":"objects/sha256/%s/source","size":1,"sha256":"%s"},"patches":[],"artifacts":[]}`, lock, lock, source, source))
	path := filepath.Join(t.TempDir(), "release.json")
	if err := os.WriteFile(path, release, 0o644); err != nil {
		t.Fatal(err)
	}
	store := &memoryStore{objects: map[string]memoryObject{
		"releases/default/1.0-1/release.json": {info: ObjectInfo{Size: int64(len(release)), SHA256: "wrong"}},
	}}
	if _, err := Publish(store, path, PublishOptions{Namespace: "example.com/llvm", DisplayName: "Example", CatalogKey: "catalog-v1.json"}); err == nil {
		t.Fatal("mismatched descriptor was accepted")
	}
}
