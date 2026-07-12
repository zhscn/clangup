package cmk

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveToolchainUsesClangupChannelInterface(t *testing.T) {
	directory := t.TempDir()
	prefix := filepath.Join(directory, "toolchain")
	if err := os.MkdirAll(filepath.Join(prefix, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"clang", "clang++"} {
		if err := os.WriteFile(filepath.Join(prefix, "bin", name), nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	toolchainFile := filepath.Join(prefix, "toolchain.cmake")
	if err := os.WriteFile(toolchainFile, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(directory, "clangup.args")
	result := fmt.Sprintf(`{"schema":"clangup.resolve/v1","channel":"libcxx","version":"22.1.8","release":1,"target":"x86_64-unknown-linux-gnu","manifest_sha256":"%s","artifact_sha256":"%s","driver":{"cxx_stdlib":{"name":"libc++"}},"install":{"prefix":%q,"cc":%q,"cxx":%q,"toolchain_file":%q,"tools":{}}}`,
		strings.Repeat("a", 64), strings.Repeat("b", 64), prefix,
		filepath.Join(prefix, "bin", "clang"), filepath.Join(prefix, "bin", "clang++"), toolchainFile)
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
	if lock.Toolchain.Selector != "libcxx@22.1.8-1" || lock.Toolchain.Target != "x86_64-unknown-linux-gnu" {
		t.Fatalf("unexpected lock: %#v", lock.Toolchain)
	}
	arguments, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if string(arguments) != "ensure libcxx --format=json\n" {
		t.Fatalf("clangup arguments = %q", arguments)
	}
}

func TestEffectiveSelectorUsesMatchingChannelPin(t *testing.T) {
	lock := &Lock{Toolchain: LockToolchain{Selector: "libcxx@22.1.8-1"}}
	if got := effectiveSelector("libcxx", lock); got != "libcxx@22.1.8-1" {
		t.Fatalf("effectiveSelector() = %q", got)
	}
	if got := effectiveSelector("default", lock); got != "default" {
		t.Fatalf("effectiveSelector() crossed channels: %q", got)
	}
}
