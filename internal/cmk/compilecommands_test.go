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

	// Inspect every representation. An unrelated output must not hide a
	// configuration marker in the arguments array.
	dbArgs := `[{"directory":"/b","file":"/s/m.cc","output":"m.cc.o","arguments":["clang","-c","/s/m.cc","-o","CMakeFiles/x.dir/Release/m.cc.o"]}]`
	outArgs, err := filterCompileCommands([]byte(dbArgs), "Release")
	if err != nil {
		t.Fatal(err)
	}
	var gotArgs []map[string]any
	if err := json.Unmarshal(outArgs, &gotArgs); err != nil {
		t.Fatal(err)
	}
	if len(gotArgs) != 1 {
		t.Fatalf("arguments marker should keep the Release entry: %s", outArgs)
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

func TestLintCompilationDatabaseSelectsOneConfiguration(t *testing.T) {
	root := t.TempDir()
	buildDir := filepath.Join(root, "build")
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, configFileName), []byte("version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	db := `[
	  {"file":"/s/main.cc","output":"CMakeFiles/x.dir/Debug/main.cc.o","command":"clang -c /s/main.cc"},
	  {"file":"/s/main.cc","output":"CMakeFiles/x.dir/Release/main.cc.o","command":"clang -O3 -c /s/main.cc"}
	]`
	if err := os.WriteFile(filepath.Join(buildDir, "compile_commands.json"), []byte(db), 0o644); err != nil {
		t.Fatal(err)
	}
	preset := &PresetCfg{Generator: "Ninja Multi-Config"}
	p := &Project{Root: root, Cfg: &Config{Configure: ConfigureCfg{
		Generator:            "Ninja Multi-Config",
		DefaultConfiguration: "Debug",
		Configurations:       []*ConfigurationCfg{{Name: "Debug"}, {Name: "Release"}},
	}}}

	dir, config, err := p.lintCompilationDatabase(buildDir, preset, "Release")
	if err != nil {
		t.Fatal(err)
	}
	if dir == buildDir || config != "Release" {
		t.Fatalf("lint database = (%q, %q), want filtered Release database", dir, config)
	}
	data, err := os.ReadFile(filepath.Join(dir, "compile_commands.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.Contains(got[0]["output"].(string), "/Release/") {
		t.Fatalf("filtered database = %s", data)
	}

	old := time.Now().Add(-time.Hour)
	destination := filepath.Join(dir, "compile_commands.json")
	if err := os.Chtimes(destination, old, old); err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.lintCompilationDatabase(buildDir, preset, "Release"); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(destination); err != nil || !info.ModTime().Equal(old) {
		t.Fatalf("unchanged lint database was rewritten: info=%v err=%v", info, err)
	}
}

func TestLintCompilationDatabaseUsesForeignCMakeDefault(t *testing.T) {
	root := t.TempDir()
	buildDir := filepath.Join(root, "build")
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cache := "CMAKE_GENERATOR:INTERNAL=Ninja Multi-Config\n" +
		"CMAKE_CONFIGURATION_TYPES:STRING=Debug;Release\n" +
		"CMAKE_DEFAULT_BUILD_TYPE:STRING=Release\n"
	if err := os.WriteFile(filepath.Join(buildDir, "CMakeCache.txt"), []byte(cache), 0o644); err != nil {
		t.Fatal(err)
	}
	db := `[
	  {"file":"/s/main.cc","output":"CMakeFiles/x.dir/Debug/main.cc.o"},
	  {"file":"/s/main.cc","output":"CMakeFiles/x.dir/Release/main.cc.o"}
	]`
	if err := os.WriteFile(filepath.Join(buildDir, "compile_commands.json"), []byte(db), 0o644); err != nil {
		t.Fatal(err)
	}
	p := &Project{Root: root, Cfg: &Config{}}

	_, config, err := p.lintCompilationDatabase(buildDir, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if config != "Release" {
		t.Fatalf("selected configuration = %q, want Release", config)
	}
	if _, _, err := p.lintCompilationDatabase(buildDir, nil, "Asan"); err == nil || !strings.Contains(err.Error(), "known: Debug, Release") {
		t.Fatalf("unknown configuration error = %v", err)
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
