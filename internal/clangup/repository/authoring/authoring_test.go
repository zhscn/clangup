package authoring

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/theupdateframework/go-tuf/v2/metadata/config"
	"github.com/theupdateframework/go-tuf/v2/metadata/updater"
)

func TestFilesystemPublishEndToEnd(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := Init(workspace, "example.com/llvm", "Example LLVM", true); err != nil {
		t.Fatal(err)
	}
	makeTestRelease(t, workspace)
	if err := SetCurrent(workspace, "default", "22.1.8-1"); err != nil {
		t.Fatal(err)
	}
	published := filepath.Join(root, "published")
	base := time.Now().UTC().Truncate(time.Second)
	if err := PublishFilesystem(workspace, published, base); err != nil {
		t.Fatal(err)
	}
	if err := VerifyFilesystem(published, base.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	paths, err := RepositoryTargetPaths(published)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 6 {
		t.Fatalf("published target count = %d, want 6: %v", len(paths), paths)
	}
	if _, err := os.Stat(filepath.Join(published, "metadata", "root.json")); err != nil {
		t.Fatal(err)
	}

	contents, err := os.ReadFile(filepath.Join(published, "metadata", "1.targets.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(contents) {
		t.Fatal("targets metadata is not JSON")
	}

	server := httptest.NewServer(http.FileServer(http.Dir(published)))
	defer server.Close()
	trustedRoot, err := os.ReadFile(filepath.Join(published, "metadata", "root.json"))
	if err != nil {
		t.Fatal(err)
	}
	clientConfig, err := config.New(server.URL+"/metadata/", trustedRoot)
	if err != nil {
		t.Fatal(err)
	}
	clientConfig.LocalMetadataDir = filepath.Join(root, "client-metadata")
	clientConfig.LocalTargetsDir = filepath.Join(root, "client-targets")
	clientConfig.RemoteTargetsURL = server.URL + "/targets/"
	clientConfig.PrefixTargetsWithHash = true
	client, err := updater.New(clientConfig)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Refresh(); err != nil {
		t.Fatal(err)
	}
	target, err := client.GetTargetInfo("catalog-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	downloaded, _, err := client.DownloadTarget(target, "", "")
	if err != nil {
		t.Fatal(err)
	}
	downloadedCatalog, err := os.ReadFile(downloaded)
	if err != nil || !json.Valid(downloadedCatalog) {
		t.Fatalf("downloaded catalog is invalid: %v", err)
	}
	var catalogValue catalog
	if err := json.Unmarshal(downloadedCatalog, &catalogValue); err != nil {
		t.Fatal(err)
	}
	if got := catalogValue.Channels["default"].Releases[0].ReleasedAt; got != "2026-07-11T00:00:00Z" {
		t.Fatalf("released_at = %q", got)
	}
	firstTarget := filepath.Join(published, filepath.FromSlash(paths[0]))
	if err := os.WriteFile(firstTarget, []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := VerifyFilesystem(published, base.Add(time.Hour)); err == nil {
		t.Fatal("tampered target was accepted")
	}
}

func makeTestRelease(t *testing.T, workspace string) {
	t.Helper()
	writeObject := func(contents []byte) string {
		digest := digest(contents)
		path := filepath.Join(workspace, "objects", "sha256", digest)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, contents, 0o644); err != nil {
			t.Fatal(err)
		}
		return digest
	}
	source := []byte("llvm source")
	sourceDigest := writeObject(source)
	payload := []byte("toolchain payload")
	payloadDigest := writeObject(payload)
	manifestValue := map[string]any{
		"schema":               "clangup.artifact/v1",
		"release":              map[string]any{"channel": "default", "version": "22.1.8", "release": 1},
		"artifact":             map[string]any{"name": "clang.tar.zst", "size": len(payload), "sha256": payloadDigest},
		"runtime_requirements": map[string]any{"triple": "x86_64-unknown-linux-gnu"},
		"source": map[string]any{
			"archive": map[string]any{"sha256": sourceDigest, "target": "sources/sha256/" + sourceDigest + ".tar.xz"},
			"patches": []any{}, "patchset_sha256": "empty",
		},
	}
	manifest, _ := json.Marshal(manifestValue)
	manifest = append(manifest, '\n')
	manifestDigest := writeObject(manifest)
	lockValue := map[string]any{
		"schema":    "clangup.build-lock/v1",
		"release":   map[string]any{"channel": "default", "version": "22.1.8", "release": 1},
		"source":    map[string]any{"sha256": sourceDigest, "patches": []any{}, "patchset_sha256": "empty"},
		"targets":   []any{map[string]any{"triple": "x86_64-unknown-linux-gnu", "required": true}},
		"changelog": []any{map[string]any{"release": 1, "date": "2026-07-11", "summary": "initial"}},
	}
	lock, _ := json.Marshal(lockValue)
	lock = append(lock, '\n')
	lockDigest := writeObject(lock)
	buildRecordValue := map[string]any{
		"schema": "clangup.build-record/v1", "release": map[string]any{"channel": "default", "version": "22.1.8", "release": 1},
		"target": "x86_64-unknown-linux-gnu", "locked_spec_sha256": lockDigest, "artifact_sha256": payloadDigest,
	}
	buildRecord, _ := json.Marshal(buildRecordValue)
	buildRecord = append(buildRecord, '\n')
	buildRecordDigest := writeObject(buildRecord)
	release := ImportedRelease{
		Schema: ReleaseSchema,
		Release: ReleaseIdentity{
			Channel: "default", Version: "22.1.8", Release: 1,
		},
		LockedSpecSHA256: lockDigest,
		Artifacts: []ImportedArtifact{{
			Target: "x86_64-unknown-linux-gnu", Name: "clang.tar.zst",
			PayloadSHA256: payloadDigest, ManifestSHA256: manifestDigest,
			BuildRecordSHA256: buildRecordDigest,
		}},
		Objects:    []ImportedObject{{Kind: "source", SHA256: sourceDigest, Name: "source.tar.xz"}},
		Changelog:  "initial",
		ReleasedAt: "2026-07-11T00:00:00Z",
	}
	releaseDirectory := filepath.Join(workspace, "releases", "default", "22.1.8-1")
	if err := writeTOML(filepath.Join(releaseDirectory, "release.toml"), release); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(releaseDirectory, "manifests"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(releaseDirectory, "manifests", "x86_64-unknown-linux-gnu.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
}

func digest(contents []byte) string {
	value := sha256.Sum256(contents)
	return hex.EncodeToString(value[:])
}
