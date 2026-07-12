package spec

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultSpecMatchesGoldenLock(t *testing.T) {
	loaded, err := Load(filepath.Join("..", "..", "..", "..", "specs", "default", "22.1.8", "spec.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	locked, err := Lock(loaded)
	if err != nil {
		t.Fatal(err)
	}
	contents, err := MarshalCanonical(locked)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join("testdata", "default.lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != strings.TrimSpace(string(want)) {
		t.Fatalf("default lock differs from golden file\ngot:  %s\nwant: %s", contents, want)
	}
	if locked.Source.Patches == nil {
		t.Fatal("patches must be an empty array, not null")
	}
	if got, want := locked.Source.PatchsetSHA256, "f3a1b0067f796eb0c2f1834f66518650ecda921a965f21a00dd5c392aa18c28c"; got != want {
		t.Fatalf("patchset digest = %s, want %s", got, want)
	}
	if got := locked.Targets[0]; got.Triple != "aarch64-unknown-linux-gnu" {
		t.Fatalf("targets are not sorted by triple: %#v", locked.Targets)
	}
	if got := locked.Targets[1]; got.Triple != "arm64-apple-darwin" || len(got.RuntimeDelivery) != 0 || got.Driver.RTLib != "compiler-rt" {
		t.Fatalf("macOS override was not fully expanded: %#v", got)
	}
}

func TestLoadRejectsUnknownField(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "spec.yaml")
	writeTestSpec(t, path, "mystery: true\n")
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "field mystery not found") {
		t.Fatalf("Load() error = %v, want unknown field error", err)
	}
}

func TestDecodeYAMLRejectsUnsupportedSyntax(t *testing.T) {
	tests := map[string]string{
		"duplicate key": "schema: one\nschema: two\n",
		"anchor":        "schema: &schema clangup.build/v1\n",
		"alias":         "schema: &schema clangup.build/v1\ncopy: *schema\n",
		"custom tag":    "schema: !schema clangup.build/v1\n",
		"multiple docs": "schema: one\n---\nschema: two\n",
	}
	for name, contents := range tests {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "input.yaml")
			if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
				t.Fatal(err)
			}
			var value map[string]any
			if err := decodeYAMLFile(path, &value); err == nil {
				t.Fatal("decodeYAMLFile() succeeded")
			}
		})
	}
}

func TestLoadRequiresYAMLExtension(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spec.yml")
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), ".yaml extension") {
		t.Fatalf("Load() error = %v, want extension error", err)
	}
}

func TestLoadVerifiesPatchSeries(t *testing.T) {
	directory := t.TempDir()
	patchDirectory := filepath.Join(directory, "patches")
	if err := os.MkdirAll(patchDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	patch := []byte("diff --git a/a b/a\n--- a/a\n+++ b/a\n@@ -1 +1 @@\n-old\n+new\n")
	patchPath := filepath.Join(patchDirectory, "0001.patch")
	if err := os.WriteFile(patchPath, patch, 0o644); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(patch)
	series := fmt.Sprintf("schema: %q\nstrip: 1\npatches:\n  - path: %q\n    sha256: %q\n",
		"clangup.patch-series/v1", "patches/0001.patch", fmt.Sprintf("%x", digest))
	if err := os.WriteFile(filepath.Join(patchDirectory, "series.yaml"), []byte(series), 0o644); err != nil {
		t.Fatal(err)
	}
	specPath := filepath.Join(directory, "spec.yaml")
	writeTestSpec(t, specPath, "  patch_series: patches/series.yaml\n")
	loaded, err := Load(specPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Patches) != 1 || loaded.Patches[0].SHA256 != fmt.Sprintf("%x", digest) {
		t.Fatalf("loaded patches = %#v", loaded.Patches)
	}

	if err := os.WriteFile(patchPath, append(patch, 'x'), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = Load(specPath)
	if err == nil || !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("Load() error = %v, want patch digest mismatch", err)
	}
}

func TestLoadRejectsSymlinkedPatch(t *testing.T) {
	directory := t.TempDir()
	patchDirectory := filepath.Join(directory, "patches")
	if err := os.MkdirAll(patchDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	realPatch := filepath.Join(directory, "real.patch")
	patch := []byte("patch")
	if err := os.WriteFile(realPatch, patch, 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(patchDirectory, "0001.patch")
	if err := os.Symlink(realPatch, link); err != nil {
		t.Skipf("symlink is unavailable: %v", err)
	}
	digest := sha256.Sum256(patch)
	series := fmt.Sprintf("schema: %q\nstrip: 1\npatches:\n  - path: %q\n    sha256: %q\n",
		"clangup.patch-series/v1", "patches/0001.patch", fmt.Sprintf("%x", digest))
	if err := os.WriteFile(filepath.Join(patchDirectory, "series.yaml"), []byte(series), 0o644); err != nil {
		t.Fatal(err)
	}
	specPath := filepath.Join(directory, "spec.yaml")
	writeTestSpec(t, specPath, "  patch_series: patches/series.yaml\n")
	_, err := Load(specPath)
	if err == nil || !strings.Contains(err.Error(), "symlink component") {
		t.Fatalf("Load() error = %v, want symlink rejection", err)
	}
}

func writeTestSpec(t *testing.T, path, sourceExtra string) {
	t.Helper()
	contents := fmt.Sprintf(`schema: clangup.build/v1
channel: test
version: 1.0.0
release: 1
%ssource:
  url: https://example.com/source.tar.xz
  sha256: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
%sdistribution:
  projects: [clang]
  runtimes: [libcxx]
runtime_delivery:
  libcxx:
    linkage: static
driver:
  libc: system
  cxx_stdlib: system
  cxx_stdlib_linkage: system
  linker: system
  rtlib: system
  unwindlib: system
targets:
  - os: linux
    arch: x86_64
    triple: x86_64-unknown-linux-gnu
    libc: glibc
    libc_version: "2.17"
    required: true
changelog:
  - release: 1
    date: "2026-07-11"
    summary: initial
`, sourceExtraBeforeMapping(sourceExtra), sourceExtraWithinSource(sourceExtra))
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func sourceExtraBeforeMapping(extra string) string {
	if strings.HasPrefix(extra, "mystery") {
		return extra
	}
	return ""
}

func sourceExtraWithinSource(extra string) string {
	if strings.HasPrefix(extra, "mystery") {
		return ""
	}
	return extra
}
