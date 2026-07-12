package cmk

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type Project struct {
	Root string
	Cfg  *Config
	// Lock is the loaded cmk.lock; dep store paths resolve through its
	// pinned stamps.
	Lock *Lock
	// BuildDirs maps root-relative paths to absolute paths of
	// directories containing a CMakeCache.txt.
	BuildDirs map[string]string
	// tc caches the resolved toolchain (see Project.toolchain).
	tc *Toolchain
}

// toolchain resolves the pinned toolchain once per invocation, persisting
// a changed lock pin on first resolution. Every consumer goes through
// here, so one cmk command can never see two different resolutions and
// the resolve+save boilerplate lives in one place.
func (p *Project) toolchain() (*Toolchain, error) {
	if p.tc != nil {
		return p.tc, nil
	}
	tc, dirty, err := resolveToolchain(p.Cfg.Toolchain.Selector, p.Lock)
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
	p := &Project{Root: root, Cfg: cfg, Lock: lk, BuildDirs: map[string]string{}}
	p.scanBuildDirs()
	return p, nil
}

// findProjectRoot walks up from the PWD looking for cmk.toml, falling
// back to the git toplevel (preferring a superproject).
func findProjectRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for d := dir; ; {
		if _, err := os.Stat(filepath.Join(d, configFileName)); err == nil {
			return d, nil
		}
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	cmd := exec.Command("git", "rev-parse", "--show-superproject-working-tree", "--show-toplevel")
	cmd.Env = append(os.Environ(), "GIT_DISCOVERY_ACROSS_FILESYSTEM=1")
	out, err := cmd.Output()
	if err != nil {
		return "", errors.New("no cmk.toml found and not inside a git repository")
	}
	head, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	if head == "" {
		return "", errors.New("cannot determine project root")
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
	p.collectBuildDirs(p.Root, 1, maxScanDepth())
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
	keys := make([]string, 0, len(p.BuildDirs))
	for k := range p.BuildDirs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// resolveBuildDir follows the cascade: explicit name → single dir → PWD
// inside a dir → [build].default → fzf prompt.
func (p *Project) resolveBuildDir(name string) (string, error) {
	if name != "" {
		if abs, ok := p.BuildDirs[name]; ok {
			return abs, nil
		}
		// allow absolute or PWD-relative paths too
		if abs, err := filepath.Abs(name); err == nil {
			if _, err := os.Stat(filepath.Join(abs, "CMakeCache.txt")); err == nil {
				return abs, nil
			}
		}
		return "", fmt.Errorf("build directory %q not found (known: %s)",
			name, strings.Join(p.listBuildDirs(), ", "))
	}
	if len(p.BuildDirs) == 0 {
		return "", errors.New("no CMake build directories found; run `cmk config` first")
	}
	if len(p.BuildDirs) == 1 {
		for _, abs := range p.BuildDirs {
			return abs, nil
		}
	}
	if pwd, err := os.Getwd(); err == nil {
		for _, abs := range p.BuildDirs {
			if pwd == abs || strings.HasPrefix(pwd, abs+string(filepath.Separator)) {
				return abs, nil
			}
		}
	}
	if d := p.Cfg.Build.Default; d != "" {
		if abs, ok := p.BuildDirs[d]; ok {
			return abs, nil
		}
		return "", fmt.Errorf("configured default build dir %q not found (known: %s)",
			d, strings.Join(p.listBuildDirs(), ", "))
	}
	// fall back to the default preset's dir before prompting
	if d := p.Cfg.Configure.Default; d != "" {
		if pr := p.Cfg.Configure.Presets[d]; pr != nil && pr.Dir != "" {
			if abs, ok := p.BuildDirs[filepath.Clean(expandVars(pr.Dir, p.vars()))]; ok {
				return abs, nil
			}
		}
	}
	sel, err := completingRead(p.listBuildDirs())
	if err != nil {
		return "", err
	}
	return p.BuildDirs[sel], nil
}

// hasCmkToml reports whether the project declares itself cmk-managed.
func (p *Project) hasCmkToml() bool {
	_, err := os.Stat(filepath.Join(p.Root, configFileName))
	return err == nil
}

// vars returns the expansion variables available in cmk.toml values.
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

func (p *Project) commandEnvWithToolchain(layers ...map[string]string) ([]string, error) {
	tc, err := p.toolchain()
	if err != nil {
		return nil, err
	}
	all := append([]map[string]string{tc.envMap()}, layers...)
	return p.commandEnv(all...), nil
}

// ccacheEnv configures ccache for cross-worktree reuse when [config]
// compiler_launcher = "ccache". CCACHE_BASEDIR rewrites absolute paths
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

// --- CMake file API ---

type Target struct {
	Name string `json:"name"`
	Type string `json:"type"`
	// Imported is true for targets pulled in from outside the build
	// (e.g. Git::Git from find_package, whose artifact is /usr/bin/git).
	// They are not ours to run or build, so they're filtered out.
	Imported  bool `json:"imported"`
	Artifacts []struct {
		Path string `json:"path"`
	} `json:"artifacts"`
}

func (t *Target) isExecutable() bool { return t.Type == "EXECUTABLE" }

// ensureFileAPI plants the shared stateless queries cmk relies on:
// codemodel for target discovery, cmakeFiles for staleness detection
// (see ensureConfigured). CMake rewrites the replies on every configure.
func (p *Project) ensureFileAPI(buildDir string) error {
	queryDir := filepath.Join(buildDir, ".cmake/api/v1/query")
	if err := os.MkdirAll(queryDir, 0o755); err != nil {
		return err
	}
	for _, query := range []string{"codemodel-v2", "cmakeFiles-v1"} {
		marker := filepath.Join(queryDir, query)
		if _, err := os.Stat(marker); os.IsNotExist(err) {
			if err := os.WriteFile(marker, nil, 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}

// codemodelReply is the slice of the CMake file API codemodel we need:
// the per-configuration target lists, each pointing at a target object
// file. In multi-config builds the artifact paths differ per config, so
// targets must be read through the chosen configuration's entry.
type codemodelReply struct {
	Configurations []struct {
		Name    string `json:"name"`
		Targets []struct {
			Name     string `json:"name"`
			JSONFile string `json:"jsonFile"`
		} `json:"targets"`
	} `json:"configurations"`
}

func readCodemodel(replyDir string) (*codemodelReply, error) {
	var cm codemodelReply
	if err := readReplyObject(replyDir, "codemodel-v2", &cm); err != nil {
		return nil, err
	}
	return &cm, nil
}

// collectTargets reads the targets for the given configuration ("" picks
// the only/first one — the single-config case). A missing reply (a build
// dir configured before the file API queries existed) triggers one full
// reconfigure to populate it.
func (p *Project) collectTargets(buildDir, config string) ([]Target, error) {
	replyDir := filepath.Join(buildDir, ".cmake/api/v1/reply")
	cm, err := readCodemodel(replyDir)
	if err != nil {
		if err := runConfigure(p, buildDir, presetForDir(p, buildDir), stampExtra(buildDir)); err != nil {
			return nil, err
		}
		cm, err = readCodemodel(replyDir)
		if err != nil {
			return nil, err
		}
	}
	if len(cm.Configurations) == 0 {
		return nil, fmt.Errorf("no configurations in CMake file API reply for %s", buildDir)
	}
	idx := 0
	if config != "" {
		idx = -1
		for i, c := range cm.Configurations {
			if c.Name == config {
				idx = i
				break
			}
		}
		if idx < 0 {
			names := make([]string, len(cm.Configurations))
			for i, c := range cm.Configurations {
				names[i] = c.Name
			}
			return nil, fmt.Errorf("configuration %q not configured in %s (have: %s); run `cmk config`",
				config, buildDir, strings.Join(names, ", "))
		}
	}
	var targets []Target
	for _, ref := range cm.Configurations[idx].Targets {
		data, err := os.ReadFile(filepath.Join(replyDir, ref.JSONFile))
		if err != nil {
			return nil, err
		}
		var t Target
		if err := json.Unmarshal(data, &t); err != nil {
			continue
		}
		targets = append(targets, t)
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].Name < targets[j].Name })
	return targets, nil
}

func (p *Project) executableTargets(buildDir, config string) ([]Target, error) {
	all, err := p.collectTargets(buildDir, config)
	if err != nil {
		return nil, err
	}
	var out []Target
	for _, t := range all {
		if t.isExecutable() && !t.Imported && len(t.Artifacts) > 0 {
			out = append(out, t)
		}
	}
	return out, nil
}
