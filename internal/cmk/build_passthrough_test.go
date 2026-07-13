package cmk

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildUsesExistingCMakeTreeWithoutManagedToolchain(t *testing.T) {
	root := t.TempDir()
	build := filepath.Join(root, "build")
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(build, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, configFileName), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(build, "CMakeCache.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(root, "cmake.args")
	cmake := filepath.Join(bin, "cmake")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" > " + shellQuote([]string{log}) + "\n"
	if err := os.WriteFile(cmake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	t.Setenv("PATH", bin)
	t.Setenv("CC", "")
	t.Setenv("CXX", "")

	if err := cmdBuild(nil, nil, buildOptions{Jobs: defaultJobs()}); err != nil {
		t.Fatal(err)
	}
	arguments, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(arguments), "--build "+build+" -j ") {
		t.Fatalf("cmake arguments = %q", arguments)
	}
	if fileExists(filepath.Join(build, injectionStampFile)) {
		t.Fatal("pass-through build wrote a cmk injection stamp")
	}
}
