package cmk

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/zhscn/clangup/internal/clangup"
	"github.com/zhscn/clangup/internal/clangup/toolchain"
)

func TestResolveToolchainUsesClangupInProcess(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLANGUP_HOME", home)
	target := hostTarget()
	prefix := filepath.Join(home, "toolchains", "libcxx", "23.1.0-1", target)
	if err := os.MkdirAll(filepath.Join(prefix, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"clang", "clang++", "clang-format", "clang-tidy"} {
		if err := os.WriteFile(filepath.Join(prefix, "bin", name), []byte(name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	toolchainFile := filepath.Join(prefix, "toolchain.cmake")
	if err := os.WriteFile(toolchainFile, []byte("# toolchain\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := toolchain.RecordInstall(toolchain.InstallRecord{
		Channel:        "libcxx",
		Version:        "23.1.0",
		Release:        1,
		Target:         target,
		Prefix:         prefix,
		ManifestSHA256: strings.Repeat("a", 64),
		ArtifactSHA256: strings.Repeat("b", 64),
		Driver:         map[string]any{"cxx_stdlib": map[string]any{"name": "libc++"}},
	}); err != nil {
		t.Fatal(err)
	}

	var calls []string
	prevEnsure := clangupEnsure
	clangupEnsure = func(selector, requestedTarget string) (*clangup.ResolveResult, error) {
		calls = append(calls, selector+"#"+requestedTarget)
		if selector == "libcxx" {
			selector = "libcxx@23.1.0-1"
		}
		return clangup.Ensure(selector, requestedTarget)
	}
	t.Cleanup(func() { clangupEnsure = prevEnsure })

	lock := &Lock{}
	tc, dirty, err := resolveToolchain("libcxx", lock)
	if err != nil {
		t.Fatal(err)
	}
	if !dirty || tc.Selector != "libcxx@23.1.0-1" || tc.CXXStdlib != "libc++" || tc.File != toolchainFile {
		t.Fatalf("unexpected toolchain: %#v, dirty=%v", tc, dirty)
	}
	pin := lock.Toolchains[hostPlatform(runtime.GOOS, runtime.GOARCH)]
	if pin == nil || pin.Selector != "libcxx@23.1.0-1" || pin.Target != target {
		t.Fatalf("unexpected lock: %#v", lock.Toolchains)
	}
	if len(calls) != 1 || calls[0] != "libcxx#"+target {
		t.Fatalf("first resolve calls = %v", calls)
	}
	if tidy, err := tc.command("clang-tidy"); err != nil || tidy != filepath.Join(prefix, "bin", "clang-tidy") {
		t.Fatalf("clang-tidy = %q, %v", tidy, err)
	}

	// Second resolve uses the exact pinned selector directly from CLANGUP_HOME
	// without requiring a clangup binary on PATH or network access.
	t.Setenv("PATH", t.TempDir())
	if _, dirty, err := resolveToolchain("libcxx", lock); err != nil || dirty {
		t.Fatalf("pinned resolve: dirty=%v, err=%v", dirty, err)
	}
	if len(calls) != 2 || calls[1] != "libcxx@23.1.0-1#"+target {
		t.Fatalf("pinned resolve calls = %v", calls)
	}

	located, err := locateToolchain("libcxx", lock)
	if err != nil || located == nil || located.Root != prefix || located.File != toolchainFile {
		t.Fatalf("locateToolchain = %#v, %v", located, err)
	}
}

func TestEffectiveSelectorUsesMatchingChannelPin(t *testing.T) {
	pin := &LockToolchain{Selector: "libcxx@23.1.0-1"}
	if got := effectiveSelector("libcxx", pin); got != "libcxx@23.1.0-1" {
		t.Fatalf("effectiveSelector() = %q", got)
	}
	if got := effectiveSelector("default", pin); got != "default" {
		t.Fatalf("effectiveSelector() crossed channels: %q", got)
	}
}

func TestToolchainSelectorUsesHostPlatform(t *testing.T) {
	cfg := ToolchainCfg{
		"default": "fallback", "linux": "libcxx", "macos": "default",
		"linux-aarch64": "libcxx-pgo",
	}
	if got := cfg.selectorFor("linux", "amd64"); got != "libcxx" {
		t.Fatalf("linux selector = %q", got)
	}
	if got := cfg.selectorFor("linux", "arm64"); got != "libcxx-pgo" {
		t.Fatalf("Linux aarch64 selector = %q", got)
	}
	if got := cfg.selectorFor("darwin", "arm64"); got != "default" {
		t.Fatalf("macOS selector = %q", got)
	}
	if got := cfg.selectorFor("freebsd", "amd64"); got != "fallback" {
		t.Fatalf("fallback selector = %q", got)
	}
}
