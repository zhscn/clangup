package cmk

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCompileGlobSegment(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{"*.cc", "foo.cc", true},
		{"*.cc", "foo.cch", false},
		{"*.cc", ".hidden.cc", true}, // CMake globs match dotfiles
		{"foo?.h", "foo1.h", true},
		{"foo?.h", "foo12.h", false},
		{"[a-c].txt", "b.txt", true},
		{"[a-c].txt", "d.txt", false},
		{"[!a-c].txt", "d.txt", true},
		{"[!a-c].txt", "b.txt", false},
		{"literal.cc", "literal.cc", true},
		{"literal.cc", "literalXcc", false}, // '.' must stay literal
		{"a[b", "a[b", true},                // unterminated class is literal
		{"*", "anything", true},
	}
	for _, c := range cases {
		re, err := compileGlobSegment(c.pattern)
		if err != nil {
			t.Fatalf("compileGlobSegment(%q): %v", c.pattern, err)
		}
		if got := re.MatchString(c.name); got != c.want {
			t.Errorf("pattern %q vs %q: got %v, want %v", c.pattern, c.name, got, c.want)
		}
	}
}

func TestEvalGlobDependent(t *testing.T) {
	root := t.TempDir()
	mk := func(rel string) {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("src/a.cc")
	mk("src/b.cc")
	mk("src/b.h")
	mk("src/sub/c.cc")
	mk("other/d.cc")

	slash := filepath.ToSlash

	t.Run("glob", func(t *testing.T) {
		got, err := evalGlobDependent(globDependent{
			Expression:      slash(root) + "/src/*.cc",
			ListDirectories: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		want := []string{slash(root) + "/src/a.cc", slash(root) + "/src/b.cc"}
		if !sortedEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("glob does not list matching dirs without LIST_DIRECTORIES", func(t *testing.T) {
		got, err := evalGlobDependent(globDependent{Expression: slash(root) + "/src/*"})
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range got {
			if strings.HasSuffix(m, "/sub") {
				t.Errorf("directory %s listed without LIST_DIRECTORIES", m)
			}
		}
		if len(got) != 3 {
			t.Errorf("got %v, want the 3 files in src/", got)
		}
	})

	t.Run("glob_recurse", func(t *testing.T) {
		got, err := evalGlobDependent(globDependent{
			Expression: slash(root) + "/src/*.cc",
			Recurse:    true,
		})
		if err != nil {
			t.Fatal(err)
		}
		want := []string{
			slash(root) + "/src/a.cc",
			slash(root) + "/src/b.cc",
			slash(root) + "/src/sub/c.cc",
		}
		if !sortedEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("relative", func(t *testing.T) {
		got, err := evalGlobDependent(globDependent{
			Expression:      slash(root) + "/src/*.cc",
			ListDirectories: true,
			Relative:        slash(root),
		})
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"src/a.cc", "src/b.cc"}
		if !sortedEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("wildcard dir segment", func(t *testing.T) {
		got, err := evalGlobDependent(globDependent{
			Expression:      slash(root) + "/*/[ad].cc",
			ListDirectories: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		want := []string{slash(root) + "/other/d.cc", slash(root) + "/src/a.cc"}
		if !sortedEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})
}

// fakeReply writes a minimal file API reply for dir claiming the given
// inputs (source-relative) and globs.
func fakeReply(t *testing.T, root, dir string, inputs []string, globs []globDependent) {
	t.Helper()
	replyDir := filepath.Join(dir, ".cmake/api/v1/reply")
	if err := os.MkdirAll(replyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var cf cmakeFilesReply
	cf.Paths.Source = filepath.ToSlash(root)
	cf.Paths.Build = filepath.ToSlash(dir)
	for _, in := range inputs {
		cf.Inputs = append(cf.Inputs, struct {
			Path        string `json:"path"`
			IsGenerated bool   `json:"isGenerated"`
			IsExternal  bool   `json:"isExternal"`
			IsCMake     bool   `json:"isCMake"`
		}{Path: in})
	}
	cf.GlobsDependent = globs
	data, err := json.Marshal(&cf)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(replyDir, "cmakeFiles-v1-test.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	index := `{"reply": {"cmakeFiles-v1": {"jsonFile": "cmakeFiles-v1-test.json"}}}`
	if err := os.WriteFile(filepath.Join(replyDir, "index-2026-01-01T00-00-00-0000.json"), []byte(index), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReconfigureReason(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "build")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := &Project{Root: root, Cfg: &Config{}, Lock: &Lock{}}
	tc := &Toolchain{CC: "/usr/bin/cc", CXX: "/usr/bin/c++"}

	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)
	write := func(rel string, mtime time.Time) string {
		path := filepath.Join(root, rel)
		if err := os.WriteFile(path, []byte(rel), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
		return path
	}

	if got := p.reconfigureReason(dir, tc, nil); got != "build dir is not configured" {
		t.Fatalf("unconfigured dir: got %q", got)
	}

	cmakeLists := write("CMakeLists.txt", past)
	write("build/CMakeCache.txt", past)
	fakeReply(t, root, dir, []string{"CMakeLists.txt"}, nil)

	if got := p.reconfigureReason(dir, tc, nil); got != "injected configuration changed" {
		t.Fatalf("before stamp: got %q", got)
	}

	stamp := func() {
		_, stampArgs, err := computeInjection(p, tc, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		writeInjectionStamp(dir, stampArgs, nil, "")
	}
	stamp()

	if got := p.reconfigureReason(dir, tc, nil); got != "" {
		t.Fatalf("fresh config: got %q, want no reason", got)
	}

	// Editing a CMake input file must trigger a reconfigure.
	if err := os.Chtimes(cmakeLists, future, future); err != nil {
		t.Fatal(err)
	}
	if got := p.reconfigureReason(dir, tc, nil); !strings.Contains(got, "CMakeLists.txt changed") {
		t.Fatalf("touched CMakeLists: got %q", got)
	}
	if err := os.Chtimes(cmakeLists, past, past); err != nil {
		t.Fatal(err)
	}

	// A removed input as well.
	if err := os.Rename(cmakeLists, cmakeLists+".bak"); err != nil {
		t.Fatal(err)
	}
	if got := p.reconfigureReason(dir, tc, nil); !strings.Contains(got, "is gone") {
		t.Fatalf("removed CMakeLists: got %q", got)
	}
	if err := os.Rename(cmakeLists+".bak", cmakeLists); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(cmakeLists, past, past); err != nil {
		t.Fatal(err)
	}

	// cmk.yaml is a cmk-side input CMake knows nothing about.
	write(configFileName, future)
	if got := p.reconfigureReason(dir, tc, nil); !strings.Contains(got, configFileName+" changed") {
		t.Fatalf("touched cmk.yaml: got %q", got)
	}
	write(configFileName, past)

	// An [env] change alters the injection identity.
	p.Cfg.Env = map[string]string{"FOO": "bar"}
	if got := p.reconfigureReason(dir, tc, nil); got != "injected configuration changed" {
		t.Fatalf("env change: got %q", got)
	}
	stamp()
	if got := p.reconfigureReason(dir, tc, nil); got != "" {
		t.Fatalf("restamped after env change: got %q", got)
	}

	// A hand-edited cache (ccmake and friends) must reconfigure too.
	write("build/CMakeCache.txt", future)
	if got := p.reconfigureReason(dir, tc, nil); !strings.Contains(got, "CMakeCache.txt") {
		t.Fatalf("touched cache: got %q", got)
	}
	write("build/CMakeCache.txt", past)

	// CONFIGURE_DEPENDS globs: recorded result drifting from the file
	// system means a reconfigure.
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	write("src/a.cc", past)
	glob := globDependent{
		Expression:      filepath.ToSlash(root) + "/src/*.cc",
		ListDirectories: true,
		Paths:           []string{filepath.ToSlash(root) + "/src/a.cc"},
	}
	fakeReply(t, root, dir, []string{"CMakeLists.txt"}, []globDependent{glob})
	if got := p.reconfigureReason(dir, tc, nil); got != "" {
		t.Fatalf("glob in sync: got %q", got)
	}
	write("src/b.cc", past) // new file matches the glob
	if got := p.reconfigureReason(dir, tc, nil); !strings.Contains(got, "file(GLOB)") {
		t.Fatalf("glob drift: got %q", got)
	}
	fakeReply(t, root, dir, []string{"CMakeLists.txt"}, nil)

	// Ad-hoc args recorded by the last configure stay part of the
	// checked identity — and the recorded stamp round-trips them.
	extra := []string{"-DFOO=ON"}
	_, extraStamp, err := computeInjection(p, tc, nil, extra)
	if err != nil {
		t.Fatal(err)
	}
	writeInjectionStamp(dir, extraStamp, extra, "")
	if got := p.reconfigureReason(dir, tc, nil); got != "" {
		t.Fatalf("stamped extra args: got %q, want no reason", got)
	}
	if got := stampExtra(dir); len(got) != 1 || got[0] != "-DFOO=ON" {
		t.Fatalf("stampExtra = %v", got)
	}
}

func TestComputeInjectionSuppressesRegeneration(t *testing.T) {
	root := t.TempDir()
	p := &Project{Root: root, Cfg: &Config{}, Lock: &Lock{}}
	tc := &Toolchain{CC: "cc", CXX: "c++"}
	injected, _, err := computeInjection(p, tc, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range injected {
		if a == "-DCMAKE_SUPPRESS_REGENERATION=ON" {
			found = true
		}
	}
	if !found {
		t.Errorf("injection %v lacks CMAKE_SUPPRESS_REGENERATION", injected)
	}
}

func TestPresetForDir(t *testing.T) {
	root := t.TempDir()
	debug := &PresetCfg{Name: "debug", Build: "build/debug"}
	release := &PresetCfg{Name: "release", Build: "${PROJECT_ROOT}/build/release"}
	p := &Project{Root: root, Lock: &Lock{}, Cfg: &Config{
		Configure: ConfigureCfg{Presets: map[string]*PresetCfg{"debug": debug, "release": release}},
	}}
	if got := presetForDir(p, filepath.Join(root, "build/debug")); got != debug {
		t.Errorf("debug dir: got %v", got)
	}
	if got := presetForDir(p, filepath.Join(root, "build/release")); got != release {
		t.Errorf("release dir (expanded): got %v", got)
	}
	if got := presetForDir(p, filepath.Join(root, "build/other")); got != nil {
		t.Errorf("unknown dir: got %v, want nil", got)
	}
}

func TestWriteConfigFlagsFileKeepsMtimeWhenUnchanged(t *testing.T) {
	root := t.TempDir()
	p := &Project{Root: root, Lock: &Lock{}, Cfg: &Config{
		Configure: ConfigureCfg{
			Generator:      "Ninja Multi-Config",
			Configurations: []*ConfigurationCfg{{Name: "Asan", Compile: []string{"-fsanitize=address"}}},
		},
	}}
	normalizeConfig(p.Cfg)
	if err := writeConfigFlagsFile(p); err != nil {
		t.Fatal(err)
	}
	path, content := configFlagsFile(p)
	if content == "" {
		t.Fatal("expected flags content for a custom configuration")
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if err := writeConfigFlagsFile(p); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !fi.ModTime().Equal(old) {
		t.Errorf("unchanged rewrite bumped mtime: %v -> %v", old, fi.ModTime())
	}
}

func TestReadReplyObjectPicksNewestIndex(t *testing.T) {
	replyDir := t.TempDir()
	writeJSON := func(name, content string) {
		if err := os.WriteFile(filepath.Join(replyDir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeJSON("obj-old.json", `{"paths": {"source": "/old"}}`)
	writeJSON("obj-new.json", `{"paths": {"source": "/new"}}`)
	writeJSON("index-2026-01-01T00-00-00-0000.json", `{"reply": {"cmakeFiles-v1": {"jsonFile": "obj-old.json"}}}`)
	writeJSON("index-2026-01-02T00-00-00-0000.json", `{"reply": {"cmakeFiles-v1": {"jsonFile": "obj-new.json"}}}`)

	var cf cmakeFilesReply
	if err := readReplyObject(replyDir, "cmakeFiles-v1", &cf); err != nil {
		t.Fatal(err)
	}
	if cf.Paths.Source != "/new" {
		t.Errorf("got source %q, want /new (from the newest index)", cf.Paths.Source)
	}
	if err := readReplyObject(replyDir, "codemodel-v2", &cf); err == nil {
		t.Error("missing query should error")
	}
}
