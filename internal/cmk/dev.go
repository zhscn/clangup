package cmk

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// A dev override redirects one dependency from its pinned source to a
// local checkout — the fork-iteration escape hatch. Overrides are
// machine-local state (cmk.dev.yaml, never committed): cmk.yaml and
// cmk.lock keep describing the pinned world the team shares, and every
// stamp that depends on an override is quarantined in the same file so
// local experiments can never leak into the committed lock.
//
// An overridden dep builds into a MUTABLE store entry: the work tree
// survives across source edits, the recipe re-runs in place, and
// CMK_SRC points straight at the checkout — so the dep's own build
// system does incremental work instead of the usual clone-and-rebuild.
// The entry's path is stable across edits, which also keeps configured
// build dirs from churning through reconfigures on every iteration.

const devFileName = "cmk.dev.yaml"

const devFileHeader = `# Machine-local dev overrides, managed by ` + "`cmk dev`" + `. Do not commit
# this file (add it to .gitignore).
`

type DevState struct {
	Deps map[string]*DevDep `yaml:"dependencies,omitempty"`
	// Stamps quarantines the stamps of deps whose identity depends on an
	// override (the dependents of an overridden dep): platform -> dep ->
	// stamp. They belong to the overridden world, so they must not be
	// written into cmk.lock.
	Stamps map[string]map[string]string `yaml:"stamps,omitempty"`
	dirty  bool
}

type DevDep struct {
	Path string `yaml:"path"`
}

func (ds *DevState) empty() bool {
	return len(ds.Deps) == 0 && len(ds.Stamps) == 0
}

func (ds *DevState) stampFor(platform, name string) string {
	if ds == nil {
		return ""
	}
	return ds.Stamps[platform][name]
}

func (ds *DevState) setStampFor(platform, name, stamp string) {
	if ds.Stamps == nil {
		ds.Stamps = map[string]map[string]string{}
	}
	if ds.Stamps[platform] == nil {
		ds.Stamps[platform] = map[string]string{}
	}
	if ds.Stamps[platform][name] != stamp {
		ds.Stamps[platform][name] = stamp
		ds.dirty = true
	}
}

// pruneStamps drops quarantined stamps for deps that no longer depend on
// an override (after `cmk dev --drop`, or a needs edit). Affectedness is
// structural, so it applies across platforms.
func (ds *DevState) pruneStamps(affected map[string]bool) {
	if ds == nil {
		return
	}
	for platform, stamps := range ds.Stamps {
		for name := range stamps {
			if !affected[name] {
				delete(stamps, name)
				ds.dirty = true
			}
		}
		if len(stamps) == 0 {
			delete(ds.Stamps, platform)
			ds.dirty = true
		}
	}
}

// loadDevState reads cmk.dev.yaml, dropping overrides for deps that are
// no longer in cmk.yaml (a branch switch shouldn't error the whole
// project; the file is rewritten on the next state change).
func loadDevState(root string, cfg *Config) (*DevState, error) {
	ds := &DevState{Deps: map[string]*DevDep{}}
	data, err := os.ReadFile(filepath.Join(root, devFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return ds, nil
		}
		return nil, err
	}
	if err := yaml.Unmarshal(data, ds); err != nil {
		return nil, fmt.Errorf("%s: %w", devFileName, err)
	}
	if ds.Deps == nil {
		ds.Deps = map[string]*DevDep{}
	}
	for name, d := range ds.Deps {
		if _, ok := cfg.Deps[name]; !ok || d == nil || d.Path == "" {
			fmt.Fprintf(os.Stderr, "cmk: warning: %s: ignoring override for unknown dep %q\n", devFileName, name)
			delete(ds.Deps, name)
			ds.dirty = true
		}
	}
	return ds, nil
}

func saveDevFile(root string, ds *DevState) error {
	path := filepath.Join(root, devFileName)
	if ds.empty() {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		ds.dirty = false
		return nil
	}
	data, err := yaml.Marshal(ds)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append([]byte(devFileHeader), data...), 0o644); err != nil {
		return err
	}
	ds.dirty = false
	return nil
}

// devEntryDir is the mutable store entry of an overridden dep, keyed by
// project root and checkout path: the same override always lands in the
// same entry (that stability is what makes the build incremental), while
// two projects — or two checkouts — can never clobber each other.
func devEntryDir(root, name, path string) string {
	h := sha256.Sum256([]byte(root + "\x00" + path))
	return filepath.Join(storeDir(), "dev", name+"-"+hex.EncodeToString(h[:])[:12])
}

// devMarker is the dev entry's .cmk-complete: unlike a pinned entry
// (immutable, marker == built) the dev entry records what it was last
// built from, so sync knows whether to skip, re-run in place, or wipe.
type devMarker struct {
	// Input is the full input identity: toolchain, config, and source tree.
	Input string `json:"input"`
	// Toolchain is the wipe key: the work tree is only reusable while the
	// compiler stays the same (CMake refuses a compiler change in an
	// existing build tree), so a mismatch rebuilds from scratch.
	Toolchain string `json:"toolchain"`
}

func loadDevMarker(entry string) *devMarker {
	data, err := os.ReadFile(filepath.Join(entry, completeMarker))
	if err != nil {
		return nil
	}
	var m devMarker
	if json.Unmarshal(data, &m) != nil || m.Input == "" {
		return nil
	}
	return &m
}

func hashStrings(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// devTreeHash is the source identity of a local checkout. A git checkout
// hashes HEAD plus the content of every dirty and untracked file — fast,
// and gitignored build artifacts don't churn the identity. A plain
// directory falls back to hashing the whole tree.
func devTreeHash(dir string) (string, error) {
	if err := exec.Command("git", "-C", dir, "rev-parse", "--git-dir").Run(); err == nil {
		return gitTreeHash(dir)
	}
	return hashTree(dir, true)
}

func gitTreeHash(dir string) (string, error) {
	head := "none"
	if out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output(); err == nil {
		head = strings.TrimSpace(string(out))
	}
	top := dir
	if out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output(); err == nil {
		top = strings.TrimSpace(string(out))
	}
	out, err := exec.Command("git", "-C", dir, "status", "--porcelain", "-z", "-uall", "--", dir).Output()
	if err != nil {
		return "", fmt.Errorf("git status in %s: %w", dir, err)
	}
	// -z fields: "XY <path>", with a rename's original path as an extra field.
	var paths []string
	fields := strings.Split(string(out), "\x00")
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if len(f) < 4 {
			continue
		}
		if f[0] == 'R' || f[0] == 'C' {
			i++
		}
		paths = append(paths, f[3:])
	}
	sort.Strings(paths)

	h := sha256.New()
	w := func(parts ...string) {
		for _, p := range parts {
			h.Write([]byte(p))
			h.Write([]byte{0})
		}
	}
	w("cmk-tree-v1", head)
	for _, rel := range paths {
		w("path", rel)
		hashTreeEntry(h, filepath.Join(top, filepath.FromSlash(rel)))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// hashTree hashes a directory tree: names, file kinds, exec bits and
// contents. It identifies dev sources without git (skipGit drops a .git
// dir) and, with skipGit false, store entry prefixes for the dep output
// hash.
func hashTree(dir string, skipGit bool) (string, error) {
	h := sha256.New()
	w := func(parts ...string) {
		for _, p := range parts {
			h.Write([]byte(p))
			h.Write([]byte{0})
		}
	}
	w("cmk-walk-v1")
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil || rel == "." {
			return relErr
		}
		if skipGit && d.IsDir() && d.Name() == ".git" {
			return fs.SkipDir
		}
		if rel == completeMarker || rel == outHashFile {
			return nil
		}
		w("path", filepath.ToSlash(rel))
		if d.IsDir() {
			w("dir")
			return nil
		}
		hashTreeEntry(h, path)
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// hashTreeEntry hashes what one path points at; a broken or vanished
// path hashes as absent rather than erroring, since dirty checkouts are
// allowed to be mid-edit.
func hashTreeEntry(h hash.Hash, path string) {
	write := func(parts ...string) {
		for _, p := range parts {
			h.Write([]byte(p))
			h.Write([]byte{0})
		}
	}
	fi, err := os.Lstat(path)
	switch {
	case err != nil:
		write("absent")
	case fi.IsDir():
		write("dir")
	case fi.Mode()&os.ModeSymlink != 0:
		target, _ := os.Readlink(path)
		write("link", target)
	default:
		data, err := os.ReadFile(path)
		if err != nil {
			write("absent")
			return
		}
		kind := "file"
		if fi.Mode()&0o111 != 0 {
			kind = "exec"
		}
		write(kind)
		h.Write(data)
		h.Write([]byte{0})
	}
}

// buildDevDep brings an overridden dep's mutable store entry up to date
// with its local checkout. Unchanged inputs skip; a source or config
// change re-runs the recipe in the surviving work tree (the incremental
// path this entire mechanism exists for); only a toolchain change or
// --force wipes the entry.
func buildDevDep(p *Project, name, srcPath string, tc *Toolchain, needOut map[string]string, force bool) (outHash string, built bool, err error) {
	d := p.Cfg.Deps[name]
	patches, extras, err := depInputs(p.Root, name, d)
	if err != nil {
		return "", false, err
	}
	if len(patches) > 0 {
		fmt.Fprintf(os.Stderr, "cmk: dep %s: patches are not applied to the dev override source\n", name)
	}
	srcHash, err := devTreeHash(srcPath)
	if err != nil {
		return "", false, err
	}
	script, err := os.ReadFile(filepath.Join(p.Root, d.Script))
	if err != nil {
		return "", false, fmt.Errorf("dependencies.%s: %w", name, err)
	}
	toolKey := hashStrings("cmk-dev-tc-v1", tc.ID)
	cfgParts := []string{"cmk-dev-cfg-v1", name, string(script)}
	for _, k := range slices.Sorted(maps.Keys(d.Env)) {
		cfgParts = append(cfgParts, "env", k, d.Env[k])
	}
	for _, rel := range extras {
		data, err := os.ReadFile(filepath.Join(p.Root, rel))
		if err != nil {
			return "", false, fmt.Errorf("dependencies.%s: %w", name, err)
		}
		cfgParts = append(cfgParts, "input", rel, string(data))
	}
	needs := append([]string(nil), d.Needs...)
	sort.Strings(needs)
	for _, n := range needs {
		cfgParts = append(cfgParts, "need-out", n, needOut[n])
	}
	input := hashStrings("cmk-dev-v1", toolKey, hashStrings(cfgParts...), srcHash)

	entry := devEntryDir(p.Root, name, srcPath)
	upToDate := func() bool {
		m := loadDevMarker(entry)
		return m != nil && m.Input == input
	}
	if !force && upToDate() {
		out, err := entryOutHash(entry)
		return out, false, err
	}

	flk, err := lockStoreEntry(name, "dev-"+filepath.Base(entry)[len(name)+1:])
	if err != nil {
		return "", false, err
	}
	defer unlockFile(flk)
	if !force && upToDate() {
		out, err := entryOutHash(entry)
		return out, false, err
	}

	if m := loadDevMarker(entry); force || m == nil || m.Toolchain != toolKey {
		if err := os.RemoveAll(entry); err != nil {
			return "", false, err
		}
	}
	// The install tree is rebuilt from scratch every time — an in-place
	// re-install must not leave files a renamed target no longer owns —
	// but work/ survives, so the recipe's build system sees its own
	// previous state and rebuilds incrementally.
	prefix := filepath.Join(entry, "prefix")
	work := filepath.Join(entry, "work")
	if err := os.RemoveAll(prefix); err != nil {
		return "", false, err
	}
	if err := os.Remove(filepath.Join(entry, completeMarker)); err != nil && !os.IsNotExist(err) {
		return "", false, err
	}
	for _, dir := range []string{prefix, work} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", false, err
		}
	}

	fmt.Fprintf(os.Stderr, "cmk: building dep %s from %s\n", name, srcPath)
	env, err := recipeEnv(p, name, d, tc, prefix, work, srcPath)
	if err != nil {
		return "", false, err
	}
	if err := runRecipe(p, name, d, work, env); err != nil {
		return "", false, fmt.Errorf("%w (a stale work tree can break after big changes; `cmk sync --force %s` rebuilds from scratch)", err, name)
	}

	out, err := writeEntryOutHash(entry)
	if err != nil {
		return "", false, err
	}
	data, err := json.Marshal(&devMarker{Input: input, Toolchain: toolKey})
	if err != nil {
		return "", false, err
	}
	if err := os.WriteFile(filepath.Join(entry, completeMarker), append(data, '\n'), 0o644); err != nil {
		return "", false, err
	}
	return out, true, nil
}
