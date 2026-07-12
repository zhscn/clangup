package cmk

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

// cmdConfig runs cmake configure with the toolchain and dep exports
// injected. Deps are synced first (cargo-style).
func cmdConfig(args []string) error {
	var buildDir string
	a := newArgSpec()
	a.strFlag(&buildDir, "-B", "--build-dir")
	if err := a.parse(args); err != nil {
		return err
	}
	presetName, err := a.atMostOnePos("config")
	if err != nil {
		return err
	}

	p, err := openProject()
	if err != nil {
		return err
	}
	mc := isMultiConfig(p.Cfg)
	if mc && presetName != "" {
		return fmt.Errorf("multi-config configures every configuration at once; "+
			"pick one at build time with `cmk build -c %s`, not `cmk config %s`", presetName, presetName)
	}
	var preset *PresetCfg
	if !mc {
		preset, err = resolvePreset(p.Cfg, presetName)
		if err != nil {
			return err
		}
	}
	dir := buildDir
	if dir != "" && !filepath.IsAbs(dir) {
		dir = filepath.Join(p.Root, dir)
	}
	if dir == "" {
		dir = defaultConfigureDir(p, preset)
	}
	return runConfigure(p, dir, preset, a.Rest)
}

// defaultConfigureDir is where configure puts the build tree when no -B
// is given: the preset's dir, the multi-config dir, or "build".
func defaultConfigureDir(p *Project, preset *PresetCfg) string {
	dir := ""
	if preset != nil {
		dir = expandVars(preset.Dir, p.vars())
	}
	if dir == "" && isMultiConfig(p.Cfg) {
		dir = p.multiConfigDir()
	}
	if dir == "" {
		dir = "build"
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(p.Root, dir)
	}
	return dir
}

// runConfigure is the single configure path: resolve the toolchain, sync
// deps, compute the injection, run cmake (--fresh when the injection
// changed), and refresh the generated artifacts (injection stamp, file
// API queries, CMakeUserPresets.json, root compile_commands.json). Both
// `cmk config` and build-time auto-reconfigure (ensureConfigured) land
// here, so a reconfigure behaves identically no matter what triggered it.
func runConfigure(p *Project, dir string, preset *PresetCfg, extraArgs []string) error {
	// Serialize configures of one build dir: two concurrent cmk
	// invocations that both detected staleness must not run cmake into
	// the same cache. The loser re-runs the --fresh decision below
	// against the winner's fresh stamp once it holds the lock (a
	// redundant but harmless reconfigure).
	lock, err := lockBuildDir(dir)
	if err != nil {
		return err
	}
	defer unlockBuildDir(lock)

	tc, err := p.toolchain()
	if err != nil {
		return err
	}
	depsDirty, err := syncDeps(p, tc, nil, false)
	if depsDirty {
		if saveErr := saveLock(p.Root, p.Lock); saveErr != nil && err == nil {
			err = saveErr
		}
	}
	if err != nil {
		return err
	}

	injected, stampArgs, err := computeInjection(p, tc, preset, extraArgs)
	if err != nil {
		return err
	}

	gen := p.Cfg.Configure.Generator
	if gen == "" {
		gen = "Ninja"
	}
	cmakeArgs := []string{"-S", p.Root, "-B", dir, "-G", gen}
	// When the injection changed since this build dir was last
	// configured, cached find_package() results (Boost_DIR, OPENSSL_*)
	// can short-circuit to the previous dep entries — store entries are
	// immutable, so the old paths still exist and the staleness would
	// be silent. --fresh discards the cache and re-finds everything.
	if fresh := injectionChanged(dir, stampArgs); fresh {
		if _, err := os.Stat(filepath.Join(dir, "CMakeCache.txt")); err == nil {
			fmt.Fprintf(os.Stderr, "cmk: injected configuration changed; reconfiguring %s with --fresh\n", dir)
			cmakeArgs = append(cmakeArgs, "--fresh")
		}
	}
	cmakeArgs = append(cmakeArgs, injected...)

	// Materialize the configuration flags include the injection points at.
	if err := writeConfigFlagsFile(p); err != nil {
		return err
	}
	// Queries must exist before cmake runs so this configure's generate
	// step writes the reply ensureConfigured reads.
	if err := p.ensureFileAPI(dir); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "+ cmake %s\n", shellQuote(cmakeArgs))
	cmd := exec.Command("cmake", cmakeArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = p.commandEnv(tc.envMap())
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("cmake configure failed: %w", err)
	}
	// The stamp's mtime doubles as the staleness baseline: written after
	// cmake finishes, every configure input is at least as old as it.
	writeInjectionStamp(dir, stampArgs, extraArgs)
	if err := writeUserPresets(p, tc); err != nil {
		return err
	}
	return p.syncRootCompileCommands(dir)
}

// injectionParts is the decomposed cmk injection — everything cmk adds on
// top of the project's own CMake configuration. The configure argv
// (argv), the stamp identity (stampArgs), and the generated
// CMakeUserPresets.json (writeUserPresets) are all projections of one
// value, so they cannot drift apart.
type injectionParts struct {
	// toolchain is the toolchain-file-vs-compiler-vars decision for
	// cmk's own configure. writeUserPresets re-makes this decision per
	// preset, since a preset may bring its own CMAKE_TOOLCHAIN_FILE.
	toolchain []string
	// launcher is the CMAKE_<LANG>_COMPILER_LAUNCHER injection.
	launcher []string
	// common are defines every projection carries
	// (CMAKE_EXPORT_COMPILE_COMMANDS).
	common []string
	// argvOnly are defines for cmk-managed build dirs only, deliberately
	// absent from the generated presets: CMAKE_SUPPRESS_REGENERATION
	// belongs where cmk itself runs the staleness checks
	// (ensureConfigured); an IDE-driven configure keeps CMake's own
	// regen rules.
	argvOnly []string
	// exports are the dep store injections (-D<Name>_ROOT=… or whatever
	// the recipes export).
	exports []string
	// multiConfig is the multi-config setup: CMAKE_CONFIGURATION_TYPES,
	// CMAKE_DEFAULT_BUILD_TYPE, and CMAKE_PROJECT_INCLUDE when there are
	// configuration flag edits.
	multiConfig []string
	// userArgs are the expanded [config].args, plus the selected
	// preset's args and ad-hoc CLI args when configuring.
	userArgs []string
	// flagsPath/flagsContent is the computed configurations include
	// (content "" when there is none).
	flagsPath    string
	flagsContent string
	// envStamp folds the [env] overlay into the identity: configure
	// reads it through $ENV{...}, and no file mtime reflects a change.
	envStamp []string
}

// injectionParts assembles the injection for one configure of this
// project: preset selects a [config.preset.*] (nil for multi-config and
// plain single-config), extraArgs are ad-hoc CLI args.
func (p *Project) injectionParts(tc *Toolchain, preset *PresetCfg, extraArgs []string) (*injectionParts, error) {
	vars := p.vars()
	exports, err := allDepExports(p)
	if err != nil {
		return nil, err
	}
	in := &injectionParts{
		launcher: launcherArgs(p.Cfg.Configure.CompilerLauncher),
		common:   []string{"-DCMAKE_EXPORT_COMPILE_COMMANDS=ON"},
		// cmk owns reconfiguration (see ensureConfigured): the generated
		// build system gets no regen rule, so a stale configure can never
		// be re-run by ninja with an environment cmk didn't control.
		argvOnly: []string{"-DCMAKE_SUPPRESS_REGENERATION=ON"},
		exports:  exports,
		userArgs: expandAll(p.Cfg.Configure.Args, vars),
		envStamp: envStampEntries(p),
	}
	if preset != nil {
		in.userArgs = append(in.userArgs, expandAll(preset.Args, vars)...)
	}
	in.userArgs = append(in.userArgs, extraArgs...)
	in.toolchain = tc.cmakeArgs(in.userArgs)
	in.flagsPath, in.flagsContent = configFlagsFile(p)
	if isMultiConfig(p.Cfg) {
		in.multiConfig = append(in.multiConfig,
			"-DCMAKE_CONFIGURATION_TYPES="+strings.Join(effectiveConfigurations(p.Cfg), ";"))
		if d := p.Cfg.Configure.Default; d != "" {
			in.multiConfig = append(in.multiConfig, "-DCMAKE_DEFAULT_BUILD_TYPE="+d)
		}
		if in.flagsContent != "" {
			in.multiConfig = append(in.multiConfig, "-DCMAKE_PROJECT_INCLUDE="+in.flagsPath)
		}
	}
	return in, nil
}

// argv is the configure argument list. Order matters to CMake — a later
// -D wins — so the user's args come last.
func (in *injectionParts) argv() []string {
	var out []string
	out = append(out, in.toolchain...)
	out = append(out, in.launcher...)
	out = append(out, in.common...)
	out = append(out, in.argvOnly...)
	out = append(out, in.exports...)
	out = append(out, in.multiConfig...)
	out = append(out, in.userArgs...)
	return out
}

// stampArgs is the injection identity recorded in the stamp: the argv
// plus the content hash of injected files and the [env] overlay.
func (in *injectionParts) stampArgs() []string {
	out := in.argv()
	if in.flagsContent != "" {
		// Hash the computed content, not the on-disk file: the identity
		// must not depend on whether the file has been materialized yet.
		sum := sha256.Sum256([]byte(in.flagsContent))
		out = append(out, "file:"+in.flagsPath+"="+hex.EncodeToString(sum[:]))
	}
	return append(out, in.envStamp...)
}

// computeInjection is the argv/stamp projection of injectionParts.
func computeInjection(p *Project, tc *Toolchain, preset *PresetCfg, extraArgs []string) (injected, stampArgs []string, err error) {
	parts, err := p.injectionParts(tc, preset, extraArgs)
	if err != nil {
		return nil, nil, err
	}
	return parts.argv(), parts.stampArgs(), nil
}

// envStampEntries folds the expanded [env] section into the injection
// identity: configure reads it through $ENV{...} and sub-configures
// inherit it, so a change must reconfigure like any -D change would.
func envStampEntries(p *Project) []string {
	if len(p.Cfg.Env) == 0 {
		return nil
	}
	vars := p.vars()
	keys := make([]string, 0, len(p.Cfg.Env))
	for k := range p.Cfg.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, "env:"+k+"="+expandVars(p.Cfg.Env[k], vars))
	}
	return out
}

// launcherArgs resolves the configured compiler launcher (ccache/sccache)
// and returns the CMAKE_<LANG>_COMPILER_LAUNCHER injection. An absent
// launcher is a warning, not an error: better to build slowly than to
// fail because ccache isn't installed on this host.
func launcherArgs(launcher string) []string {
	if launcher == "" {
		return nil
	}
	path, err := exec.LookPath(launcher)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cmk: warning: compiler_launcher %q not found on PATH; building without it\n", launcher)
		return nil
	}
	return []string{
		"-DCMAKE_C_COMPILER_LAUNCHER=" + path,
		"-DCMAKE_CXX_COMPILER_LAUNCHER=" + path,
	}
}

// lockBuildDir takes an exclusive flock on <dir>/.cmk-lock, creating the
// dir if this is its first configure. The store has its own entry locks
// (deps.go); this one only covers the build tree.
func lockBuildDir(dir string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, ".cmk-lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, fmt.Errorf("locking build dir %s: %w", dir, err)
	}
	return f, nil
}

func unlockBuildDir(f *os.File) {
	syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	f.Close()
}

const injectionStampFile = ".cmk-injection"

func injectionHash(args []string) string {
	h := sha256.New()
	for _, a := range args {
		h.Write([]byte(a))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// injectionStamp is the .cmk-injection record: the injection identity
// hash, plus the ad-hoc CLI args (`cmk config -- -DFOO=ON`) that were
// part of it — recorded so an automatic reconfigure re-applies them
// instead of silently dropping them with the --fresh cache wipe. An
// explicit `cmk config` remains authoritative and resets them.
type injectionStamp struct {
	Hash  string   `json:"hash"`
	Extra []string `json:"extra,omitempty"`
}

// loadInjectionStamp returns the recorded stamp, or nil when the dir has
// none (never cmk-configured, or a pre-JSON stamp — either way the next
// configure re-stamps it).
func loadInjectionStamp(dir string) *injectionStamp {
	data, err := os.ReadFile(filepath.Join(dir, injectionStampFile))
	if err != nil {
		return nil
	}
	var st injectionStamp
	if json.Unmarshal(data, &st) != nil || st.Hash == "" {
		return nil
	}
	return &st
}

// stampExtra returns the ad-hoc CLI args recorded by the last configure.
func stampExtra(dir string) []string {
	if st := loadInjectionStamp(dir); st != nil {
		return st.Extra
	}
	return nil
}

// injectionChanged reports whether the build dir was last configured
// with a different injected argument set (or by an older cmk that
// didn't record one).
func injectionChanged(dir string, args []string) bool {
	st := loadInjectionStamp(dir)
	return st == nil || st.Hash != injectionHash(args)
}

func writeInjectionStamp(dir string, args, extra []string) {
	data, err := json.Marshal(&injectionStamp{Hash: injectionHash(args), Extra: extra})
	if err == nil {
		err = os.WriteFile(filepath.Join(dir, injectionStampFile), append(data, '\n'), 0o644)
	}
	if err != nil {
		// Not fatal for this run, but without a stamp every subsequent
		// build reconfigures with --fresh; say why instead of looping
		// silently.
		fmt.Fprintf(os.Stderr, "cmk: warning: cannot write %s: %v (every build will reconfigure until this is fixed)\n",
			filepath.Join(dir, injectionStampFile), err)
	}
}

// resolvePreset picks the named preset, the configured default, the
// single defined preset, or none.
func resolvePreset(cfg *Config, name string) (*PresetCfg, error) {
	presets := cfg.Configure.Presets
	if name != "" {
		pr, ok := presets[name]
		if !ok {
			return nil, fmt.Errorf("preset %q not found (known: %s)", name, strings.Join(presetNames(presets), ", "))
		}
		return pr, nil
	}
	if d := cfg.Configure.Default; d != "" {
		pr, ok := presets[d]
		if !ok {
			return nil, fmt.Errorf("[config].default = %q does not name a preset", d)
		}
		return pr, nil
	}
	switch len(presets) {
	case 0:
		return nil, nil
	case 1:
		for _, pr := range presets {
			return pr, nil
		}
	}
	return nil, fmt.Errorf("multiple presets defined; pick one of: %s (or set [config].default)",
		strings.Join(presetNames(presets), ", "))
}

func presetNames(presets map[string]*PresetCfg) []string {
	names := make([]string, 0, len(presets))
	for n := range presets {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
