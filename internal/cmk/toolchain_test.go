package cmk

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestResolveToolchainUsesClangupChannelInterface(t *testing.T) {
	directory := t.TempDir()
	prefix := filepath.Join(directory, "toolchain")
	if err := os.MkdirAll(filepath.Join(prefix, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"clang", "clang++", "clang-format", "clang-tidy"} {
		if err := os.WriteFile(filepath.Join(prefix, "bin", name), nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	toolchainFile := filepath.Join(prefix, "toolchain.cmake")
	if err := os.WriteFile(toolchainFile, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(directory, "clangup.args")
	result := fmt.Sprintf(`{"schema":"clangup.resolve/v1","channel":"libcxx","version":"22.1.8","release":1,"target":"x86_64-unknown-linux-gnu","manifest_sha256":"%s","artifact_sha256":"%s","driver":{"cxx_stdlib":{"name":"libc++"}},"install":{"prefix":%q,"cc":%q,"cxx":%q,"toolchain_file":%q,"tools":{"clang-format":%q,"clang-tidy":%q}}}`,
		strings.Repeat("a", 64), strings.Repeat("b", 64), prefix,
		filepath.Join(prefix, "bin", "clang"), filepath.Join(prefix, "bin", "clang++"), toolchainFile,
		filepath.Join(prefix, "bin", "clang-format"), filepath.Join(prefix, "bin", "clang-tidy"))
	clangup := filepath.Join(directory, "clangup")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" > " + shellQuote([]string{log}) + "\nprintf '%s\\n' " + shellQuote([]string{result}) + "\n"
	if err := os.WriteFile(clangup, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory)

	lock := &Lock{}
	toolchain, dirty, err := resolveToolchain("libcxx", lock)
	if err != nil {
		t.Fatal(err)
	}
	if !dirty || toolchain.Selector != "libcxx@22.1.8-1" || toolchain.CXXStdlib != "libc++" {
		t.Fatalf("unexpected toolchain: %#v, dirty=%v", toolchain, dirty)
	}
	pin := lock.Toolchains[hostPlatform(runtime.GOOS, runtime.GOARCH)]
	if pin == nil || pin.Selector != "libcxx@22.1.8-1" || pin.Target != "x86_64-unknown-linux-gnu" {
		t.Fatalf("unexpected lock: %#v", lock.Toolchains)
	}
	arguments, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if string(arguments) != "ensure libcxx --target "+hostTarget()+" --format=json\n" {
		t.Fatalf("clangup arguments = %q", arguments)
	}
	if tidy, err := toolchain.command("clang-tidy"); err != nil || tidy != filepath.Join(prefix, "bin", "clang-tidy") {
		t.Fatalf("clang-tidy = %q, %v", tidy, err)
	}
	if _, dirty, err := resolveToolchain("libcxx", lock); err != nil || dirty {
		t.Fatalf("pinned resolve: dirty=%v, err=%v", dirty, err)
	}
	arguments, err = os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if string(arguments) != "ensure libcxx@22.1.8-1 --target x86_64-unknown-linux-gnu --format=json\n" {
		t.Fatalf("pinned clangup arguments = %q", arguments)
	}
}

func TestEffectiveSelectorUsesMatchingChannelPin(t *testing.T) {
	pin := &LockToolchain{Selector: "libcxx@22.1.8-1"}
	if got := effectiveSelector("libcxx", pin); got != "libcxx@22.1.8-1" {
		t.Fatalf("effectiveSelector() = %q", got)
	}
	if got := effectiveSelector("default", pin); got != "default" {
		t.Fatalf("effectiveSelector() crossed channels: %q", got)
	}
}

func TestToolchainSelectorUsesHostPlatform(t *testing.T) {
	cfg := ToolchainCfg{
		"selector": "fallback", "linux": "libcxx", "macos": "default",
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
