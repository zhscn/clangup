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
	write("cmk.toml", `[config]
generator = "Ninja Multi-Config"
configurations = ["Debug", "Release"]
default = "Debug"
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
	return p.reconfigureReason(dir, tc, presetForDir(p, dir))
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

	// A CMake input edit is detected and healed.
	touchAfterStamp(t, root, dir, "CMakeLists.txt")
	if got := itReason(t, p, dir); !strings.Contains(got, "CMakeLists.txt changed") {
		t.Fatalf("touched CMakeLists: reason %q", got)
	}
	if err := ensureConfigured(p, dir, configureAuto); err != nil {
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
	if err := ensureConfigured(p, dir, configureAuto); err != nil {
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
	if err := runConfigure(p, dir, nil, []string{"-DCMK_IT_EXTRA=yes"}); err != nil {
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
	if err := ensureConfigured(p, dir, configureAuto); err != nil {
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
	if err := ensureConfigured(p, dir, configureLocked); err != nil {
		t.Fatalf("locked on a current configuration: %v", err)
	}

	// Locked + stale -> error naming the reason, not a reconfigure.
	touchAfterStamp(t, root, dir, "CMakeLists.txt")
	err := ensureConfigured(p, dir, configureLocked)
	if err == nil || !strings.Contains(err.Error(), "--locked") {
		t.Fatalf("locked on a stale configuration: %v", err)
	}
	// --no-config skips the check entirely.
	if err := ensureConfigured(p, dir, configureSkip); err != nil {
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
	// Configured by hand, not by cmk — and no cmk.toml anywhere.
	dir := filepath.Join(root, "build")
	out, err := exec.Command("cmake", "-S", root, "-B", dir, "-G", "Ninja", "-DFOREIGN_OPT=1").CombinedOutput()
	if err != nil {
		t.Fatalf("manual configure: %v\n%s", err, out)
	}
	t.Chdir(root)
	// A git root would normally be required to even resolve a project;
	// fake the minimum: openProject falls back to git, so init one.
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Skipf("git init: %v\n%s", err, out)
	}
	p, err := openProject()
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureConfigured(p, dir, configureAuto); err != nil {
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
