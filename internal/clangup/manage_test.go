package clangup

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/zhscn/clangup/internal/clangup/toolchain"
)

// installFixture records a fake installed toolchain under CLANGUP_HOME and
// returns its prefix.
func installFixture(t *testing.T, channel, version string, release int, target string) string {
	t.Helper()
	root, err := toolchain.DataRoot()
	if err != nil {
		t.Fatal(err)
	}
	exact := fmt.Sprintf("%s-%d", version, release)
	prefix := filepath.Join(root, "toolchains", channel, exact, target)
	if err := os.MkdirAll(filepath.Join(prefix, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"clang", "clang++"} {
		if err := os.WriteFile(filepath.Join(prefix, "bin", name), []byte(name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	record := toolchain.InstallRecord{
		Channel: channel, Version: version, Release: release, Target: target, Prefix: prefix,
		ManifestSHA256: "manifest", ArtifactSHA256: "artifact",
	}
	if err := toolchain.RecordInstall(record); err != nil {
		t.Fatal(err)
	}
	return prefix
}

func TestFindInstalledMatchesAndReportsAmbiguity(t *testing.T) {
	t.Setenv("CLANGUP_HOME", t.TempDir())
	x86 := installFixture(t, "libcxx", "22.1.8", 1, "x86_64-unknown-linux-gnu")
	installFixture(t, "libcxx", "22.1.8", 1, "aarch64-unknown-linux-gnu")
	installFixture(t, "default", "21.1.0", 2, "x86_64-unknown-linux-gnu")

	// An ID or a prefix names exactly one install.
	for _, selector := range []string{"libcxx@22.1.8-1#x86_64-unknown-linux-gnu", x86} {
		record, err := findInstalled(selector)
		if err != nil || record.Prefix != x86 {
			t.Fatalf("findInstalled(%q) = %#v, %v", selector, record, err)
		}
	}
	// A channel installed for two targets is ambiguous, and the message
	// has to list what the user can pick from.
	_, err := findInstalled("libcxx")
	if err == nil {
		t.Fatal("ambiguous selector resolved")
	}
	for _, want := range []string{"ambiguous", "x86_64-unknown-linux-gnu", "aarch64-unknown-linux-gnu"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not mention %q", err, want)
		}
	}
	if _, err := findInstalled("nope"); err == nil {
		t.Fatal("unknown selector resolved")
	}
}

func TestRemoveToolchainClearsTheDefault(t *testing.T) {
	t.Setenv("CLANGUP_HOME", t.TempDir())
	prefix := installFixture(t, "libcxx", "22.1.8", 1, "x86_64-unknown-linux-gnu")
	keep := installFixture(t, "default", "21.1.0", 2, "x86_64-unknown-linux-gnu")
	if err := toolchain.SetDefault(prefix); err != nil {
		t.Fatal(err)
	}

	record, err := findInstalled(prefix)
	if err != nil {
		t.Fatal(err)
	}
	if err := removeToolchain(record); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(prefix); !os.IsNotExist(err) {
		t.Errorf("prefix survived removal: %v", err)
	}
	records, defaultPrefix, err := installedToolchains()
	if err != nil {
		t.Fatal(err)
	}
	if defaultPrefix != "" {
		t.Errorf("default still points at the removed toolchain: %s", defaultPrefix)
	}
	if len(records) != 1 || records[0].Prefix != keep {
		t.Errorf("remaining installs = %#v, want only %s", records, keep)
	}
}

func TestCollectGarbageRemovesPartialsAndStaleRecords(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLANGUP_HOME", home)
	t.Setenv("CLANGUP_CACHE_HOME", filepath.Join(home, "cache"))
	prefix := installFixture(t, "libcxx", "22.1.8", 1, "x86_64-unknown-linux-gnu")

	partial := filepath.Join(home, "cache", "objects", "sha256", "abc.partial")
	staging := filepath.Join(home, "toolchains", ".clangup-install-1234")
	if err := os.MkdirAll(filepath.Dir(partial), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(partial, []byte("half"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(staging, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A toolchain whose tree was deleted by hand leaves a dangling record.
	if err := os.RemoveAll(prefix); err != nil {
		t.Fatal(err)
	}

	removed, err := collectGarbage()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(removed, partial) || !slices.Contains(removed, staging) {
		t.Fatalf("removed = %v, want both %s and %s", removed, partial, staging)
	}
	records, _, err := installedToolchains()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Errorf("dangling install record survived: %#v", records)
	}
}
