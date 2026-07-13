package clangup

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zhscn/clangup/internal/clangup/toolchain"
)

func TestCompareNumericVersion(t *testing.T) {
	for _, test := range []struct {
		left, right string
		want        int
	}{
		{left: "2.28", right: "2.17", want: 1},
		{left: "2.17", right: "2.17.0", want: 0},
		{left: "11.0", right: "12.0", want: -1},
	} {
		if got := compareNumericVersion(test.left, test.right); got != test.want {
			t.Fatalf("compareNumericVersion(%q, %q) = %d, want %d", test.left, test.right, got, test.want)
		}
	}
}

func TestInstalledExactResolvesImportedChannel(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLANGUP_HOME", root)
	prefix := filepath.Join(root, "toolchains", "libcxx", "22.1.8-1", "x86_64-unknown-linux-gnu")
	if err := os.MkdirAll(filepath.Join(prefix, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"clang", "clang++"} {
		if err := os.WriteFile(filepath.Join(prefix, "bin", name), []byte(name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	record := toolchain.InstallRecord{
		Channel: "libcxx", Version: "22.1.8", Release: 1,
		Target: "x86_64-unknown-linux-gnu", Prefix: prefix,
		ManifestSHA256: "manifest", ArtifactSHA256: "artifact",
		Driver: map[string]any{"cxx_stdlib": map[string]any{"name": "libc++"}},
	}
	if err := toolchain.RecordInstall(record); err != nil {
		t.Fatal(err)
	}
	installed, err := installedExact("libcxx@22.1.8-1", "")
	if err != nil || installed == nil || installed.Channel != "libcxx" {
		t.Fatalf("installed = %#v, %v", installed, err)
	}
	result := resolveResultForInstalled("libcxx@22.1.8-1", installed)
	if result.Channel != "libcxx" || result.Driver["cxx_stdlib"] == nil {
		t.Fatalf("result = %#v", result)
	}
}

func TestUnknownCommandIsInvalidRequest(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := Run([]string{"unknown", "--format=json"}, &stdout, &stderr, "test")
	if exitCode != 2 {
		t.Fatalf("exit code = %d, want 2; stderr = %s", exitCode, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"code":"invalid_request"`) {
		t.Fatalf("unexpected error: %s", stdout.String())
	}
}
