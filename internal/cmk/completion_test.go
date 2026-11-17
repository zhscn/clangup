package cmk

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runComplete(t *testing.T, args ...string) []string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Run(append([]string{"__complete"}, args...), &stdout, &stderr, "test")
	if code != 0 {
		t.Fatalf("__complete %v exited %d: %s", args, code, stderr.String())
	}
	var out []string
	for line := range strings.Lines(stdout.String()) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		name, _, _ := strings.Cut(line, "\t")
		out = append(out, name)
	}
	return out
}

func TestShellCompletionPresetsConfigsTargetsAndDeps(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("cmk.yaml", `version: 1
cmake:
  default-preset: default
  default-configuration: Debug
  presets:
    default: {build-dir: build}
    minimal: {build-dir: build-min, generator: Ninja, build-type: Release}
  configurations:
    - {name: Debug}
    - {name: Release}
    - {name: Asan, compile: [-fsanitize=address]}
dependencies:
  fmt: {script: cmk/deps/fmt.sh}
  zlib: {script: cmk/deps/zlib.sh}
`)
	overrideDir := filepath.Join(root, "local-fmt")
	if err := os.MkdirAll(overrideDir, 0o755); err != nil {
		t.Fatal(err)
	}
	write("cmk.dev.yaml", "dependencies:\n  fmt:\n    path: "+overrideDir+"\n")

	// Seed a minimal File API codemodel in build/ with one executable, one
	// static library, and one imported target (which must be filtered out).
	write("build/CMakeCache.txt", multiConfigCache)
	write("build/.cmake/api/v1/reply/index-2026-01-01T00-00-00-0000.json",
		`{"reply":{"codemodel-v2":{"jsonFile":"codemodel-v2.json"}}}`)
	write("build/.cmake/api/v1/reply/codemodel-v2.json", `{
	  "configurations": [{
	    "name": "Debug",
	    "targets": [
	      {"name": "app", "jsonFile": "target-app.json"},
	      {"name": "core", "jsonFile": "target-core.json"},
	      {"name": "Git::Git", "jsonFile": "target-git.json"}
	    ]
	  }]
	}`)
	write("build/.cmake/api/v1/reply/target-app.json", `{"name":"app","type":"EXECUTABLE","artifacts":[{"path":"Debug/app"}]}`)
	write("build/.cmake/api/v1/reply/target-core.json", `{"name":"core","type":"STATIC_LIBRARY","artifacts":[{"path":"Debug/libcore.a"}]}`)
	write("build/.cmake/api/v1/reply/target-git.json", `{"name":"Git::Git","type":"EXECUTABLE","imported":true,"artifacts":[{"path":"/usr/bin/git"}]}`)
	write("build/compile_commands.json", `[{"directory":`+`"`+filepath.ToSlash(root)+`","file":`+`"`+filepath.ToSlash(filepath.Join(root, "src/main.cpp"))+`","command":"clang++ -c src/main.cpp"}]`)
	write("src/main.cpp", "int main() {}\n")

	t.Chdir(root)

	// `cmk completion zsh` generates a non-empty completion script.
	var compOut, compErr bytes.Buffer
	if code := Run([]string{"completion", "zsh"}, &compOut, &compErr, "test"); code != 0 || !strings.Contains(compOut.String(), "compdef") {
		t.Fatalf("completion zsh exited %d: %s", code, compErr.String())
	}

	if got := runComplete(t, "config", "-p", ""); !eqStrings(got, []string{"default", "minimal"}) {
		t.Fatalf("preset completion = %v, want [default minimal]", got)
	}
	if got := runComplete(t, "build", "-c", ""); !eqStrings(got, []string{"Debug", "Release", "Asan"}) {
		t.Fatalf("config completion = %v, want [Debug Release Asan]", got)
	}
	// Single-config preset exposes no --config completions.
	if got := runComplete(t, "build", "-p", "minimal", "-c", ""); len(got) != 0 {
		t.Fatalf("single-config preset completed --config: %v", got)
	}
	// `cmk build` completes all non-imported targets; `cmk run` completes only executables.
	if got := runComplete(t, "build", ""); !eqStrings(got, []string{"app", "core"}) {
		t.Fatalf("build target completion = %v, want [app core]", got)
	}
	if got := runComplete(t, "build", "app", ""); !eqStrings(got, []string{"core"}) {
		t.Fatalf("build target dedup completion = %v, want [core]", got)
	}
	if got := runComplete(t, "build", "--", ""); len(got) != 0 {
		t.Fatalf("build passthrough after -- completed targets: %v", got)
	}
	if got := runComplete(t, "run", ""); !eqStrings(got, []string{"app"}) {
		t.Fatalf("run target completion = %v, want [app]", got)
	}
	if got := runComplete(t, "tu", ""); !eqStrings(got, []string{"src/main.cpp"}) {
		t.Fatalf("tu source completion = %v, want [src/main.cpp]", got)
	}
	// Dependency commands complete declared deps and active dev overrides.
	if got := runComplete(t, "sync", ""); !eqStrings(got, []string{"fmt", "zlib"}) {
		t.Fatalf("sync completion = %v, want [fmt zlib]", got)
	}
	if got := runComplete(t, "update", ""); !eqStrings(got, []string{"toolchain", "fmt", "zlib"}) {
		t.Fatalf("update completion = %v, want [toolchain fmt zlib]", got)
	}
	if got := runComplete(t, "dev", "--drop", ""); !eqStrings(got, []string{"fmt"}) {
		t.Fatalf("dev --drop completion = %v, want [fmt]", got)
	}
	if got := runComplete(t, "add", "spdlog", "--needs", "fmt,"); !eqStrings(got, []string{"fmt,zlib"}) {
		t.Fatalf("add --needs completion = %v, want [fmt,zlib]", got)
	}
}
