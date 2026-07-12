package channel

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultReleasePlan(t *testing.T) {
	loaded, err := Load(filepath.Join("..", "..", "..", "channels", "default", "22.1.8", "release.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Lock(loaded)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Release.Channel != "default" || len(plan.Targets) != 3 || len(plan.Source.Patches) != 1 {
		t.Fatalf("unexpected default plan: %#v", plan)
	}
	if plan.Targets[0].Triple != "aarch64-unknown-linux-gnu" || plan.Targets[1].Driver.RTLib != "compiler-rt" {
		t.Fatalf("target policy was not resolved: %#v", plan.Targets)
	}
}

func TestLoadRejectsUnknownField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "release.yaml")
	writeTestRelease(t, path, "mystery: true\n")
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "field mystery not found") {
		t.Fatalf("Load() error = %v", err)
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
	series := fmt.Sprintf("schema: %q\nstrip: 1\npatches:\n  - path: %q\n    sha256: %q\n", "clangup.patch-series/v1", "patches/0001.patch", fmt.Sprintf("%x", digest))
	if err := os.WriteFile(filepath.Join(patchDirectory, "series.yaml"), []byte(series), 0o644); err != nil {
		t.Fatal(err)
	}
	releasePath := filepath.Join(directory, "release.yaml")
	writeTestRelease(t, releasePath, "  patch_series: patches/series.yaml\n")
	if _, err := Load(releasePath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(patchPath, append(patch, 'x'), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(releasePath); err == nil || !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("Load() error = %v", err)
	}
}

func writeTestRelease(t *testing.T, path, sourceExtra string) {
	t.Helper()
	topLevel, source := "", sourceExtra
	if strings.HasPrefix(sourceExtra, "mystery") {
		topLevel, source = sourceExtra, ""
	}
	contents := fmt.Sprintf(`schema: clangup.channel/v1
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
  libcxx: {linkage: static}
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
  - {release: 1, date: "2026-07-11", summary: initial}
`, topLevel, source)
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
