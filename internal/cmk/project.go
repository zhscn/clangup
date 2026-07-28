package cmk

import (
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
)

type Project struct {
	Root string
	Cfg  *Config
	// Lock is the loaded cmk.lock; dep store paths resolve through its
	// pinned stamps.
	Lock *Lock
	// Dev is the machine-local dev override state (cmk.dev.yaml): deps
	// redirected to local checkouts, plus the quarantined stamps of
	// everything downstream of them. Never nil.
	Dev *DevState
	// BuildDirs maps root-relative paths to absolute paths of
	// directories containing a CMakeCache.txt.
	BuildDirs map[string]string
	// devAffected marks deps whose stamp depends on a dev override: the
	// overridden deps themselves and their transitive dependents. Their
	// stamps live in Dev, not Lock.
	devAffected map[string]bool
	// tc caches the resolved toolchain (see Project.toolchain).
	tc *Toolchain
}

// devOverrides is the nil-safe view of the override map (tests build
// Projects by hand without a DevState).
func (p *Project) devOverrides() map[string]*DevDep {
	if p.Dev == nil {
		return nil
	}
	return p.Dev.Deps
}

func (p *Project) devPath(name string) (string, bool) {
	if d := p.devOverrides()[name]; d != nil {
		return d.Path, true
	}
	return "", false
}

func (p *Project) hasDevOverrides() bool { return len(p.devOverrides()) > 0 }

func (p *Project) devOverrideNames() []string {
	return slices.Sorted(maps.Keys(p.devOverrides()))
}

// devAffectedNames is the sync subset that keeps dev overrides fresh: the
// overridden deps and everything downstream of them.
func (p *Project) devAffectedNames() []string {
	return slices.Sorted(maps.Keys(p.devAffected))
}

func (p *Project) computeDevAffected() {
	p.devAffected = map[string]bool{}
	overrides := p.devOverrides()
	if len(overrides) == 0 {
		return
	}
	for name := range p.Cfg.Deps {
		if _, ok := overrides[name]; ok {
			p.devAffected[name] = true
			continue
		}
		for _, n := range needsClosure(p.Cfg.Deps, name) {
			if _, ok := overrides[n]; ok {
				p.devAffected[name] = true
				break
			}
		}
	}
}

// depStampFor is the single stamp lookup: dev-affected deps read the
// quarantined stamp in cmk.dev.yaml, everything else the pin in cmk.lock.
func (p *Project) depStampFor(name string) string {
	if p.devAffected[name] {
		return p.Dev.stampFor(hostPlatform(runtime.GOOS, runtime.GOARCH), name)
	}
	return p.Lock.Deps[name].stampFor(runtime.GOOS, runtime.GOARCH)
}

// setDepStamp records a freshly computed stamp, reporting whether
// cmk.lock changed. A dev-affected stamp goes to the quarantine instead:
// the committed lock must keep describing the pinned world.
func (p *Project) setDepStamp(name, stamp string) (lockChanged bool) {
	if p.devAffected[name] {
		p.Dev.setStampFor(hostPlatform(runtime.GOOS, runtime.GOARCH), name, stamp)
		return false
	}
	ld := p.Lock.Deps[name]
	if ld == nil {
		ld = &LockDep{}
		p.Lock.Deps[name] = ld
	}
	if ld.stampFor(runtime.GOOS, runtime.GOARCH) != stamp {
		ld.setStampFor(runtime.GOOS, runtime.GOARCH, stamp)
		return true
	}
	return false
}

func (p *Project) saveDevState() error {
	if p.Dev == nil || !p.Dev.dirty {
		return nil
	}
	return saveDevFile(p.Root, p.Dev)
}

// toolchain resolves the pinned toolchain once per invocation, persisting
// a changed lock pin on first resolution. Every consumer goes through
// here, so one cmk command can never see two different resolutions and
// the resolve+save boilerplate lives in one place.
func (p *Project) toolchain() (*Toolchain, error) {
	if p.tc != nil {
		return p.tc, nil
	}
	tc, dirty, err := resolveToolchain(p.toolchainSelector(), p.Lock)
	if err != nil {
		return nil, err
	}
	if dirty {
		if err := saveLock(p.Root, p.Lock); err != nil {
			return nil, err
		}
	}
	p.tc = tc
	return tc, nil
}

func (p *Project) toolchainSelector() string {
	return p.Cfg.Toolchain.selectorFor(runtime.GOOS, runtime.GOARCH)
}

// tool resolves a toolchain program such as clang-format or clang-tidy.
// With a selector it comes from the pinned clangup toolchain; without one
// there is no toolchain to resolve and PATH decides directly — going
// through Project.toolchain would insist on discovering a C/C++ compiler,
// which formatting and linting have no use for.
func (p *Project) tool(name string) (string, error) {
	if p.toolchainSelector() == "" {
		path, err := exec.LookPath(name)
		if err != nil {
			return "", fmt.Errorf("%s not found on PATH", name)
		}
		return path, nil
	}
	tc, err := p.toolchain()
	if err != nil {
		return "", err
	}
	return tc.command(name)
}

func openProject() (*Project, error) {
	root, err := findProjectRoot()
	if err != nil {
		return nil, err
	}
	cfg, err := loadConfig(root)
	if err != nil {
		return nil, err
	}
	lk, err := loadLock(root)
	if err != nil {
		return nil, err
	}
	dev, err := loadDevState(root, cfg)
	if err != nil {
		return nil, err
	}
	p := &Project{Root: root, Cfg: cfg, Lock: lk, Dev: dev, BuildDirs: map[string]string{}}
	p.computeDevAffected()
	p.scanBuildDirs()
	p.registerManagedBuildDirs()
	return p, nil
}

// findProjectRoot walks up from the PWD looking for cmk.yaml, then uses an
// enclosing foreign CMake build tree, the git toplevel, or the PWD.
func findProjectRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	nearestBuildTree := ""
	for d := dir; ; {
		if _, err := os.Stat(filepath.Join(d, configFileName)); err == nil {
			return d, nil
		}
		if nearestBuildTree == "" {
			if _, err := os.Stat(filepath.Join(d, "CMakeCache.txt")); err == nil {
				nearestBuildTree = d
			}
		}
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	if nearestBuildTree != "" {
		return nearestBuildTree, nil
	}
	cmd := exec.Command("git", "rev-parse", "--show-superproject-working-tree", "--show-toplevel")
	cmd.Env = append(os.Environ(), "GIT_DISCOVERY_ACROSS_FILESYSTEM=1")
	out, err := cmd.Output()
	if err != nil {
		return dir, nil
	}
	head, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	if head == "" {
		return dir, nil
	}
	return head, nil
}

func maxScanDepth() int {
	if s := os.Getenv("CMK_MAX_DEPTH"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return n
		}
	}
	return 2
}

func (p *Project) scanBuildDirs() {
	if _, err := os.Stat(filepath.Join(p.Root, "CMakeCache.txt")); err == nil {
		p.BuildDirs["."] = p.Root
	}
	p.collectBuildDirs(p.Root, 1, maxScanDepth())
}

func (p *Project) registerManagedBuildDirs() {
	if !p.hasCmkConfig() {
		return
	}
	for _, preset := range p.Cfg.Configure.Presets {
		path := presetBuildDir(p, preset)
		if _, err := os.Stat(filepath.Join(path, "CMakeCache.txt")); err != nil {
			continue
		}
		rel, err := filepath.Rel(p.Root, path)
		if err == nil {
			p.BuildDirs[rel] = path
		}
	}
}

func (p *Project) collectBuildDirs(dir string, depth, maxDepth int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if _, err := os.Stat(filepath.Join(path, "CMakeCache.txt")); err == nil {
			rel, err := filepath.Rel(p.Root, path)
			if err == nil {
				p.BuildDirs[rel] = path
			}
		}
		if depth < maxDepth {
			p.collectBuildDirs(path, depth+1, maxDepth)
		}
	}
}

func (p *Project) listBuildDirs() []string {
	return slices.Sorted(maps.Keys(p.BuildDirs))
}

// resolveBuildDir follows the cascade: explicit name, PWD inside a build
// tree, the only known tree, the managed default preset, then fzf.
func (p *Project) resolveBuildDir(name string) (string, error) {
	if name != "" {
		if abs, ok := p.BuildDirs[name]; ok {
			return abs, nil
		}
		candidate := name
		if p.hasCmkConfig() && !filepath.IsAbs(candidate) {
			candidate = filepath.Join(p.Root, candidate)
		}
		// Managed paths are project-relative; foreign paths are PWD-relative.
		if abs, err := filepath.Abs(candidate); err == nil {
			if _, err := os.Stat(filepath.Join(abs, "CMakeCache.txt")); err == nil {
				return abs, nil
			}
		}
		return "", fmt.Errorf("build directory %q not found (known: %s)",
			name, strings.Join(p.listBuildDirs(), ", "))
	}
	if pwd, err := os.Getwd(); err == nil {
		for _, abs := range p.BuildDirs {
			if pwd == abs || strings.HasPrefix(pwd, abs+string(filepath.Separator)) {
				return abs, nil
			}
		}
	}
	if len(p.BuildDirs) == 0 {
		if !p.hasCmkConfig() {
			// `cmk config` is not an option here — it requires cmk.yaml.
			return "", fmt.Errorf("no CMake build directories found under %s; configure one with `cmake -B build`, or add %s and run `cmk config`",
				p.Root, configFileName)
		}
		return "", fmt.Errorf("no CMake build directories found; pass --build or run `cmk config`")
	}
	if len(p.BuildDirs) == 1 {
		for _, abs := range p.BuildDirs {
			return abs, nil
		}
	}
	if p.hasCmkConfig() {
		if preset := p.Cfg.Configure.Presets[p.Cfg.Configure.DefaultPreset]; preset != nil {
			path := presetBuildDir(p, preset)
			if _, err := os.Stat(filepath.Join(path, "CMakeCache.txt")); err == nil {
				return path, nil
			}
		}
	}
	sel, err := completingRead(p.listBuildDirs())
	if err != nil {
		return "", err
	}
	return p.BuildDirs[sel], nil
}

// hasCmkConfig reports whether the project declares itself cmk-managed.
func (p *Project) hasCmkConfig() bool {
	_, err := os.Stat(filepath.Join(p.Root, configFileName))
	return err == nil
}

// vars returns the expansion variables available in cmk.yaml values.
// ${DEP_<NAME>} resolves only once the dep has been synced (its stamp
// is in cmk.lock); before that the reference stays literal, which is
// visible enough to diagnose.
func (p *Project) vars() map[string]string {
	v := map[string]string{
		"PROJECT_ROOT": p.Root,
	}
	for name := range p.Cfg.Deps {
		if pfx, err := p.depPrefix(name); err == nil {
			v["DEP_"+envName(name)] = pfx
		}
	}
	return v
}

// commandEnv is os.Environ() plus the ccache defaults, the expanded [env]
// section, and any extra layers (later layers win: [env] overrides the
// ccache defaults, explicit layers override [env]).
func (p *Project) commandEnv(layers ...map[string]string) []string {
	vars := p.vars()
	env := os.Environ()
	merged := map[string]string{}
	for k, val := range p.ccacheEnv() {
		merged[k] = val
	}
	for k, val := range p.Cfg.Env {
		merged[k] = expandVars(val, vars)
	}
	for _, layer := range layers {
		for k, val := range layer {
			merged[k] = expandVars(val, vars)
		}
	}
	for k, val := range merged {
		env = append(env, k+"="+val)
	}
	return env
}

// buildEnv is the environment for driving a build tool (cmake --build,
// ninja). A cmk project builds inside its pinned toolchain. A foreign build
// tree records its own compiler in its cache and cmk overrides nothing
// there, so it needs no toolchain at all — resolving one would make
// `cmk build` in an existing tree fail on hosts where cmk can find no
// compiler of its own.
func (p *Project) buildEnv(layers ...map[string]string) ([]string, error) {
	if !p.hasCmkConfig() {
		return p.commandEnv(layers...), nil
	}
	tc, err := p.toolchain()
	if err != nil {
		return nil, err
	}
	return p.commandEnv(append([]map[string]string{tc.envMap()}, layers...)...), nil
}

// ccacheEnv configures ccache for cross-worktree reuse when the CMake
// launcher is ccache. CCACHE_BASEDIR rewrites absolute paths
// under the project root to relative before hashing, so the same TU built
// in another worktree (same layout, different absolute path) hits the
// cache; CCACHE_NOHASHDIR keeps the build directory out of the hash.
// Both defer to values already set in the environment.
func (p *Project) ccacheEnv() map[string]string {
	if filepath.Base(p.Cfg.Configure.CompilerLauncher) != "ccache" {
		return nil
	}
	m := map[string]string{}
	if os.Getenv("CCACHE_BASEDIR") == "" {
		m["CCACHE_BASEDIR"] = p.Root
	}
	if os.Getenv("CCACHE_NOHASHDIR") == "" {
		m["CCACHE_NOHASHDIR"] = "true"
	}
	return m
}
