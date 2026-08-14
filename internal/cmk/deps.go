package cmk

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
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
// cmk.lock (or quarantined in cmk.dev.yaml for dev-affected deps), so
// build/run/env never have to recompute stamps. A dev-overridden dep
// resolves to its mutable dev entry, whose path is stable across edits.
func (p *Project) depEntry(name string) (string, error) {
	if path, ok := p.devPath(name); ok {
		entry := devEntryDir(p.Root, name, path)
		if loadDevMarker(entry) == nil {
			return "", fmt.Errorf("dep %s (dev override) is not built (run `cmk sync`)", name)
		}
		return entry, nil
	}
	stamp := p.depStampFor(name)
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
// patch/extra-input contents, and the OUTPUT hash of every need. Keying
// on outputs rather than the needs' own stamps is the early cutoff: an
// upstream rebuild whose install tree comes out byte-identical (a recipe
// comment, a rebased fork with the same result) stops cascading right
// there.
func depStamp(root, name string, d *DepCfg, tcID string, ld *LockDep, needOut map[string]string, patches, extras []string) (string, error) {
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
	for _, k := range slices.Sorted(maps.Keys(d.Env)) {
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
		w("need-out", n, needOut[n])
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
// Stamps are filled in later, during the sync itself. A dev-overridden
// dep builds from its local checkout, so its pin is left exactly as it
// was — dropping the override falls back to it.
func ensureLockEntries(cfg *Config, lk *Lock, names []string, overridden map[string]*DevDep) (bool, error) {
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
		if _, ok := overridden[name]; ok {
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

// fetchTarball downloads url into the downloads dir, verifying it against
// the expected sha256. An already-downloaded file is reused.
func fetchTarball(url, sha string) (string, error) {
	path, _, err := download(url, sha)
	return path, err
}

// download fetches url into the downloads dir, where files are named by
// their sha256, hashing on the wire. A non-empty want is verified and a
// mismatch is an error; an empty want means "whatever it hashes to"
// (`cmk add` computing the sha256 of a new source). An already-present
// file with the wanted digest is reused without a request.
func download(url, want string) (path, sha string, err error) {
	dir := downloadsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	if want != "" {
		if dest := filepath.Join(dir, want); fileExists(dest) {
			return dest, want, nil
		}
	}
	fmt.Fprintf(os.Stderr, "cmk: downloading %s\n", url)
	client := &http.Client{Timeout: 30 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	tmp, err := os.CreateTemp(dir, ".partial-*")
	if err != nil {
		return "", "", err
	}
	defer os.Remove(tmp.Name())
	digest := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, digest), resp.Body); err != nil {
		tmp.Close()
		return "", "", err
	}
	if err := tmp.Close(); err != nil {
		return "", "", err
	}
	sha = hex.EncodeToString(digest.Sum(nil))
	if want != "" && sha != want {
		return "", "", fmt.Errorf("sha256 mismatch for %s\n  expected %s\n  got      %s", url, want, sha)
	}
	dest := filepath.Join(dir, sha)
	if fileExists(dest) {
		return dest, sha, nil // raced with another cmk, or already had it
	}
	return dest, sha, os.Rename(tmp.Name(), dest)
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

// outHashFile records a store entry's output identity — the hash of its
// install tree — computed once after the recipe succeeds. Dependents key
// their stamps on it (see depStamp).
const outHashFile = ".cmk-out"

// entryOutHash returns a built entry's output hash, computing and
// caching it for entries that predate the file.
func entryOutHash(entry string) (string, error) {
	path := filepath.Join(entry, outHashFile)
	if data, err := os.ReadFile(path); err == nil {
		if out := strings.TrimSpace(string(data)); out != "" {
			return out, nil
		}
	}
	return writeEntryOutHash(entry)
}

func writeEntryOutHash(entry string) (string, error) {
	out, err := hashTree(filepath.Join(entry, "prefix"), false)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(entry, outHashFile), []byte(out+"\n"), 0o644); err != nil {
		return "", err
	}
	return out, nil
}

// recipeEnv is the environment a dep recipe runs in, shared by pinned
// and dev builds so a recipe behaves identically in both.
func recipeEnv(p *Project, name string, d *DepCfg, tc *Toolchain, prefix, work, src string) ([]string, error) {
	env := append(recipeBaseEnv(), tc.scriptEnv()...)
	env = append(env,
		"CMK_PREFIX="+prefix,
		"CMK_WORK="+work,
		"CMK_DEFAULT_JOBS="+fmt.Sprint(defaultJobs()),
		"CMK_PROJECT_ROOT="+p.Root,
	)
	if src != "" {
		env = append(env, "CMK_SRC="+src)
	}
	vars := p.vars()
	for _, k := range slices.Sorted(maps.Keys(d.Env)) {
		env = append(env, k+"="+expandVars(d.Env[k], vars))
	}
	for _, n := range d.Needs {
		pfx, err := p.depPrefix(n)
		if err != nil {
			return nil, fmt.Errorf("dep %s: %w", name, err)
		}
		env = append(env, "CMK_DEP_"+envName(n)+"_PREFIX="+pfx)
	}
	return append(env, needsSearchEnv(p, name)...), nil
}

// runRecipe executes a dep's recipe script in work with env.
func runRecipe(p *Project, name string, d *DepCfg, work string, env []string) error {
	cmd := exec.Command("bash", filepath.Join(p.Root, d.Script))
	cmd.Dir = work
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = env
	start := time.Now()
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("dep %s: %s failed: %w", name, d.Script, err)
	}
	fmt.Fprintf(os.Stderr, "cmk: dep %s done in %s\n", name, time.Since(start).Round(time.Second))
	return nil
}

// buildDep brings one dep's store entry into existence, returning the
// entry path, whether work was done and whether the lock changed.
// needOut must already contain output hashes for all needs. Entries are
// immutable once complete: a stamp change lands in a NEW entry, so build
// dirs that reference the old one stay valid.
func buildDep(p *Project, name string, tc *Toolchain, needOut map[string]string, force bool) (entry string, built, lockDirty bool, err error) {
	d := p.Cfg.Deps[name]
	lk := p.Lock
	patches, extras, err := depInputs(p.Root, name, d)
	if err != nil {
		return "", false, false, err
	}
	stamp, err := depStamp(p.Root, name, d, tc.ID, lk.Deps[name], needOut, patches, extras)
	if err != nil {
		return "", false, false, err
	}
	lockDirty = p.setDepStamp(name, stamp)

	entry = entryDir(name, stamp)
	marker := filepath.Join(entry, completeMarker)
	if !force {
		if _, err := os.Stat(marker); err == nil {
			return entry, false, lockDirty, nil
		}
	}

	flk, err := lockStoreEntry(name, stamp)
	if err != nil {
		return "", false, lockDirty, err
	}
	defer unlockFile(flk)
	if !force {
		// a concurrent cmk (another worktree) may have built it while
		// we waited for the lock
		if _, err := os.Stat(marker); err == nil {
			fmt.Fprintf(os.Stderr, "cmk: dep %s was built by a concurrent cmk\n", name)
			return entry, false, lockDirty, nil
		}
	}

	fmt.Fprintf(os.Stderr, "cmk: building dep %s\n", name)
	if err := os.RemoveAll(entry); err != nil {
		return "", false, lockDirty, err
	}
	prefix := filepath.Join(entry, "prefix")
	work := filepath.Join(entry, "work")
	for _, dir := range []string{prefix, work} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", false, lockDirty, err
		}
	}
	src, err := prepareSrc(entry, p.Root, d, lk.Deps[name], patches)
	if err != nil {
		return "", false, lockDirty, err
	}

	env, err := recipeEnv(p, name, d, tc, prefix, work, src)
	if err != nil {
		return "", false, lockDirty, err
	}
	if err := runRecipe(p, name, d, work, env); err != nil {
		return "", false, lockDirty, err
	}
	if _, err := writeEntryOutHash(entry); err != nil {
		return "", false, lockDirty, err
	}
	if err := os.WriteFile(marker, []byte(stamp+"\n"), 0o644); err != nil {
		return "", false, lockDirty, err
	}
	return entry, true, lockDirty, nil
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
// store, pinning their stamps in cmk.lock — except dev-affected stamps,
// which are quarantined in cmk.dev.yaml (saved here, since it is
// machine-local state no caller needs to coordinate).
func syncDeps(p *Project, tc *Toolchain, want []string, force bool) (lockDirty bool, err error) {
	order, err := topoOrder(p.Cfg.Deps, want)
	if err != nil {
		return false, err
	}
	if names := p.devOverrideNames(); len(names) > 0 {
		var lines []string
		for _, n := range names {
			lines = append(lines, n+" -> "+p.devOverrides()[n].Path)
		}
		fmt.Fprintf(os.Stderr, "cmk: dev overrides active: %s\n", strings.Join(lines, ", "))
	}
	defer func() {
		if saveErr := p.saveDevState(); saveErr != nil && err == nil {
			err = saveErr
		}
	}()
	lockDirty, err = ensureLockEntries(p.Cfg, p.Lock, order, p.devOverrides())
	if err != nil {
		return lockDirty, err
	}
	needOut := map[string]string{}
	built := 0
	for _, name := range order {
		// An empty want means "sync everything", so --force applies to
		// every dep in the order; a named want forces only what was named.
		forced := force && (len(want) == 0 || slices.Contains(want, name))
		var out string
		var did bool
		if path, ok := p.devPath(name); ok {
			out, did, err = buildDevDep(p, name, path, tc, needOut, forced)
		} else {
			var entry string
			var dirty bool
			entry, did, dirty, err = buildDep(p, name, tc, needOut, forced)
			lockDirty = lockDirty || dirty
			if err == nil {
				out, err = entryOutHash(entry)
			}
		}
		if err != nil {
			return lockDirty, err
		}
		needOut[name] = out
		if did {
			built++
		}
	}
	if built == 0 && len(order) > 0 {
		fmt.Fprintf(os.Stderr, "cmk: deps up to date (%d)\n", len(order))
	}
	p.Dev.pruneStamps(p.devAffected)
	return lockDirty, nil
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
	return slices.Sorted(maps.Keys(deps))
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
