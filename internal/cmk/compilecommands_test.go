package cmk

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFilterCompileCommands(t *testing.T) {
	db := `[
	  {"directory":"/b","file":"/s/main.cc","output":"CMakeFiles/x.dir/Debug/main.cc.o","command":"clang -c /s/main.cc -o CMakeFiles/x.dir/Debug/main.cc.o"},
	  {"directory":"/b","file":"/s/main.cc","output":"CMakeFiles/x.dir/Release/main.cc.o","command":"clang -c /s/main.cc -o CMakeFiles/x.dir/Release/main.cc.o"},
	  {"directory":"/b","file":"/s/main.cc","output":"CMakeFiles/x.dir/Asan/main.cc.o","command":"clang -c /s/main.cc -o CMakeFiles/x.dir/Asan/main.cc.o"},
	  {"directory":"/b","file":"/s/a.cc","output":"CMakeFiles/x.dir/Asan/a.cc.o","command":"clang -c /s/a.cc"}
	]`
	out, err := filterCompileCommands([]byte(db), "Asan")
	if err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 Asan entries, got %d: %s", len(got), out)
	}
	for _, e := range got {
		if o, _ := e["output"].(string); !strings.Contains(o, "/Asan/") {
			t.Errorf("kept a non-Asan entry: %v", e)
		}
	}

	// Older CMake omits "output": fall back to matching the command.
	db2 := `[{"directory":"/b","file":"/s/m.cc","command":"clang -c /s/m.cc -o CMakeFiles/x.dir/Release/m.cc.o"}]`
	out2, err := filterCompileCommands([]byte(db2), "Release")
	if err != nil {
		t.Fatal(err)
	}
	var got2 []map[string]any
	if err := json.Unmarshal(out2, &got2); err != nil {
		t.Fatal(err)
	}
	if len(got2) != 1 {
		t.Fatalf("command fallback should keep the Release entry: %s", out2)
	}

	// A configuration nothing was built for yields an empty (but valid) array.
	out3, err := filterCompileCommands([]byte(db), "MinSizeRel")
	if err != nil {
		t.Fatal(err)
	}
	var got3 []map[string]any
	if err := json.Unmarshal(out3, &got3); err != nil {
		t.Fatalf("empty result must still be valid JSON: %v\n%s", err, out3)
	}
	if len(got3) != 0 {
		t.Fatalf("want 0 entries, got %d", len(got3))
	}
}

func TestSyncRootCompileCommands(t *testing.T) {
	root := t.TempDir()
	buildDir := filepath.Join(root, "build")
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		t.Fatal(err)
	}
	db := `[
	  {"file":"/s/main.cc","output":"CMakeFiles/x.dir/Debug/main.cc.o","command":"clang -c /s/main.cc"},
	  {"file":"/s/main.cc","output":"CMakeFiles/x.dir/Asan/main.cc.o","command":"clang -c /s/main.cc"}
	]`
	buildDB := filepath.Join(buildDir, "compile_commands.json")
	if err := os.WriteFile(buildDB, []byte(db), 0o644); err != nil {
		t.Fatal(err)
	}

	p := &Project{Root: root, Cfg: &Config{Configure: ConfigureCfg{
		Generator:            "Ninja Multi-Config",
		Configurations:       []*ConfigurationCfg{{Name: "Debug"}, {Name: "Asan"}},
		DefaultConfiguration: "Asan",
		CompileCommands:      "default",
	}}}
	rootDB := filepath.Join(root, "compile_commands.json")

	// First sync narrows the root copy to the default (Asan) configuration.
	if err := p.syncRootCompileCommands(buildDir, nil); err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	data, _ := os.ReadFile(rootDB)
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("root DB not valid JSON: %v", err)
	}
	if len(got) != 1 || !strings.Contains(got[0]["output"].(string), "/Asan/") {
		t.Fatalf("root DB should hold the single Asan entry, got: %s", data)
	}

	// A second sync with no change must NOT rewrite the file — clangd keys
	// off mtime, so a no-op reconfigure should leave it untouched.
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(rootDB, old, old); err != nil {
		t.Fatal(err)
	}
	if err := p.syncRootCompileCommands(buildDir, nil); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(rootDB); !fi.ModTime().Equal(old) {
		t.Errorf("unchanged DB was rewritten (mtime moved from %v to %v)", old, fi.ModTime())
	}

	// When the build DB actually changes, the root copy is rewritten.
	db2 := `[{"file":"/s/main.cc","output":"CMakeFiles/x.dir/Asan/main.cc.o","command":"clang -O2 -c /s/main.cc"}]`
	if err := os.WriteFile(buildDB, []byte(db2), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := p.syncRootCompileCommands(buildDir, nil); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(rootDB); fi.ModTime().Equal(old) {
		t.Error("changed DB was not rewritten")
	}
}
