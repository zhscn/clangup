package cmk

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"
)

// storeDir is the shared, content-addressed dep store. Entries are
// keyed by name+stamp, so checkouts and git worktrees with identical
// pins share one build, while divergent branches get disjoint paths and
// can never invalidate each other. It lives under XDG data — not cache
// — because cache cleaners must not eat build trees that running build
// dirs still reference.
func storeDir() string {
	if d := os.Getenv("CMK_STORE_DIR"); d != "" {
		return d
	}
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "cmk", "store")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".cmk-store"
	}
	return filepath.Join(home, ".local", "share", "cmk", "store")
}

// entryDir is one immutable store entry: prefix/ (the install tree),
// work/ (the build tree, for build-tree consumers like FDB), src/ (the
// materialized source) and .cmk-complete (written after the recipe
// succeeds; an entry without it is garbage and gets rebuilt).
func entryDir(name, stamp string) string {
	if len(stamp) > 16 {
		stamp = stamp[:16]
	}
	return filepath.Join(storeDir(), name+"-"+stamp)
}

const completeMarker = ".cmk-complete"

// depEntry resolves a dep to its store entry via the stamp pinned in
// cmk.lock, so build/run/env never have to recompute stamps.
func (p *Project) depEntry(name string) (string, error) {
	ld := p.Lock.Deps[name]
	stamp := ld.stampFor(runtime.GOOS, runtime.GOARCH)
	if stamp == "" {
		return "", fmt.Errorf("dep %s is not synced (run `cmk sync`)", name)
	}
	return entryDir(name, stamp), nil
}

func (p *Project) depPrefix(name string) (string, error) {
	entry, err := p.depEntry(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(entry, "prefix"), nil
}

// downloadsDir caches tarballs by sha256. Unlike the store this may
// live in XDG cache: entries are re-downloadable and hash-verified.
func downloadsDir() string {
	if d := os.Getenv("XDG_CACHE_HOME"); d != "" {
		return filepath.Join(d, "cmk", "downloads")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".cmk-downloads"
	}
	return filepath.Join(home, ".cache", "cmk", "downloads")
}

// lockStoreEntry serializes concurrent builds of the same entry (two
// worktrees syncing at once build it exactly once). The lock file lives
// outside the entry so wiping a half-built entry can't drop the lock.
func lockStoreEntry(name, stamp string) (*os.File, error) {
	dir := filepath.Join(storeDir(), ".locks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if len(stamp) > 16 {
		stamp = stamp[:16]
	}
	f, err := os.OpenFile(filepath.Join(dir, name+"-"+stamp+".lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, fmt.Errorf("locking store entry %s: %w", name, err)
	}
	return f, nil
}

func unlockStoreEntry(f *os.File) {
	syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	f.Close()
}

// tryLockStoreEntry is the non-blocking form of lockStoreEntry: ok is
// false (with no error) when another process holds the lock, so callers
// like `cmk clean --prune` can skip entries a concurrent sync is building.
func tryLockStoreEntry(name, stamp string) (f *os.File, ok bool, err error) {
	dir := filepath.Join(storeDir(), ".locks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, false, err
	}
	if len(stamp) > 16 {
		stamp = stamp[:16]
	}
	f, err = os.OpenFile(filepath.Join(dir, name+"-"+stamp+".lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, false, nil
	}
	return f, true, nil
}

// topoOrder returns want (or all deps when want is empty) plus their
// transitive needs, dependencies first.
func topoOrder(deps map[string]*DepCfg, want []string) ([]string, error) {
	if len(want) == 0 {
		for name := range deps {
			want = append(want, name)
		}
	}
	sort.Strings(want)

	const (
		visiting = 1
		done     = 2
	)
	state := map[string]int{}
	var order []string
	var visit func(name string, chain []string) error
	visit = func(name string, chain []string) error {
		switch state[name] {
		case done:
			return nil
		case visiting:
			return fmt.Errorf("dependency cycle: %s -> %s", strings.Join(chain, " -> "), name)
		}
		d, ok := deps[name]
		if !ok {
			return fmt.Errorf("unknown dep %q", name)
		}
		state[name] = visiting
		needs := append([]string(nil), d.Needs...)
		sort.Strings(needs)
		for _, n := range needs {
			if err := visit(n, append(chain, name)); err != nil {
				return err
			}
		}
		state[name] = done
		order = append(order, name)
		return nil
	}
	for _, name := range want {
		if err := visit(name, nil); err != nil {
			return nil, err
		}
	}
	return order, nil
}

// sourceID is the part of the stamp describing where the source came
// from: tarball sha256, locked git commit, or "none".
func sourceID(d *DepCfg, ld *LockDep) string {
	switch {
	case d.Source == nil:
		return "none"
	case d.Source.URL != "":
		return "url:" + d.Source.SHA256
	default:
		commit := ""
		if ld != nil {
			commit = ld.Commit
		}
		return "git:" + d.Source.Git + "@" + commit
	}
}

// depInputs resolves the patch and extra_inputs globs of a dep into
// sorted root-relative paths. A pattern matching nothing is an error
// (almost always a typo, and silently dropping it would corrupt stamps).
func depInputs(root, name string, d *DepCfg) (patches, extras []string, err error) {
	patches, err = resolveInputGlobs(root, d.Patches)
	if err != nil {
		return nil, nil, fmt.Errorf("dependencies.%s.patches: %w", name, err)
	}
	extras, err = resolveInputGlobs(root, d.ExtraInputs)
	if err != nil {
		return nil, nil, fmt.Errorf("dependencies.%s.extra-inputs: %w", name, err)
	}
	return patches, extras, nil
}

func resolveInputGlobs(root string, patterns []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, pat := range patterns {
		matches, err := filepath.Glob(filepath.Join(root, pat))
		if err != nil {
			return nil, fmt.Errorf("%q: %w", pat, err)
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("no files match %q", pat)
		}
		sort.Strings(matches)
		for _, m := range matches {
			rel, err := filepath.Rel(root, m)
			if err != nil {
				return nil, err
			}
			if !seen[rel] {
				seen[rel] = true
				out = append(out, rel)
			}
		}
	}
	return out, nil
}

// hashFiles hashes the names and contents of root-relative files.
func hashFiles(root string, rels []string) (string, error) {
	h := sha256.New()
	for _, rel := range rels {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			return "", err
		}
		h.Write([]byte(rel))
		h.Write([]byte{0})
		h.Write(data)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// depStamp hashes everything that should trigger a rebuild: the recipe
// script, the source identity, the toolchain, the declared env knobs,
// patch/extra-input contents, and the stamps of all needs (so upstream
// rebuilds cascade).
func depStamp(root, name string, d *DepCfg, tcID string, ld *LockDep, needStamps map[string]string, patches, extras []string) (string, error) {
	script, err := os.ReadFile(filepath.Join(root, d.Script))
	if err != nil {
		return "", fmt.Errorf("dependencies.%s: %w", name, err)
	}
	h := sha256.New()
	w := func(parts ...string) {
		for _, p := range parts {
			h.Write([]byte(p))
			h.Write([]byte{0})
		}
	}
	w("cmk-stamp-v3", name, tcID, sourceID(d, ld))
	h.Write(script)
	h.Write([]byte{0})
	envKeys := make([]string, 0, len(d.Env))
	for k := range d.Env {
		envKeys = append(envKeys, k)
	}
	sort.Strings(envKeys)
	for _, k := range envKeys {
		w("env", k, d.Env[k]) // raw (pre-expansion) values: stable across checkouts
	}
	for _, group := range []struct {
		tag   string
		files []string
	}{{"patch", patches}, {"input", extras}} {
		for _, rel := range group.files {
			data, err := os.ReadFile(filepath.Join(root, rel))
			if err != nil {
				return "", fmt.Errorf("dependencies.%s: %w", name, err)
			}
			w(group.tag, rel)
			h.Write(data)
			h.Write([]byte{0})
		}
	}
	needs := append([]string(nil), d.Needs...)
	sort.Strings(needs)
	for _, n := range needs {
		w(n, needStamps[n])
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

var fullShaRe = regexp.MustCompile(`^[0-9a-f]{40}$`)

// resolveGitCommit pins a git ref to a commit via ls-remote, preferring
// peeled tags. A 40-hex ref is already a commit.
func resolveGitCommit(url, ref string) (string, error) {
	if fullShaRe.MatchString(ref) {
		return ref, nil
	}
	out, err := exec.Command("git", "ls-remote", url,
		"refs/tags/"+ref+"^{}", "refs/tags/"+ref, "refs/heads/"+ref, ref).Output()
	if err != nil {
		return "", fmt.Errorf("git ls-remote %s %s: %w", url, ref, err)
	}
	found := map[string]string{}
	for line := range strings.Lines(string(out)) {
		sha, name, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if ok {
			found[name] = sha
		}
	}
	for _, cand := range []string{"refs/tags/" + ref + "^{}", "refs/tags/" + ref, "refs/heads/" + ref, ref} {
		if sha, ok := found[cand]; ok {
			return sha, nil
		}
	}
	return "", fmt.Errorf("ref %q not found in %s", ref, url)
}

// ensureLockEntries pins every floating git dep and drops entries for
// dependencies no longer in cmk.yaml, returning whether the lock changed.
// Stamps are filled in later, during the sync itself.
func ensureLockEntries(cfg *Config, lk *Lock, names []string) (bool, error) {
	dirty := false
	for name := range lk.Deps {
		if _, ok := cfg.Deps[name]; !ok {
			delete(lk.Deps, name)
			dirty = true
		}
	}
	for _, name := range names {
		d := cfg.Deps[name]
		if d.Source == nil || d.Source.Git == "" {
			continue
		}
		ld := lk.Deps[name]
		if ld != nil && ld.Git == d.Source.Git && ld.Ref == d.Source.Ref && fullShaRe.MatchString(ld.Commit) {
			continue
		}
		fmt.Fprintf(os.Stderr, "cmk: resolving %s %s@%s\n", name, d.Source.Git, d.Source.Ref)
		commit, err := resolveGitCommit(d.Source.Git, d.Source.Ref)
		if err != nil {
			return dirty, fmt.Errorf("dependencies.%s: %w", name, err)
		}
		if ld == nil {
			ld = &LockDep{}
			lk.Deps[name] = ld
		}
		ld.Git, ld.Ref, ld.Commit = d.Source.Git, d.Source.Ref, commit
		dirty = true
	}
	return dirty, nil
}

// fetchTarball downloads url into the downloads dir (named by its
// sha256), hashing on the wire. An existing verified file is reused.
func fetchTarball(url, sha string) (string, error) {
	dir := downloadsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dest := filepath.Join(dir, sha)
	if _, err := os.Stat(dest); err == nil {
		return dest, nil
	}
	fmt.Fprintf(os.Stderr, "cmk: downloading %s\n", url)
	client := &http.Client{Timeout: 30 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	tmp, err := os.CreateTemp(dir, ".partial-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), resp.Body); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != sha {
		return "", fmt.Errorf("sha256 mismatch for %s\n  expected %s\n  got      %s", url, sha, got)
	}
	return dest, os.Rename(tmp.Name(), dest)
}

// prepareSrc materializes the dep source under <entry>/src, applies the
// patches, and returns its path. A .cmk-src marker records what's
// checked out (including patch identity) so an up-to-date tree is
// reused and a changed patch re-materializes the tree.
func prepareSrc(entry, root string, d *DepCfg, ld *LockDep, patches []string) (string, error) {
	if d.Source == nil {
		return "", nil
	}
	srcDir := filepath.Join(entry, "src")
	marker := filepath.Join(srcDir, ".cmk-src")
	id := sourceID(d, ld)
	if len(patches) > 0 {
		ph, err := hashFiles(root, patches)
		if err != nil {
			return "", err
		}
		id += "+patches:" + ph
	}
	if data, err := os.ReadFile(marker); err == nil && strings.TrimSpace(string(data)) == id {
		return srcDir, nil
	}
	if err := os.RemoveAll(srcDir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		return "", err
	}

	if d.Source.URL != "" {
		tarball, err := fetchTarball(d.Source.URL, d.Source.SHA256)
		if err != nil {
			return "", err
		}
		// system tar auto-detects compression by content
		cmd := exec.Command("tar", "--strip-components=1", "-C", srcDir, "-xf", tarball)
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("extracting %s: %w", tarball, err)
		}
	} else {
		if err := gitCheckout(srcDir, d.Source.Git, d.Source.Ref, ld.Commit); err != nil {
			return "", err
		}
	}
	for _, rel := range patches {
		fmt.Fprintf(os.Stderr, "cmk: applying %s\n", rel)
		cmd := exec.Command("patch", "-p1", "-i", filepath.Join(root, rel))
		cmd.Dir = srcDir
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("patch %s failed:\n%s", rel, strings.TrimSpace(string(out)))
		}
	}
	if err := os.WriteFile(marker, []byte(id+"\n"), 0o644); err != nil {
		return "", err
	}
	return srcDir, nil
}

// recipeBaseEnv is the sanitized environment recipes run in: the
// inherited PATH plus a small whitelist. Shell-session vars like CFLAGS
// or PKG_CONFIG_PATH must not leak in — the stamp can't see them. Build
// knobs belong in dependencies.<name>.env, which is hashed.
var recipeEnvKeep = []string{
	"HOME", "USER", "LOGNAME", "SHELL", "TERM", "TMPDIR", "LANG", "LC_ALL",
	"http_proxy", "https_proxy", "no_proxy", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY",
	"SSL_CERT_FILE", "SSL_CERT_DIR", "CURL_CA_BUNDLE", "GIT_SSL_CAINFO",
	"SSH_AUTH_SOCK", "CCACHE_DIR",
}

func recipeBaseEnv() []string {
	env := []string{"PATH=" + os.Getenv("PATH")}
	for _, k := range recipeEnvKeep {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}

// gitCheckout produces a shallow checkout of exactly commit. It first
// tries fetching the commit directly (works on GitHub), falling back to
// a shallow branch/tag clone verified against the pinned commit.
func gitCheckout(dir, url, ref, commit string) error {
	fmt.Fprintf(os.Stderr, "cmk: cloning %s@%s (%s)\n", url, ref, commit[:12])
	run := func(args ...string) error {
		cmd := exec.Command("git", args...)
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
	if run("init", "-q", dir) == nil &&
		run("-C", dir, "fetch", "-q", "--depth", "1", url, commit) == nil &&
		run("-C", dir, "checkout", "-q", "--detach", "FETCH_HEAD") == nil {
		return nil
	}
	// fallback: shallow clone of the ref, then verify
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := run("clone", "-q", "--depth", "1", "--branch", ref, url, dir); err != nil {
		return fmt.Errorf("git clone %s@%s failed", url, ref)
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		return err
	}
	if head := strings.TrimSpace(string(out)); head != commit {
		return fmt.Errorf("%s@%s now resolves to %s but cmk.lock pins %s; run `cmk update <dep>` to accept",
			url, ref, head[:12], commit[:12])
	}
	return nil
}

// buildDep brings one dep's store entry into existence, returning
// whether work was done and whether the lock changed. needStamps must
// already contain entries for all needs. Entries are immutable once
// complete: a stamp change lands in a NEW entry, so build dirs that
// reference the old one stay valid.
func buildDep(p *Project, name string, tc *Toolchain, needStamps map[string]string, force bool) (built, lockDirty bool, err error) {
	d := p.Cfg.Deps[name]
	lk := p.Lock
	patches, extras, err := depInputs(p.Root, name, d)
	if err != nil {
		return false, false, err
	}
	stamp, err := depStamp(p.Root, name, d, tc.ID, lk.Deps[name], needStamps, patches, extras)
	if err != nil {
		return false, false, err
	}
	needStamps[name] = stamp
	ld := lk.Deps[name]
	if ld == nil {
		ld = &LockDep{}
		lk.Deps[name] = ld
	}
	if ld.stampFor(runtime.GOOS, runtime.GOARCH) != stamp {
		ld.setStampFor(runtime.GOOS, runtime.GOARCH, stamp)
		lockDirty = true
	}

	entry := entryDir(name, stamp)
	marker := filepath.Join(entry, completeMarker)
	if !force {
		if _, err := os.Stat(marker); err == nil {
			return false, lockDirty, nil
		}
	}

	flk, err := lockStoreEntry(name, stamp)
	if err != nil {
		return false, lockDirty, err
	}
	defer unlockStoreEntry(flk)
	if !force {
		// a concurrent cmk (another worktree) may have built it while
		// we waited for the lock
		if _, err := os.Stat(marker); err == nil {
			fmt.Fprintf(os.Stderr, "cmk: dep %s was built by a concurrent cmk\n", name)
			return false, lockDirty, nil
		}
	}

	fmt.Fprintf(os.Stderr, "cmk: building dep %s\n", name)
	if err := os.RemoveAll(entry); err != nil {
		return false, lockDirty, err
	}
	prefix := filepath.Join(entry, "prefix")
	work := filepath.Join(entry, "work")
	for _, dir := range []string{prefix, work} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return false, lockDirty, err
		}
	}
	src, err := prepareSrc(entry, p.Root, d, lk.Deps[name], patches)
	if err != nil {
		return false, lockDirty, err
	}

	script := filepath.Join(p.Root, d.Script)
	cmd := exec.Command("bash", script)
	cmd.Dir = work
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	env := append(recipeBaseEnv(), tc.scriptEnv()...)
	env = append(env,
		"CMK_PREFIX="+prefix,
		"CMK_WORK="+work,
		"CMK_JOBS="+fmt.Sprint(defaultJobs()),
		"CMK_PROJECT_ROOT="+p.Root,
	)
	if src != "" {
		env = append(env, "CMK_SRC="+src)
	}
	vars := p.vars()
	envKeys := make([]string, 0, len(d.Env))
	for k := range d.Env {
		envKeys = append(envKeys, k)
	}
	sort.Strings(envKeys)
	for _, k := range envKeys {
		env = append(env, k+"="+expandVars(d.Env[k], vars))
	}
	for _, n := range d.Needs {
		pfx, err := p.depPrefix(n)
		if err != nil {
			return false, lockDirty, fmt.Errorf("dep %s: %w", name, err)
		}
		env = append(env, "CMK_DEP_"+envName(n)+"_PREFIX="+pfx)
	}
	env = append(env, needsSearchEnv(p, name)...)
	cmd.Env = env

	start := time.Now()
	if err := cmd.Run(); err != nil {
		return false, lockDirty, fmt.Errorf("dep %s: %s failed: %w", name, d.Script, err)
	}
	if err := os.WriteFile(marker, []byte(stamp+"\n"), 0o644); err != nil {
		return false, lockDirty, err
	}
	fmt.Fprintf(os.Stderr, "cmk: dep %s done in %s\n", name, time.Since(start).Round(time.Second))
	return true, lockDirty, nil
}

// needsClosure returns the transitive needs of name (excluding name
// itself), dependencies-first.
func needsClosure(deps map[string]*DepCfg, name string) []string {
	d := deps[name]
	if d == nil || len(d.Needs) == 0 {
		return nil
	}
	order, err := topoOrder(deps, d.Needs)
	if err != nil {
		return d.Needs // cycle errors surface in topoOrder of the full sync
	}
	return order
}

// needsSearchEnv makes the transitive needs of a dep visible to its
// recipe's own build system: CMAKE_PREFIX_PATH so a nested CMake build's
// find_package() resolves the shared, cmk-built versions (the diamond
// case: project and dep pinning one common fmt), and PKG_CONFIG_PATH for
// autoconf-style builds. Direct needs take search precedence.
func needsSearchEnv(p *Project, name string) []string {
	closure := needsClosure(p.Cfg.Deps, name)
	if len(closure) == 0 {
		return nil
	}
	var prefixes, pkgDirs []string
	for i := len(closure) - 1; i >= 0; i-- { // dependents-first
		pfx, err := p.depPrefix(closure[i])
		if err != nil {
			continue // needs are built before their dependents; shouldn't happen
		}
		prefixes = append(prefixes, pfx)
		pkgDirs = append(pkgDirs, pkgconfigDirs(pfx)...)
	}
	// no merge with the caller's values: recipes run hermetically
	env := []string{"CMAKE_PREFIX_PATH=" + strings.Join(prefixes, ":")}
	if len(pkgDirs) > 0 {
		env = append(env, "PKG_CONFIG_PATH="+strings.Join(pkgDirs, ":"))
	}
	return env
}

// pkgconfigDirs returns the existing pkg-config dirs under a prefix.
func pkgconfigDirs(prefix string) []string {
	var out []string
	for _, sub := range []string{"lib/pkgconfig", "lib64/pkgconfig", "share/pkgconfig"} {
		if st, err := os.Stat(filepath.Join(prefix, sub)); err == nil && st.IsDir() {
			out = append(out, filepath.Join(prefix, sub))
		}
	}
	return out
}

// syncDeps brings the requested deps (default: all) up to date in the
// store, pinning their stamps in cmk.lock.
func syncDeps(p *Project, tc *Toolchain, want []string, force bool) (lockDirty bool, err error) {
	order, err := topoOrder(p.Cfg.Deps, want)
	if err != nil {
		return false, err
	}
	lockDirty, err = ensureLockEntries(p.Cfg, p.Lock, order)
	if err != nil {
		return lockDirty, err
	}
	needStamps := map[string]string{}
	built := 0
	for _, name := range order {
		did, dirty, err := buildDep(p, name, tc, needStamps, force && contains(want, name))
		lockDirty = lockDirty || dirty
		if err != nil {
			return lockDirty, err
		}
		if did {
			built++
		}
	}
	if built == 0 && len(order) > 0 {
		fmt.Fprintf(os.Stderr, "cmk: deps up to date (%d)\n", len(order))
	}
	return lockDirty, nil
}

func contains(s []string, v string) bool {
	if len(s) == 0 {
		return true // empty want means "all requested"
	}
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// depExports returns the cmake args contributed by a built dep: the
// lines of $CMK_PREFIX/.cmk-exports if the script wrote one, otherwise
// -D<Name>_ROOT=<prefix>.
func depExports(p *Project, name string, d *DepCfg) ([]string, error) {
	prefix, err := p.depPrefix(name)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(prefix, ".cmk-exports"))
	if err == nil {
		var out []string
		for line := range strings.Lines(string(data)) {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "#") {
				out = append(out, line)
			}
		}
		return out, nil
	}
	cmakeName := d.CMakeName
	if cmakeName == "" {
		cmakeName = name
	}
	return []string{"-D" + cmakeName + "_ROOT=" + prefix}, nil
}

func sortedDepNames(deps map[string]*DepCfg) []string {
	names := make([]string, 0, len(deps))
	for name := range deps {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func allDepExports(p *Project) ([]string, error) {
	var out []string
	for _, name := range sortedDepNames(p.Cfg.Deps) {
		exp, err := depExports(p, name, p.Cfg.Deps[name])
		if err != nil {
			return nil, err
		}
		out = append(out, exp...)
	}
	return out, nil
}
