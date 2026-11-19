//go:build integration

package cmk

// Integration tests for the reconfigure machinery, driving real cmake +
// ninja: run with `go test -tags integration`. They cover the flows unit
// tests can't: suppression of the regen rule, staleness detection against
// a real file API reply, CONFIGURE_DEPENDS drift, and preservation of
// ad-hoc configure args.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func requireTools(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"cmake", "ninja"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH", tool)
		}
	}
}

// itProject scaffolds a Ninja Multi-Config project with a
// CONFIGURE_DEPENDS glob and chdirs into it.
func itProject(t *testing.T) (*Project, string) {
	t.Helper()
	requireTools(t)
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
	write("CMakeLists.txt", `cmake_minimum_required(VERSION 3.27)
project(it CXX)
file(GLOB_RECURSE SRCS CONFIGURE_DEPENDS "src/*.cc")
add_executable(hello ${SRCS})
`)
	write("src/main.cc", "int main() { return 0; }\n")
	write("cmk.yaml", `version: 1
cmake:
  generator: Ninja Multi-Config
  default-preset: default
  default-configuration: Debug
  presets:
    default: {build-dir: build}
  configurations:
    - {name: Debug}
    - {name: Release}
    - name: Asan
      compile: [-g, -fsanitize=address]
      link: [-fsanitize=address]
`)
	t.Chdir(root)
	p, err := openProject()
	if err != nil {
		t.Fatal(err)
	}
	return p, root
}

// touchAfterStamp bumps rel's mtime to just past the configure stamp's,
// so the edit is unambiguously newer without sleeping.
func touchAfterStamp(t *testing.T, root, dir, rel string) {
	t.Helper()
	st, err := os.Stat(filepath.Join(dir, injectionStampFile))
	if err != nil {
		t.Fatal(err)
	}
	mt := st.ModTime().Add(time.Millisecond)
	if err := os.Chtimes(filepath.Join(root, rel), mt, mt); err != nil {
		t.Fatal(err)
	}
}

func itReason(t *testing.T, p *Project, dir string) string {
	t.Helper()
	tc, err := p.toolchain()
	if err != nil {
		t.Fatal(err)
	}
	return p.treeAt(dir, "").reconfigureReason(tc)
}

func TestIntegrationReconfigureLifecycle(t *testing.T) {
	p, root := itProject(t)

	// Bootstrap: no build dir -> configure the multi-config tree.
	if err := bootstrapIfUnconfigured(p, "", "", configureAuto); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "build")
	if _, err := os.Stat(filepath.Join(dir, "CMakeCache.txt")); err != nil {
		t.Fatalf("bootstrap did not configure: %v", err)
	}
	cache, err := os.ReadFile(filepath.Join(dir, "CMakeCache.txt"))
	if err != nil {
		t.Fatal(err)
	}
	foundAsanFlags := false
	for line := range strings.Lines(string(cache)) {
		if strings.HasPrefix(line, "CMAKE_CXX_FLAGS_ASAN:STRING=") && strings.Contains(line, "-fsanitize=address") {
			foundAsanFlags = true
		}
	}
	if !foundAsanFlags {
		t.Fatal("custom configuration flags missing from cache")
	}

	// The generated build system must carry no regen or glob-verify
	// rules — cmk owns reconfiguration.
	ninja, err := os.ReadFile(filepath.Join(dir, "build.ninja"))
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"RERUN_CMAKE", "verify_globs"} {
		if strings.Contains(string(ninja), marker) {
			t.Errorf("build.ninja still contains %s", marker)
		}
	}

	// Steady state: nothing stale.
	if got := itReason(t, p, dir); got != "" {
		t.Fatalf("fresh configure: unexpected reason %q", got)
	}
	for _, query := range projectModelQueries {
		if err := fileAPIReplyAvailable(dir, query); err != nil {
			t.Fatalf("configured tree has no %s file API reply: %v", query, err)
		}
	}
	cacheInfo, err := os.Stat(filepath.Join(dir, "CMakeCache.txt"))
	if err != nil {
		t.Fatal(err)
	}
	stampInfo, err := os.Stat(filepath.Join(dir, injectionStampFile))
	if err != nil {
		t.Fatal(err)
	}
	lock, err := lockBuildDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	ensured := make(chan error, 1)
	go func() { ensured <- cmdConfig(configOptions{BuildDir: dir, IfStale: true}, nil) }()
	select {
	case err := <-ensured:
		unlockFile(lock)
		t.Fatalf("config --if-stale did not wait for an active configure: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	unlockFile(lock)
	if err := <-ensured; err != nil {
		t.Fatal(err)
	}
	cacheAfter, _ := os.Stat(filepath.Join(dir, "CMakeCache.txt"))
	stampAfter, _ := os.Stat(filepath.Join(dir, injectionStampFile))
	if !cacheAfter.ModTime().Equal(cacheInfo.ModTime()) || !stampAfter.ModTime().Equal(stampInfo.ModTime()) {
		t.Fatal("config --if-stale rewrote a fresh build tree")
	}

	// A CMake input edit is detected and healed.
	touchAfterStamp(t, root, dir, "CMakeLists.txt")
	if got := itReason(t, p, dir); !strings.Contains(got, "CMakeLists.txt changed") {
		t.Fatalf("touched CMakeLists: reason %q", got)
	}
	if err := p.treeAt(dir, "").ensureConfigured(configureAuto); err != nil {
		t.Fatal(err)
	}
	if got := itReason(t, p, dir); got != "" {
		t.Fatalf("after reconfigure: unexpected reason %q", got)
	}

	// CONFIGURE_DEPENDS drift: a new file matching the glob.
	if err := os.WriteFile(filepath.Join(root, "src/extra.cc"), []byte("int extra() { return 1; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := itReason(t, p, dir); !strings.Contains(got, "file(GLOB)") {
		t.Fatalf("glob drift: reason %q", got)
	}
	if err := p.treeAt(dir, "").ensureConfigured(configureAuto); err != nil {
		t.Fatal(err)
	}
	if got := itReason(t, p, dir); got != "" {
		t.Fatalf("after glob reconfigure: unexpected reason %q", got)
	}
}

func TestIntegrationExtraArgsSurviveAutoReconfigure(t *testing.T) {
	p, root := itProject(t)
	dir := filepath.Join(root, "build")

	// An explicit configure with ad-hoc args...
	if err := p.treeAt(dir, "").configure([]string{"-DCMK_IT_EXTRA=yes"}); err != nil {
		t.Fatal(err)
	}
	cache, err := os.ReadFile(filepath.Join(dir, "CMakeCache.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cache), "CMK_IT_EXTRA:") {
		t.Fatal("ad-hoc arg missing from cache after explicit configure")
	}
	if got := itReason(t, p, dir); got != "" {
		t.Fatalf("after explicit configure: unexpected reason %q", got)
	}

	// ...survives the automatic reconfigure triggered by an input edit.
	touchAfterStamp(t, root, dir, "CMakeLists.txt")
	if err := p.treeAt(dir, "").ensureConfigured(configureAuto); err != nil {
		t.Fatal(err)
	}
	cache, err = os.ReadFile(filepath.Join(dir, "CMakeCache.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cache), "CMK_IT_EXTRA:") {
		t.Fatal("ad-hoc arg dropped by automatic reconfigure")
	}
}

func TestIntegrationLockedFailsInsteadOfHealing(t *testing.T) {
	p, root := itProject(t)
	dir := filepath.Join(root, "build")

	// Locked + nothing configured -> refuse to bootstrap.
	if err := bootstrapIfUnconfigured(p, "", "", configureLocked); err == nil {
		t.Fatal("locked bootstrap must fail on an unconfigured project")
	}
	if err := bootstrapIfUnconfigured(p, "", "", configureAuto); err != nil {
		t.Fatal(err)
	}

	// Locked + current -> fine.
	if err := p.treeAt(dir, "").ensureConfigured(configureLocked); err != nil {
		t.Fatalf("locked on a current configuration: %v", err)
	}

	// Locked + stale -> error naming the reason, not a reconfigure.
	touchAfterStamp(t, root, dir, "CMakeLists.txt")
	err := p.treeAt(dir, "").ensureConfigured(configureLocked)
	if err == nil || !strings.Contains(err.Error(), "--locked") {
		t.Fatalf("locked on a stale configuration: %v", err)
	}
	// --no-config skips the check entirely.
	if err := p.treeAt(dir, "").ensureConfigured(configureSkip); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationForeignProjectLeftAlone(t *testing.T) {
	requireTools(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "CMakeLists.txt"),
		[]byte("cmake_minimum_required(VERSION 3.27)\nproject(f NONE)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Configured by hand, not by cmk — and no cmk.yaml anywhere.
	dir := filepath.Join(root, "build")
	out, err := exec.Command("cmake", "-S", root, "-B", dir, "-G", "Ninja", "-DFOREIGN_OPT=1").CombinedOutput()
	if err != nil {
		t.Fatalf("manual configure: %v\n%s", err, out)
	}
	t.Chdir(root)
	p, err := openProject()
	if err != nil {
		t.Fatal(err)
	}
	if err := p.treeAt(dir, "").ensureConfigured(configureAuto); err != nil {
		t.Fatal(err)
	}
	cache, err := os.ReadFile(filepath.Join(dir, "CMakeCache.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cache), "FOREIGN_OPT:") {
		t.Fatal("ensureConfigured wiped a foreign build dir's cache")
	}
	if err := bootstrapIfUnconfigured(p, "", "", configureAuto); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationLintPreparesModuleModmapsAndBMIs(t *testing.T) {
	requireTools(t)
	home, _ := os.UserHomeDir()
	var clangupPrefix string
	if matches, _ := filepath.Glob(filepath.Join(home, ".local/share/clangup/toolchains/*/*/*")); len(matches) > 0 {
		for _, m := range matches {
			if fileExists(filepath.Join(m, "bin/clang-tidy")) && fileExists(filepath.Join(m, "bin/clang-scan-deps")) {
				clangupPrefix = m
				break
			}
		}
	}
	if clangupPrefix == "" {
		t.Skip("no local clangup toolchain with clang-tidy and clang-scan-deps installed")
	}

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
	write("CMakeLists.txt", `cmake_minimum_required(VERSION 3.28)
project(modlint LANGUAGES CXX)
set(CMAKE_CXX_STANDARD 23)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)
add_library(base)
target_sources(base PUBLIC FILE_SET CXX_MODULES FILES src/base.cppm)
add_library(m)
target_sources(m PUBLIC FILE_SET CXX_MODULES FILES src/m.cppm)
target_link_libraries(m PUBLIC base)
add_executable(app src/main.cc)
target_link_libraries(app PRIVATE m)
`)
	write("src/base.cppm", "export module base;\nexport int base_val() { return 40; }\n")
	write("src/m.cppm", "export module m;\nimport base;\nexport int answer() { return base_val() + 2; }\n")
	write("src/main.cc", "import m;\nint main() { return answer() - 42; }\n")
	write(".clang-tidy", "Checks: 'bugprone-*'\n")
	write("cmk.yaml", "version: 1\ncmake:\n  compile-commands: default\n  args:\n    - -DCMAKE_TOOLCHAIN_FILE="+filepath.ToSlash(filepath.Join(clangupPrefix, "toolchain.cmake"))+"\n    - -DCMAKE_CXX_COMPILER_CLANG_SCAN_DEPS="+filepath.ToSlash(filepath.Join(clangupPrefix, "bin/clang-scan-deps"))+"\nlint:\n  command: "+filepath.ToSlash(filepath.Join(clangupPrefix, "bin/clang-tidy"))+"\n")

	t.Chdir(root)
	if err := cmdConfig(configOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	// Lint an unbuilt tree. cmk can compile module providers to get their
	// BMIs, while the application's object and link steps are unnecessary.
	if err := cmdLint([]string{"src/m.cppm", "src/main.cc"}, lintOptions{}); err != nil {
		t.Fatalf("cmdLint on unbuilt C++23 module tree failed: %v", err)
	}
	if fileExists(filepath.Join(root, "build/Debug/app")) || fileExists(filepath.Join(root, "build/CMakeFiles/app.dir/Debug/src/main.cc.o")) {
		t.Fatal("cmdLint ran an application compile or link step")
	}
}
