package toolchain

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstallStateAndDefaultLinks(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLANGUP_HOME", root)
	prefix := filepath.Join(root, "toolchains", "example", "default", "1-1", "target")
	if err := os.MkdirAll(filepath.Join(prefix, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"clang", "clang++"} {
		if err := os.WriteFile(filepath.Join(prefix, "bin", name), []byte(name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	record := InstallRecord{Channel: "example.com/llvm/default", Version: "1", Release: 1, Target: "target", Prefix: prefix, ManifestSHA256: "manifest", ArtifactSHA256: "artifact"}
	if err := RecordInstall(record); err != nil {
		t.Fatal(err)
	}
	installed, err := ListInstalls()
	if err != nil || len(installed) != 1 || installed[0].ID() != "example.com/llvm/default@1-1#target" {
		t.Fatalf("installs = %#v, %v", installed, err)
	}
	if err := SetDefault(prefix); err != nil {
		t.Fatal(err)
	}
	bin, _ := BinRoot()
	resolved, err := filepath.EvalSymlinks(filepath.Join(bin, "clang"))
	if err != nil || resolved != filepath.Join(prefix, "bin", "clang") {
		t.Fatalf("default clang = %q, %v", resolved, err)
	}
	if err := ClearDefault(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(bin, "clang")); !os.IsNotExist(err) {
		t.Fatalf("default link remains: %v", err)
	}
}
