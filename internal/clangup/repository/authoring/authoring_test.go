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
	bundle := makeTestBundle(t, filepath.Join(root, "bundle"))
	release, err := ImportBundle(workspace, bundle)
	if err != nil {
		t.Fatal(err)
	}
	if len(release.Artifacts) != 1 || len(release.Objects) != 1 {
		t.Fatalf("unexpected imported release: %#v", release)
	}
	if _, err := ImportBundle(workspace, bundle); err != nil {
		t.Fatalf("idempotent import failed: %v", err)
	}
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

func TestImportRejectsSymlinkedBundleInput(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := Init(workspace, "example.com/llvm", "", false); err != nil {
		t.Fatal(err)
	}
	descriptor := makeTestBundle(t, filepath.Join(root, "bundle"))
	payload := filepath.Join(root, "bundle", "artifacts", "clang.tar.zst")
	real := filepath.Join(root, "real-artifact")
	if err := os.Rename(payload, real); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, payload); err != nil {
		t.Skip(err)
	}
	if _, err := ImportBundle(workspace, descriptor); err == nil {
		t.Fatal("symlinked artifact was accepted")
	}
}

func makeTestBundle(t *testing.T, root string) string {
	t.Helper()
	write := func(relative string, contents []byte) string {
		path := filepath.Join(root, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, contents, 0o644); err != nil {
			t.Fatal(err)
		}
		return digest(contents)
	}
	source := []byte("llvm source")
	sourceDigest := write("objects/sources/source.tar.xz", source)
	payload := []byte("toolchain payload")
	payloadDigest := write("artifacts/clang.tar.zst", payload)
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
	manifestDigest := write("manifests/x86_64-unknown-linux-gnu/manifest.json", manifest)
	lockValue := map[string]any{
		"schema":    "clangup.build-lock/v1",
		"release":   map[string]any{"channel": "default", "version": "22.1.8", "release": 1},
		"source":    map[string]any{"sha256": sourceDigest, "patches": []any{}, "patchset_sha256": "empty"},
		"targets":   []any{map[string]any{"triple": "x86_64-unknown-linux-gnu", "required": true}},
		"changelog": []any{map[string]any{"release": 1, "date": "2026-07-11", "summary": "initial"}},
	}
	lock, _ := json.Marshal(lockValue)
	lock = append(lock, '\n')
	lockDigest := write("spec.lock.json", lock)
	buildRecordValue := map[string]any{
		"schema": "clangup.build-record/v1", "release": map[string]any{"channel": "default", "version": "22.1.8", "release": 1},
		"target": "x86_64-unknown-linux-gnu", "locked_spec_sha256": lockDigest, "artifact_sha256": payloadDigest,
	}
	buildRecord, _ := json.Marshal(buildRecordValue)
	buildRecord = append(buildRecord, '\n')
	buildRecordDigest := write("build-records/x86_64-unknown-linux-gnu/build-record.json", buildRecord)
	descriptorValue := map[string]any{
		"schema": "clangup.release-bundle/v1", "channel": "default", "version": "22.1.8", "release": 1,
		"locked_spec": "spec.lock.json", "locked_spec_sha256": lockDigest,
		"artifacts": []any{map[string]any{
			"target": "x86_64-unknown-linux-gnu", "manifest": "manifests/x86_64-unknown-linux-gnu/manifest.json",
			"manifest_sha256": manifestDigest, "payload": "artifacts/clang.tar.zst", "payload_sha256": payloadDigest,
			"build_record": "build-records/x86_64-unknown-linux-gnu/build-record.json", "build_record_sha256": buildRecordDigest,
		}},
		"objects": []any{map[string]any{"kind": "source", "path": "objects/sources/source.tar.xz", "sha256": sourceDigest}},
	}
	descriptor, _ := json.Marshal(descriptorValue)
	write("bundle.json", append(descriptor, '\n'))
	return filepath.Join(root, "bundle.json")
}

func digest(contents []byte) string {
	value := sha256.Sum256(contents)
	return hex.EncodeToString(value[:])
}
