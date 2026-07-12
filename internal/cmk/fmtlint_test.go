package cmk

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestSelectFilesMultipleExplicitFiles(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"src/a.cc", "include/a.h"} {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("// test\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	p := &Project{Root: root}
	files, err := selectFiles(p, []string{
		filepath.Join(root, "src/a.cc"),
		filepath.Join(root, "include/a.h"),
		filepath.Join(root, "src/a.cc"),
	}, false, false, false, true, nil)
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"src/a.cc", "include/a.h"}
	if !slices.Equal(files, want) {
		t.Fatalf("selectFiles explicit = %v, want %v", files, want)
	}
}

func TestSelectFilesExplicitFilesHonorIgnore(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"src/a.cc", "gen/a.cc"} {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("// test\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	p := &Project{Root: root}
	files, err := selectFiles(p, []string{
		filepath.Join(root, "src/a.cc"),
		filepath.Join(root, "gen/a.cc"),
	}, false, false, false, true, []string{"gen/**"})
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"src/a.cc"}
	if !slices.Equal(files, want) {
		t.Fatalf("selectFiles explicit ignore = %v, want %v", files, want)
	}
}

func TestSelectFilesExplicitFilesSkipUnsupportedExtensions(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"src/a.cc", "include/a.hpp", "README.md", "scripts/gen.py"} {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("// test\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	p := &Project{Root: root}
	files, err := selectFiles(p, []string{
		filepath.Join(root, "src/a.cc"),
		filepath.Join(root, "README.md"),
		filepath.Join(root, "include/a.hpp"),
		filepath.Join(root, "scripts/gen.py"),
	}, false, false, false, true, nil)
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"src/a.cc", "include/a.hpp"}
	if !slices.Equal(files, want) {
		t.Fatalf("selectFiles explicit unsupported = %v, want %v", files, want)
	}
}

func TestSelectFilesExplicitFilesConflictWithModes(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "src/a.cc")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("// test\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := &Project{Root: root}
	_, err := selectFiles(p, []string{file}, true, false, false, true, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "pass file(s)") {
		t.Fatalf("error = %v, want it to mention explicit files", err)
	}
}

func TestIsCppFileCommonExtensions(t *testing.T) {
	tests := []struct {
		path        string
		withHeaders bool
		sourceOnly  bool
	}{
		{"src/a.c", true, true},
		{"src/a.C", true, true},
		{"src/a.c++", true, true},
		{"src/a.cc", true, true},
		{"src/a.cpp", true, true},
		{"src/a.cxx", true, true},
		{"src/a.cppm", true, true},
		{"src/a.ixx", true, true},
		{"include/a.h", true, false},
		{"include/a.H++", true, false},
		{"include/a.hh", true, false},
		{"include/a.hpp", true, false},
		{"include/a.hxx", true, false},
		{"include/a.inl", true, false},
		{"include/a.ipp", true, false},
		{"include/a.tpp", true, false},
		{"include/a.txx", true, false},
		{"README.md", false, false},
		{"CMakeLists.txt", false, false},
		{"include/a.inc", false, false},
	}

	for _, tt := range tests {
		if got := isCppFile(tt.path, true); got != tt.withHeaders {
			t.Errorf("isCppFile(%q, true) = %v, want %v", tt.path, got, tt.withHeaders)
		}
		if got := isCppFile(tt.path, false); got != tt.sourceOnly {
			t.Errorf("isCppFile(%q, false) = %v, want %v", tt.path, got, tt.sourceOnly)
		}
	}
}

func TestClangFormatWouldChangeReportsRealErrors(t *testing.T) {
	if _, err := exec.LookPath("clang-format"); err != nil {
		t.Skip("clang-format not found")
	}

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".clang-format"), []byte("BasedOnStyle: [\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(root, "a.cc")
	if err := os.WriteFile(src, []byte("int main() { return 0; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := clangFormatWouldChange(src); err == nil {
		t.Fatal("expected clang-format error")
	}
}
