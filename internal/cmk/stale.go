package cmk

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

// cmk owns reconfiguration: configure injects CMAKE_SUPPRESS_REGENERATION,
// so the generated build system has no regen rule and ninja never re-runs
// cmake behind cmk's back (with an environment cmk didn't set up, and
// without cmk's --fresh/deps/presets logic). Instead every build-time
// command calls ensureConfigured, which re-checks the same inputs CMake
// would — the file API cmakeFiles reply lists them — plus cmk's own
// (injection stamp, cmk.toml, dep recipes) and runs the full configure
// path when anything is stale.

// configurePolicy is how a build-time command treats a stale
// configuration.
type configurePolicy int

const (
	// configureAuto reconfigures stale dirs (the default).
	configureAuto configurePolicy = iota
	// configureLocked fails on staleness instead of self-healing — CI
	// semantics, like cargo --locked.
	configureLocked
	// configureSkip skips the checks entirely (--no-config).
	configureSkip
)

func configurePolicyFromFlags(locked, noConfig bool) (configurePolicy, error) {
	switch {
	case locked && noConfig:
		return 0, fmt.Errorf("pass either --locked or --no-config, not both")
	case locked:
		return configureLocked, nil
	case noConfig:
		return configureSkip, nil
	}
	return configureAuto, nil
}

// ensureConfigured reconfigures dir when its configuration is stale —
// or, under configureLocked, reports the staleness as an error.
//
// Implicit management needs an opt-in: a cmk.toml, or a build dir cmk
// itself configured (stamped) before. Anything else — a foreign project
// someone points `cmk build` at — keeps its own configuration and
// CMake's own regen rules, which no cmk configure ever suppressed there.
// An explicit `cmk config` still adopts such projects.
func ensureConfigured(p *Project, dir string, policy configurePolicy) error {
	if policy == configureSkip {
		return nil
	}
	if !p.hasCmkToml() && loadInjectionStamp(dir) == nil {
		return nil
	}
	if policy == configureLocked {
		// Bypass the memoized save: a lock pin that would change is
		// exactly what --locked exists to catch.
		tc, dirty, err := resolveToolchain(p.toolchainSelector(), p.Lock)
		if err != nil {
			return err
		}
		if dirty {
			return fmt.Errorf("--locked: cmk.lock does not match [toolchain] in cmk.toml; run `cmk config` (or `cmk update toolchain`) and commit the lock")
		}
		p.tc = tc
		if reason := p.reconfigureReason(dir, tc, presetForDir(p, dir)); reason != "" {
			return fmt.Errorf("--locked: configuration of %s is stale (%s); run `cmk config`", p.relToRoot(dir), reason)
		}
		return nil
	}
	tc, err := p.toolchain()
	if err != nil {
		return err
	}
	preset := presetForDir(p, dir)
	reason := p.reconfigureReason(dir, tc, preset)
	if reason == "" {
		return nil
	}
	fmt.Fprintf(os.Stderr, "cmk: %s; reconfiguring %s\n", reason, dir)
	// Re-apply the ad-hoc args from the last explicit configure: an
	// automatic reconfigure must not silently change the configuration.
	return runConfigure(p, dir, preset, stampExtra(dir))
}

// bootstrapIfUnconfigured configures the selected variant when its build
// dir doesn't exist yet: the default one on a fresh checkout (no build
// dir at all), or the named preset's dir on `cmk build -c <preset>`. The
// variant flag names a preset only in single-config mode; multi-config
// configures every configuration into one dir anyway.
func bootstrapIfUnconfigured(p *Project, buildDirFlag, variant string, policy configurePolicy) error {
	if policy == configureSkip {
		return nil
	}
	if buildDirFlag != "" {
		return nil // an explicit -b dir is never auto-created
	}
	if !p.hasCmkToml() {
		return nil // auto-configure is for declared cmk projects (see ensureConfigured)
	}
	if _, err := os.Stat(filepath.Join(p.Root, "CMakeLists.txt")); err != nil {
		return nil // not a CMake project root; let build-dir resolution error speak
	}
	mc := isMultiConfig(p.Cfg)
	presetSelected := !mc && variant != "" && len(p.Cfg.Configure.Presets) > 0
	if len(p.BuildDirs) > 0 && !presetSelected {
		return nil
	}
	var preset *PresetCfg
	if !mc {
		if variant != "" && len(p.Cfg.Configure.Presets) == 0 {
			return nil // let resolveVariant explain what -c means here
		}
		pr, err := resolvePreset(p.Cfg, variant)
		if err != nil {
			return err
		}
		preset = pr
	}
	dir := defaultConfigureDir(p, preset)
	if _, err := os.Stat(filepath.Join(dir, "CMakeCache.txt")); err == nil {
		return nil // the selected variant is already configured
	}
	if policy == configureLocked {
		return fmt.Errorf("--locked: %s is not configured; run `cmk config` first", p.relToRoot(dir))
	}
	fmt.Fprintf(os.Stderr, "cmk: %s is not configured yet; configuring\n", dir)
	if err := runConfigure(p, dir, preset, nil); err != nil {
		return err
	}
	p.scanBuildDirs()
	return nil
}

// presetForDir reverse-maps a build dir to the [config.preset.*] that
// configures it, so an auto-reconfigure reuses that preset's args. nil
// for multi-config and plain single-config dirs.
func presetForDir(p *Project, dir string) *PresetCfg {
	vars := p.vars()
	for _, name := range presetNames(p.Cfg.Configure.Presets) {
		pr := p.Cfg.Configure.Presets[name]
		d := expandVars(pr.Dir, vars)
		if d == "" {
			continue
		}
		if !filepath.IsAbs(d) {
			d = filepath.Join(p.Root, d)
		}
		if filepath.Clean(d) == filepath.Clean(dir) {
			return pr
		}
	}
	return nil
}

// reconfigureReason reports why dir must be reconfigured, or "" when its
// configuration is current. The baseline is the injection stamp's mtime:
// runConfigure writes it right after cmake succeeds, so any input younger
// than the stamp changed after the last configure.
func (p *Project) reconfigureReason(dir string, tc *Toolchain, preset *PresetCfg) string {
	cacheInfo, err := os.Stat(filepath.Join(dir, "CMakeCache.txt"))
	if err != nil {
		return "build dir is not configured"
	}
	// The recorded ad-hoc args are part of the configuration being
	// checked, not a deviation from it.
	_, stampArgs, err := computeInjection(p, tc, preset, stampExtra(dir))
	if err != nil {
		// e.g. a dep is not synced yet. The configure path syncs deps
		// itself, so don't parrot the error's "run `cmk sync`" advice
		// for work cmk is about to do.
		return strings.TrimSuffix(err.Error(), " (run `cmk sync`)")
	}
	if injectionChanged(dir, stampArgs) {
		return "injected configuration changed"
	}
	stampInfo, err := os.Stat(filepath.Join(dir, injectionStampFile))
	if err != nil {
		return "configure stamp missing"
	}
	stamp := stampInfo.ModTime()
	if cacheInfo.ModTime().After(stamp) {
		warnFutureMtime(filepath.Join(dir, "CMakeCache.txt"), cacheInfo.ModTime())
		return "CMakeCache.txt was modified outside cmk"
	}
	cmkInputs, err := p.cmkInputFiles()
	if err != nil {
		// e.g. a dep's patch glob no longer matches after a deletion:
		// the dep changed, and the configure path's sync will surface
		// the underlying problem.
		return err.Error()
	}
	for _, path := range cmkInputs {
		fi, err := os.Stat(path)
		// cmk inputs may legitimately be absent (no cmk.toml at a git
		// fallback root); only presence + newer mtime is a signal.
		if err == nil && fi.ModTime().After(stamp) {
			warnFutureMtime(path, fi.ModTime())
			return p.relToRoot(path) + " changed"
		}
	}
	files, err := readCMakeFilesReply(dir)
	if err != nil {
		return "cmake file API reply unavailable"
	}
	for _, in := range files.Inputs {
		if in.IsGenerated {
			continue // written by configure itself
		}
		path := filepath.FromSlash(in.Path)
		if !filepath.IsAbs(path) {
			path = filepath.Join(files.Paths.Source, path)
		}
		fi, err := os.Stat(path)
		if err != nil {
			return p.relToRoot(path) + " is gone"
		}
		if fi.ModTime().After(stamp) {
			warnFutureMtime(path, fi.ModTime())
			return p.relToRoot(path) + " changed"
		}
	}
	for _, g := range files.GlobsDependent {
		got, err := evalGlobDependent(g)
		if err != nil || !sortedEqual(got, g.Paths) {
			return "file(GLOB) results changed for " + g.Expression
		}
	}
	return ""
}

// cmkInputFiles are configure inputs CMake knows nothing about: cmk.toml
// itself and each dep's recipe script, patches, and extra_inputs. A dep
// edit must reconfigure so runConfigure re-syncs the store and injects
// the new prefixes. An error (a patch/extra_inputs glob matching nothing,
// typically after a deletion) is itself a dep change and must reconfigure
// rather than be skipped — sync then reports the underlying problem.
func (p *Project) cmkInputFiles() ([]string, error) {
	files := []string{filepath.Join(p.Root, configFileName)}
	for _, name := range sortedDepNames(p.Cfg.Deps) {
		d := p.Cfg.Deps[name]
		files = append(files, filepath.Join(p.Root, d.Script))
		patches, extras, err := depInputs(p.Root, name, d)
		if err != nil {
			return nil, err
		}
		for _, rel := range append(patches, extras...) {
			files = append(files, filepath.Join(p.Root, rel))
		}
	}
	return files, nil
}

// warnFutureMtime flags an input whose timestamp is ahead of the clock
// (clock skew, archives extracted with preserved times): it stays newer
// than any stamp cmk writes, so builds reconfigure on every run until
// the timestamp passes. Reconfiguring is still correct — this only makes
// the loop diagnosable.
func warnFutureMtime(path string, mtime time.Time) {
	if mtime.After(time.Now().Add(2 * time.Second)) {
		fmt.Fprintf(os.Stderr, "cmk: warning: %s is timestamped in the future (%s); every build will reconfigure until then\n",
			path, mtime.Format(time.RFC3339))
	}
}

func (p *Project) relToRoot(path string) string {
	if rel, err := filepath.Rel(p.Root, path); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return path
}

// --- CONFIGURE_DEPENDS glob re-evaluation ---

// evalGlobDependent re-runs a recorded CONFIGURE_DEPENDS glob, mirroring
// CMake's file(GLOB) semantics: wildcards never cross a separator, hidden
// files match, GLOB_RECURSE matches the last pattern segment against file
// names at any depth, and results come back sorted with forward slashes
// (relative when the call used RELATIVE).
func evalGlobDependent(g globDependent) ([]string, error) {
	expr := strings.TrimSuffix(g.Expression, "/")
	if !strings.HasPrefix(expr, "/") {
		return nil, fmt.Errorf("glob expression %q is not absolute", g.Expression)
	}
	segs := strings.Split(strings.TrimPrefix(expr, "/"), "/")
	root := "/"
	for len(segs) > 1 && !hasGlobMeta(segs[0]) {
		root = filepath.Join(root, segs[0])
		segs = segs[1:]
	}
	var matches []string
	if g.Recurse {
		fileSeg := segs[len(segs)-1]
		dirSegs := segs[:len(segs)-1]
		re, err := compileGlobSegment(fileSeg)
		if err != nil {
			return nil, err
		}
		for _, d := range matchDirs(root, dirSegs) {
			globRecurse(d, re, g.ListDirectories, g.FollowSymlinks, &matches)
		}
	} else {
		if err := matchGlob(root, segs, g.ListDirectories, &matches); err != nil {
			return nil, err
		}
	}
	if g.Relative != "" {
		for i, m := range matches {
			rel, err := filepath.Rel(filepath.FromSlash(g.Relative), filepath.FromSlash(m))
			if err != nil {
				return nil, err
			}
			matches[i] = filepath.ToSlash(rel)
		}
	}
	sort.Strings(matches)
	return matches, nil
}

func hasGlobMeta(s string) bool { return strings.ContainsAny(s, "*?[") }

// matchGlob matches the pattern segments under dir: intermediate segments
// must be directories; the last one appends files always and directories
// only with LIST_DIRECTORIES, like cmsys::Glob::ProcessDirectory.
func matchGlob(dir string, segs []string, listDirs bool, out *[]string) error {
	re, err := compileGlobSegment(segs[0])
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil // unreadable dirs match nothing, like cmsys
	}
	last := len(segs) == 1
	for _, e := range entries {
		if !re.MatchString(e.Name()) {
			continue
		}
		full := filepath.Join(dir, e.Name())
		isDir := entryIsDir(e, full)
		if last {
			if !isDir || listDirs {
				*out = append(*out, filepath.ToSlash(full))
			}
		} else if isDir {
			if err := matchGlob(full, segs[1:], listDirs, out); err != nil {
				return err
			}
		}
	}
	return nil
}

// matchDirs expands pattern segments to the directories they match; an
// empty pattern list yields root itself.
func matchDirs(root string, segs []string) []string {
	dirs := []string{root}
	for _, seg := range segs {
		re, err := compileGlobSegment(seg)
		if err != nil {
			return nil
		}
		var next []string
		for _, d := range dirs {
			entries, err := os.ReadDir(d)
			if err != nil {
				continue
			}
			for _, e := range entries {
				if !re.MatchString(e.Name()) {
					continue
				}
				full := filepath.Join(d, e.Name())
				if entryIsDir(e, full) {
					next = append(next, full)
				}
			}
		}
		dirs = next
	}
	return dirs
}

// globRecurse mirrors cmsys::Glob::RecurseDirectory: file names match the
// pattern at any depth; directories are listed only with LIST_DIRECTORIES
// and symlinked directories are traversed only with FOLLOW_SYMLINKS.
func globRecurse(dir string, re *regexp.Regexp, listDirs, follow bool, out *[]string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		full := filepath.Join(dir, e.Name())
		if entryIsDir(e, full) {
			if listDirs && re.MatchString(e.Name()) {
				*out = append(*out, filepath.ToSlash(full))
			}
			if e.Type()&fs.ModeSymlink == 0 || follow {
				globRecurse(full, re, listDirs, follow, out)
			}
		} else if re.MatchString(e.Name()) {
			*out = append(*out, filepath.ToSlash(full))
		}
	}
}

func entryIsDir(e fs.DirEntry, full string) bool {
	if e.IsDir() {
		return true
	}
	if e.Type()&fs.ModeSymlink != 0 {
		fi, err := os.Stat(full)
		return err == nil && fi.IsDir()
	}
	return false
}

// compileGlobSegment converts one path component of a CMake glob into a
// regexp the way cmsys::Glob does: * and ? stay within the component,
// [...] classes pass through ([!...] negates), everything else is
// literal. cmsys matches case-insensitively on macOS and Windows.
func compileGlobSegment(seg string) (*regexp.Regexp, error) {
	var b strings.Builder
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		b.WriteString("(?i)")
	}
	b.WriteString("^")
	for i := 0; i < len(seg); i++ {
		switch c := seg[i]; c {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		case '[':
			j := i + 1
			neg := false
			if j < len(seg) && (seg[j] == '!' || seg[j] == '^') {
				neg = true
				j++
			}
			k := j
			if k < len(seg) && seg[k] == ']' {
				k++ // a ']' right after '[' (or the negation) is literal
			}
			for k < len(seg) && seg[k] != ']' {
				k++
			}
			if k >= len(seg) { // unterminated class: a literal '['
				b.WriteString(regexp.QuoteMeta("["))
				continue
			}
			b.WriteString("[")
			if neg {
				b.WriteString("^")
			}
			b.WriteString(seg[j:k])
			b.WriteString("]")
			i = k
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

func sortedEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	bs := append([]string{}, b...)
	sort.Strings(bs)
	for i := range a {
		if a[i] != bs[i] {
			return false
		}
	}
	return true
}
