package cmk

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestResolveExplicitFilesPreservesOrderAndDeduplicates(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first.cc")
	second := filepath.Join(root, "README.md")
	for _, file := range []string{first, second} {
		if err := os.WriteFile(file, []byte("test\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	files, err := resolveExplicitFiles([]string{first, second, first})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{first, second}; !slices.Equal(files, want) {
		t.Fatalf("resolveExplicitFiles = %v, want %v", files, want)
	}
}

func TestSelectFilesDefaultsToTrackedChanges(t *testing.T) {
	root := initGitRepo(t)
	tracked := filepath.Join(root, "tracked.cc")
	header := filepath.Join(root, "tracked.h")
	ignored := filepath.Join(root, "generated", "ignored.cc")
	for path, content := range map[string]string{
		tracked: "int tracked = 1;\n",
		header:  "#pragma once\n",
		ignored: "int ignored = 1;\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-m", "initial")

	if err := os.WriteFile(tracked, []byte("int tracked = 2;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(header, []byte("#pragma once\n// changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ignored, []byte("int ignored = 2;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "untracked.cc"), []byte("int x;\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := selectFiles(
		&Project{Root: root}, false, false, false,
		[]string{"generated/**"}, false,
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{header, tracked}
	slices.Sort(want)
	if !slices.Equal(files, want) {
		t.Fatalf("selectFiles changed = %v, want %v", files, want)
	}
}

func TestSelectLintScopeFilesCommitAndBranch(t *testing.T) {
	root := initGitRepo(t)
	gitRun(t, root, "branch", "-M", "main")
	tracked := filepath.Join(root, "tracked.cc")
	if err := os.WriteFile(tracked, []byte("int value = 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-m", "base")
	gitRun(t, root, "checkout", "-q", "-b", "feature")

	added := filepath.Join(root, "added.cpp")
	ignored := filepath.Join(root, "generated", "ignored.cc")
	if err := os.WriteFile(tracked, []byte("int value = 2;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(added, []byte("int added = 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(ignored), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ignored, []byte("int ignored = 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-m", "feature")

	p := &Project{Root: root, Cfg: &Config{Lint: LintCfg{Ignore: []string{"generated/**"}}}}
	want := []string{added, tracked}
	slices.Sort(want)
	for name, options := range map[string]lintOptions{
		"commit": {Commit: "HEAD"},
		"branch": {Branch: "auto"},
	} {
		t.Run(name, func(t *testing.T) {
			files, err := selectLintScopeFiles(p, options)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(files, want) {
				t.Fatalf("files = %v, want %v", files, want)
			}
		})
	}
}

func TestIsCppFileMatchesRustExtensions(t *testing.T) {
	accepted := []string{
		"a.c", "a.h", "a.cc", "a.cpp", "a.cxx", "a.c++",
		"a.hh", "a.hpp", "a.hxx", "a.h++", "a.ixx", "a.cppm",
		"a.ccm", "a.cxxm", "a.c++m", "a.mxx", "a.mpp",
	}
	for _, path := range accepted {
		if !isCppFile(path) {
			t.Errorf("isCppFile(%q) = false", path)
		}
	}
	for _, path := range []string{"a.C", "a.H", "a.inl", "a.ipp", "README.md"} {
		if isCppFile(path) {
			t.Errorf("isCppFile(%q) = true", path)
		}
	}
}

func initGitRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitRun(t, root, "init", "-q")
	gitRun(t, root, "config", "user.name", "cmk test")
	gitRun(t, root, "config", "user.email", "cmk@example.invalid")
	return root
}

func gitRun(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}
