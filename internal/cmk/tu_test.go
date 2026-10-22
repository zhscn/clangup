package cmk

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestTUSourcePaths(t *testing.T) {
	for _, generator := range []string{"Ninja", "Ninja Multi-Config"} {
		t.Run(generator, func(t *testing.T) {
			for _, tool := range []string{"cmake", "ninja", "cc"} {
				if _, err := exec.LookPath(tool); err != nil {
					t.Skipf("%s unavailable", tool)
				}
			}
			root := t.TempDir()
			build := filepath.Join(root, "build")
			source := filepath.Join(root, "source dir", "foo.c")
			if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
				t.Fatal(err)
			}
			for path, data := range map[string]string{
				filepath.Join(root, "CMakeLists.txt"): "cmake_minimum_required(VERSION 3.20)\nproject(tu C)\nadd_executable(app \"source dir/foo.c\")\n",
				source:                                "int main(void) { return 0; }\n",
			} {
				if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command("cmake", "-S", root, "-B", build, "-G", generator, "-DCMAKE_EXPORT_COMPILE_COMMANDS=OFF")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("configure: %v\n%s", err, out)
			}
			t.Chdir(root)
			config := ""
			object := filepath.Join(build, "CMakeFiles", "app.dir", "source_dir", "foo.c.o")
			if generator == "Ninja Multi-Config" {
				config = "Release"
				object = filepath.Join(build, "CMakeFiles", "app.dir", config, "source_dir", "foo.c.o")
			}
			relativeObject, err := filepath.Rel(build, object)
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"./source dir/foo.c", source, relativeObject, "foo.c"} {
				if err := cmdTU([]string{name}, variantFor(build, config, 1)); err != nil {
					t.Fatalf("build-tu %q: %v", name, err)
				}
				if _, err := os.Stat(object); err != nil {
					t.Fatalf("object for %q: %v", name, err)
				}
				if err := os.Remove(object); err != nil {
					t.Fatal(err)
				}
			}
			if err := cmdTU([]string{"missing.c"}, variantFor(build, config, 1)); err == nil {
				t.Fatal("missing source succeeded")
			}
			if fileExists(filepath.Join(build, "app")) {
				t.Fatal("translation unit build linked executable")
			}
		})
	}
}

func TestTUSourceTargets(t *testing.T) {
	data := []byte(`[
 {"directory":"/project/build","file":"../src/foo.c","output":"CMakeFiles/a.dir/foo.c.o"},
 {"directory":"/project/build","file":"/project/src/foo.c","output":"/project/build/CMakeFiles/b.dir/foo.c.o"},
 {"directory":"/project/build","file":"../src/foo.c","output":"CMakeFiles/a.dir/foo.c.o"},
 {"directory":"/project/build","file":"../src/foo.c","output":"unrelated"}
 ]`)
	targets := []string{"CMakeFiles/a.dir/foo.c.o", "CMakeFiles/b.dir/foo.c.o"}
	sources, err := tuSourceTargets(data, "/project/build", targets)
	if err != nil {
		t.Fatal(err)
	}
	got := sources["/project/src/foo.c"]
	if len(got) != 2 || got[0] != targets[0] || got[1] != targets[1] {
		t.Fatalf("source targets = %v", got)
	}
	if _, err := tuSourceTargets([]byte("invalid"), "/project/build", targets); err == nil {
		t.Fatal("invalid database accepted")
	}
}
