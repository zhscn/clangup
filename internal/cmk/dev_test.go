package cmk

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

// devTestProject builds a Project around a temp root with its own store,
// writing each dep's recipe into the root.
func devTestProject(t *testing.T, deps map[string]*DepCfg, recipes map[string]string) *Project {
	t.Helper()
	root := t.TempDir()
	t.Setenv("CMK_STORE_DIR", filepath.Join(root, "store"))
	for name, body := range recipes {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	p := &Project{
		Root:      root,
		Cfg:       &Config{Deps: deps},
		Lock:      &Lock{Deps: map[string]*LockDep{}},
		Dev:       &DevState{Deps: map[string]*DevDep{}},
		BuildDirs: map[string]string{},
	}
	p.computeDevAffected()
	return p
}

func countLines(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(data), "\n")
}

func mustSync(t *testing.T, p *Project) {
	t.Helper()
	if _, err := syncDeps(p, &Toolchain{ID: "tc-test"}, nil, false); err != nil {
		t.Fatal(err)
	}
}

// TestEarlyCutoffPinned: a rebuild of a need whose install tree comes out
// identical must not cascade into its dependents.
func TestEarlyCutoffPinned(t *testing.T) {
	deps := map[string]*DepCfg{
		"a": {Script: "a.sh"},
		"b": {Script: "b.sh", Needs: []string{"a"}},
	}
	p := devTestProject(t, deps, map[string]string{
		"a.sh": "#!/bin/bash\nset -e\necho payload-v1 > \"$CMK_PREFIX/out.txt\"\necho ran >> \"$CMK_PROJECT_ROOT/a.log\"\n",
		"b.sh": "#!/bin/bash\nset -e\ncat \"$CMK_DEP_A_PREFIX/out.txt\" > \"$CMK_PREFIX/b.txt\"\necho ran >> \"$CMK_PROJECT_ROOT/b.log\"\n",
	})
	aLog := filepath.Join(p.Root, "a.log")
	bLog := filepath.Join(p.Root, "b.log")

	mustSync(t, p)
	if countLines(t, aLog) != 1 || countLines(t, bLog) != 1 {
		t.Fatalf("first sync: a=%d b=%d builds", countLines(t, aLog), countLines(t, bLog))
	}

	// A comment edit rebuilds a (input stamp changed) but produces the
	// same install tree, so b must be cut off.
	if err := os.WriteFile(filepath.Join(p.Root, "a.sh"),
		[]byte("#!/bin/bash\n# tweaked\nset -e\necho payload-v1 > \"$CMK_PREFIX/out.txt\"\necho ran >> \"$CMK_PROJECT_ROOT/a.log\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustSync(t, p)
	if countLines(t, aLog) != 2 {
		t.Fatalf("a not rebuilt after recipe edit (builds=%d)", countLines(t, aLog))
	}
	if countLines(t, bLog) != 1 {
		t.Fatalf("early cutoff failed: b rebuilt despite identical output of a (builds=%d)", countLines(t, bLog))
	}

	// An output change must still cascade.
	if err := os.WriteFile(filepath.Join(p.Root, "a.sh"),
		[]byte("#!/bin/bash\nset -e\necho payload-v2 > \"$CMK_PREFIX/out.txt\"\necho ran >> \"$CMK_PROJECT_ROOT/a.log\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustSync(t, p)
	if countLines(t, aLog) != 3 || countLines(t, bLog) != 2 {
		t.Fatalf("output change did not cascade: a=%d b=%d", countLines(t, aLog), countLines(t, bLog))
	}
}

// TestDevOverrideSync: the dev-override loop — incremental in-place
// rebuilds from a local checkout, quarantined stamps, early cutoff for
// dependents, and a clean fall-back to the pinned world on drop.
func TestDevOverrideSync(t *testing.T) {
	deps := map[string]*DepCfg{
		// a has no pinned source in this test; the override supplies one.
		"a": {Script: "a.sh"},
		"b": {Script: "b.sh", Needs: []string{"a"}},
	}
	// a's recipe reads the override checkout when one is active and a
	// "pinned" directory inside the project otherwise (a has no real
	// pinned source in this test).
	p := devTestProject(t, deps, map[string]string{
		"a.sh": "#!/bin/bash\nset -e\ncp \"${CMK_SRC:-$CMK_PROJECT_ROOT/pinned}/out.txt\" \"$CMK_PREFIX/out.txt\"\necho ran >> \"$CMK_WORK/state\"\necho ran >> \"$CMK_PROJECT_ROOT/a.log\"\n",
		"b.sh": "#!/bin/bash\nset -e\ncat \"$CMK_DEP_A_PREFIX/out.txt\" > \"$CMK_PREFIX/b.txt\"\necho ran >> \"$CMK_PROJECT_ROOT/b.log\"\n",
	})
	if err := os.MkdirAll(filepath.Join(p.Root, "pinned"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.Root, "pinned", "out.txt"), []byte("pinned\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "out.txt"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "other.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p.Dev.Deps["a"] = &DevDep{Path: src}
	p.Dev.dirty = true
	p.computeDevAffected()

	aLog := filepath.Join(p.Root, "a.log")
	bLog := filepath.Join(p.Root, "b.log")
	entry := devEntryDir(p.Root, "a", src)

	mustSync(t, p)
	if countLines(t, aLog) != 1 || countLines(t, bLog) != 1 {
		t.Fatalf("first sync: a=%d b=%d", countLines(t, aLog), countLines(t, bLog))
	}
	// Unchanged checkout: nothing rebuilds.
	mustSync(t, p)
	if countLines(t, aLog) != 1 || countLines(t, bLog) != 1 {
		t.Fatalf("no-op sync rebuilt something: a=%d b=%d", countLines(t, aLog), countLines(t, bLog))
	}

	// Both stamps are quarantined: cmk.lock stays clean, cmk.dev.yaml
	// holds b's stamp.
	platform := hostPlatform(runtime.GOOS, runtime.GOARCH)
	if s := p.Lock.Deps["b"].stampFor(runtime.GOOS, runtime.GOARCH); s != "" {
		t.Fatalf("dev-affected stamp leaked into cmk.lock: %q", s)
	}
	if p.Dev.stampFor(platform, "b") == "" {
		t.Fatal("dependent's stamp missing from the dev quarantine")
	}
	if !fileExists(filepath.Join(p.Root, devFileName)) {
		t.Fatalf("%s was not written", devFileName)
	}

	// Edit that changes a's output: a rebuilds IN PLACE (work survives),
	// b rebuilds against the new output.
	if err := os.WriteFile(filepath.Join(src, "out.txt"), []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustSync(t, p)
	if countLines(t, aLog) != 2 || countLines(t, bLog) != 2 {
		t.Fatalf("source edit: a=%d b=%d", countLines(t, aLog), countLines(t, bLog))
	}
	if got := countLines(t, filepath.Join(entry, "work", "state")); got != 2 {
		t.Fatalf("work tree was not preserved across rebuilds (state lines=%d, want 2)", got)
	}

	// Edit that does NOT change a's output: a rebuilds, b is cut off.
	if err := os.WriteFile(filepath.Join(src, "other.txt"), []byte("y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustSync(t, p)
	if countLines(t, aLog) != 3 {
		t.Fatalf("a not rebuilt after unrelated source edit (a=%d)", countLines(t, aLog))
	}
	if countLines(t, bLog) != 2 {
		t.Fatalf("early cutoff failed for dev dep: b=%d", countLines(t, bLog))
	}

	// Drop the override: b's next sync lands in a pinned entry with its
	// stamp back in cmk.lock, and the quarantine empties out.
	delete(p.Dev.Deps, "a")
	p.Dev.dirty = true
	p.computeDevAffected()
	mustSync(t, p)
	if p.Lock.Deps["b"].stampFor(runtime.GOOS, runtime.GOARCH) == "" {
		t.Fatal("pinned stamp not restored to cmk.lock after drop")
	}
	if p.Dev.stampFor(platform, "b") != "" {
		t.Fatal("quarantined stamp survived the drop")
	}
	if fileExists(filepath.Join(p.Root, devFileName)) {
		t.Fatalf("%s should be deleted once empty", devFileName)
	}
}

func TestDevTreeHashGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git on PATH")
	}
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir,
			"-c", "user.email=t@t", "-c", "user.name=t", "-c", "commit.gpgsign=false"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("ignored.txt\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("one\n"), 0o644)
	git("add", ".")
	git("commit", "-q", "-m", "init")

	hash := func() string {
		t.Helper()
		h, err := devTreeHash(dir)
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	clean := hash()
	if clean != hash() {
		t.Fatal("hash not deterministic")
	}
	// gitignored files do not change the identity
	os.WriteFile(filepath.Join(dir, "ignored.txt"), []byte("noise\n"), 0o644)
	if hash() != clean {
		t.Fatal("gitignored file changed the tree hash")
	}
	// a dirty tracked file does
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("two\n"), 0o644)
	dirty := hash()
	if dirty == clean {
		t.Fatal("dirty edit did not change the tree hash")
	}
	// so does an untracked file
	os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new\n"), 0o644)
	if h := hash(); h == dirty || h == clean {
		t.Fatal("untracked file did not change the tree hash")
	}
	// a commit changes HEAD and keeps the identity changing predictably
	git("add", ".")
	git("commit", "-q", "-m", "edit")
	committed := hash()
	if committed == clean || committed == dirty {
		t.Fatal("commit did not change the tree hash")
	}
	if committed != hash() {
		t.Fatal("hash not deterministic after commit")
	}
}

func TestDevTreeHashPlainDir(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("one\n"), 0o644)
	h1, err := devTreeHash(dir)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("two\n"), 0o644)
	h2, err := devTreeHash(dir)
	if err != nil {
		t.Fatal(err)
	}
	if h1 == h2 {
		t.Fatal("plain-dir edit did not change the tree hash")
	}
}

func TestDevStateRoundTrip(t *testing.T) {
	root := t.TempDir()
	cfg := &Config{Deps: map[string]*DepCfg{"fmt": {Script: "fmt.sh"}, "spdlog": {Script: "s.sh"}}}
	ds := &DevState{
		Deps:   map[string]*DevDep{"fmt": {Path: "/home/me/fmt"}},
		Stamps: map[string]map[string]string{"linux-x86_64": {"spdlog": "abc123"}},
		dirty:  true,
	}
	if err := saveDevFile(root, ds); err != nil {
		t.Fatal(err)
	}
	got, err := loadDevState(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got.Deps["fmt"] == nil || got.Deps["fmt"].Path != "/home/me/fmt" || got.stampFor("linux-x86_64", "spdlog") != "abc123" {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	// an override for a dep no longer in cmk.yaml is dropped on load
	delete(cfg.Deps, "fmt")
	got, err = loadDevState(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Deps) != 0 {
		t.Fatalf("stale override survived: %+v", got.Deps)
	}
	// an empty state deletes the file
	empty := &DevState{dirty: true}
	if err := saveDevFile(root, empty); err != nil {
		t.Fatal(err)
	}
	if fileExists(filepath.Join(root, devFileName)) {
		t.Fatal("empty dev state should remove the file")
	}
}

func makeTarballBytes(t *testing.T, topDir, fileName, content string) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	data := []byte(content)
	if err := tw.WriteHeader(&tar.Header{
		Name: topDir + "/" + fileName,
		Mode: 0o644,
		Size: int64(len(data)),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), hex.EncodeToString(sum[:])
}

func TestArchiveDependencyPinsOptionalSHA256InLock(t *testing.T) {
	tarV1, shaV1 := makeTarballBytes(t, "pkg-1.0", "ver.txt", "1.0\n")
	tarV2, shaV2 := makeTarballBytes(t, "pkg-2.0", "ver.txt", "2.0\n")

	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/pkg-1.0.tar":
			_, _ = w.Write(tarV1)
		case "/pkg-2.0.tar":
			_, _ = w.Write(tarV2)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	t.Setenv("XDG_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))
	deps := map[string]*DepCfg{
		"pkg": {
			Script: "pkg.sh",
			Source: &SourceCfg{URL: server.URL + "/pkg-1.0.tar"}, // sha256 omitted
		},
	}
	p := devTestProject(t, deps, map[string]string{
		"pkg.sh": "#!/bin/bash\nset -e\ncp \"$CMK_SRC/ver.txt\" \"$CMK_PREFIX/ver.txt\"\n",
	})

	// cmk.yaml without sha256 validates cleanly.
	if err := validateDeps(p.Cfg); err != nil {
		t.Fatalf("validateDeps rejected omitted sha256: %v", err)
	}
	// Before sync, --locked rejects the unlocked URL dependency.
	if err := verifyLockedDeps(p.Cfg, p.Lock); err == nil {
		t.Fatal("verifyLockedDeps accepted unlocked URL dependency")
	}

	dirty, err := syncDeps(p, &Toolchain{ID: "tc-test"}, nil, false)
	if err != nil || !dirty {
		t.Fatalf("first syncDeps = (%v, %v), want dirty=true", dirty, err)
	}
	if gotHits := hits.Load(); gotHits != 1 {
		t.Fatalf("first sync downloaded %d times, want 1", gotHits)
	}
	ld := p.Lock.Deps["pkg"]
	if ld == nil || ld.URL != server.URL+"/pkg-1.0.tar" || ld.SHA256 != shaV1 {
		t.Fatalf("locked dep = %+v, want url=%s sha256=%s", ld, server.URL+"/pkg-1.0.tar", shaV1)
	}
	if err := verifyLockedDeps(p.Cfg, p.Lock); err != nil {
		t.Fatalf("verifyLockedDeps after sync: %v", err)
	}

	// Second sync hits the locked sha256 and store without touching HTTP.
	if _, err := syncDeps(p, &Toolchain{ID: "tc-test"}, nil, false); err != nil {
		t.Fatal(err)
	}
	if gotHits := hits.Load(); gotHits != 1 {
		t.Fatalf("second sync downloaded %d times, want 1", gotHits)
	}

	// Bumping the URL in cmk.yaml invalidates the lock until synced.
	p.Cfg.Deps["pkg"].Source.URL = server.URL + "/pkg-2.0.tar"
	if err := verifyLockedDeps(p.Cfg, p.Lock); err == nil {
		t.Fatal("verifyLockedDeps accepted stale URL lock after URL bump")
	}
	dirty, err = syncDeps(p, &Toolchain{ID: "tc-test"}, nil, false)
	if err != nil || !dirty {
		t.Fatalf("v2 syncDeps = (%v, %v), want dirty=true", dirty, err)
	}
	if p.Lock.Deps["pkg"].SHA256 != shaV2 {
		t.Fatalf("v2 locked sha256 = %q, want %q", p.Lock.Deps["pkg"].SHA256, shaV2)
	}
	pfx, err := p.depPrefix("pkg")
	if err != nil {
		t.Fatal(err)
	}
	ver, err := os.ReadFile(filepath.Join(pfx, "ver.txt"))
	if err != nil || string(ver) != "2.0\n" {
		t.Fatalf("installed ver.txt = %q, %v", ver, err)
	}
}

func TestRecipeStubForwardsToolchainFileWhenSet(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	work := filepath.Join(dir, "work")
	for _, d := range []string{bin, work} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	log := filepath.Join(dir, "cmake.log")
	fakeCMake := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + shellQuote([]string{log}) + "\n"
	if err := os.WriteFile(filepath.Join(bin, "cmake"), []byte(fakeCMake), 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "recipe.sh")
	if err := os.WriteFile(script, []byte(recipeStub), 0o755); err != nil {
		t.Fatal(err)
	}

	runStub := func(tcFile string) string {
		_ = os.Remove(log)
		cmd := exec.Command("bash", script)
		cmd.Dir = work
		cmd.Env = []string{
			"PATH=" + bin + ":" + os.Getenv("PATH"),
			"CMK_SRC=/src",
			"CMK_PREFIX=/pfx",
			"CMK_JOBS=4",
		}
		if tcFile != "" {
			cmd.Env = append(cmd.Env, "CMK_TOOLCHAIN_FILE="+tcFile)
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("recipeStub failed: %v\n%s", err, out)
		}
		got, err := os.ReadFile(log)
		if err != nil {
			t.Fatal(err)
		}
		return string(got)
	}

	withTC := runStub("/opt/clangup/toolchain.cmake")
	if !strings.Contains(withTC, "-DCMAKE_TOOLCHAIN_FILE=/opt/clangup/toolchain.cmake") {
		t.Fatalf("recipeStub did not forward CMK_TOOLCHAIN_FILE:\n%s", withTC)
	}
	withoutTC := runStub("")
	if strings.Contains(withoutTC, "CMAKE_TOOLCHAIN_FILE") {
		t.Fatalf("recipeStub passed empty CMAKE_TOOLCHAIN_FILE:\n%s", withoutTC)
	}
}
