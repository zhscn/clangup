package cmk

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// syncRootCompileCommands refreshes the project-root compile_commands.json
// from the database CMake exported into buildDir, honoring
// cmake.compile-commands. It is a no-op when that setting is empty or
// when buildDir has no database yet (configure not run, or export disabled).
//
// clangd resolves a source file's compile command from the nearest
// compile_commands.json walking up from the file, so a root-level copy takes
// precedence over build/compile_commands.json. In multi-config that build
// database carries one entry per file *per configuration*, and clangd would
// use whichever comes first (the first configuration, not the default);
// narrowing the root copy to a single configuration fixes that.
func (t *buildTree) syncRootCompileCommands() error {
	p, buildDir, preset := t.p, t.dir, t.preset()
	sel := p.Cfg.Configure.CompileCommands
	if sel == "" || sel == "none" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(buildDir, "compile_commands.json"))
	if err != nil {
		return nil // not configured yet, or export disabled — nothing to mirror
	}

	out := data
	if isMultiConfig(p.Cfg, preset) {
		cfg := sel
		if cfg == "default" {
			if cfg, err = p.resolveConfig(preset, ""); err != nil {
				return err
			}
		}
		if out, err = filterCompileCommands(data, cfg); err != nil {
			return fmt.Errorf("filtering compile_commands.json to %s: %w", cfg, err)
		}
	} else if hasSyntheticBMIEntries(data) {
		if out, err = filterCompileCommands(data, ""); err != nil {
			return fmt.Errorf("filtering synthetic BMI entries from compile_commands.json: %w", err)
		}
	}

	dst := filepath.Join(p.Root, "compile_commands.json")
	if fi, lerr := os.Lstat(dst); lerr == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			// Often a symlink into the build dir; writing through it would
			// clobber CMake's own database. Drop it for a real file.
			os.Remove(dst)
		} else if cur, rerr := os.ReadFile(dst); rerr == nil && bytes.Equal(cur, out) {
			// Unchanged — leave the file (and its mtime) alone so clangd
			// doesn't re-parse every translation unit on a no-op reconfigure.
			return nil
		}
	}
	if err := os.WriteFile(dst, out, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "cmk: wrote %s\n", dst)
	return nil
}

// filterCompileCommands narrows compile_commands.json to the entries clangd and
// clang-tidy should consume: when config is non-empty, only entries for that
// configuration are kept; in all modes, CMake's synthetic `--precompile` BMI
// rules (`@synth_*.dir/*.bmi`) are dropped so a module interface unit does not
// appear twice alongside its `.cppm.o` compilation entry.
func filterCompileCommands(data []byte, config string) ([]byte, error) {
	var raws []json.RawMessage
	if err := json.Unmarshal(data, &raws); err != nil {
		return nil, err
	}
	kept := make([]json.RawMessage, 0, len(raws))
	for _, r := range raws {
		var e compileCommandEntry
		if err := json.Unmarshal(r, &e); err != nil {
			return nil, err
		}
		if e.isSyntheticBMI() {
			continue
		}
		if config == "" || e.matchesConfig(config) {
			kept = append(kept, r)
		}
	}
	return json.MarshalIndent(kept, "", "  ")
}

func hasSyntheticBMIEntries(data []byte) bool {
	var entries []compileCommandEntry
	if json.Unmarshal(data, &entries) != nil {
		return false
	}
	for i := range entries {
		if entries[i].isSyntheticBMI() {
			return true
		}
	}
	return false
}

type compileCommandEntry struct {
	Directory string   `json:"directory"`
	File      string   `json:"file"`
	Output    string   `json:"output"`
	Command   string   `json:"command"`
	Arguments []string `json:"arguments"`
}

func (e *compileCommandEntry) tokens() []string {
	if len(e.Arguments) > 0 {
		return e.Arguments
	}
	if e.Command != "" {
		return splitCommandTokens(e.Command)
	}
	return nil
}

func (e *compileCommandEntry) isSyntheticBMI() bool {
	for _, out := range e.outputPaths() {
		slash := strings.ReplaceAll(out, `\`, "/")
		if strings.HasSuffix(slash, ".bmi") || strings.HasSuffix(slash, ".ifc") || strings.Contains(slash, "@synth_") {
			return true
		}
	}
	return slices.Contains(e.tokens(), "--precompile")
}

func (e *compileCommandEntry) resolvedFile(fallbackDir string) string {
	if e.File == "" {
		return ""
	}
	if filepath.IsAbs(e.File) {
		return filepath.Clean(e.File)
	}
	base := e.Directory
	if base == "" {
		base = fallbackDir
	}
	return filepath.Clean(filepath.Join(base, e.File))
}

func (e *compileCommandEntry) modmapFiles(fallbackDir string) []string {
	base := e.Directory
	if base == "" {
		base = fallbackDir
	}
	var out []string
	for _, tok := range e.tokens() {
		if !strings.HasPrefix(tok, "@") || !strings.HasSuffix(tok, ".modmap") {
			continue
		}
		p := strings.TrimPrefix(tok, "@")
		if !filepath.IsAbs(p) {
			p = filepath.Join(base, filepath.FromSlash(p))
		}
		out = append(out, filepath.Clean(p))
	}
	return out
}

func (e *compileCommandEntry) matchesConfig(config string) bool {
	if intdir := e.cmakeIntDir(); intdir != "" {
		return intdir == config
	}
	for _, out := range e.outputPaths() {
		if matchesOutputConfig(out, e.Directory, config) {
			return true
		}
	}
	return false
}

func (e *compileCommandEntry) cmakeIntDir() string {
	for _, arg := range e.Arguments {
		if val, ok := parseCMakeIntDirArg(arg); ok {
			return val
		}
	}
	for _, token := range splitCommandTokens(e.Command) {
		if val, ok := parseCMakeIntDirArg(token); ok {
			return val
		}
	}
	return ""
}

func parseCMakeIntDirArg(arg string) (string, bool) {
	const prefix = "-DCMAKE_INTDIR="
	if !strings.HasPrefix(arg, prefix) {
		return "", false
	}
	val := strings.TrimPrefix(arg, prefix)
	val = strings.Trim(val, `"'`)
	return val, val != ""
}

func (e *compileCommandEntry) outputPaths() []string {
	var outs []string
	if e.Output != "" {
		outs = append(outs, e.Output)
	}
	outs = append(outs, extractDashO(e.Arguments)...)
	if len(e.Arguments) == 0 && e.Command != "" {
		outs = append(outs, extractDashO(splitCommandTokens(e.Command))...)
	}
	return outs
}

func extractDashO(args []string) []string {
	var outs []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "-o" && i+1 < len(args) {
			outs = append(outs, args[i+1])
			i++
		} else if strings.HasPrefix(arg, "-o") && len(arg) > 2 {
			outs = append(outs, arg[2:])
		}
	}
	return outs
}

// splitCommandTokens splits a compile_commands.json shell command string into
// unquoted tokens, honoring single quotes, double quotes, and backslash escapes.
func splitCommandTokens(cmd string) []string {
	var tokens []string
	var cur strings.Builder
	inToken := false
	quote := byte(0)
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		if quote != 0 {
			if c == '\\' && quote == '"' && i+1 < len(cmd) {
				i++
				cur.WriteByte(cmd[i])
			} else if c == quote {
				quote = 0
			} else {
				cur.WriteByte(c)
			}
			continue
		}
		switch c {
		case ' ', '\t', '\n', '\r':
			if inToken {
				tokens = append(tokens, cur.String())
				cur.Reset()
				inToken = false
			}
		case '\'', '"':
			quote = c
			inToken = true
		case '\\':
			inToken = true
			if i+1 < len(cmd) {
				i++
				cur.WriteByte(cmd[i])
			}
		default:
			inToken = true
			cur.WriteByte(c)
		}
	}
	if inToken {
		tokens = append(tokens, cur.String())
	}
	return tokens
}

func matchesOutputConfig(out, directory, config string) bool {
	p := strings.ReplaceAll(out, `\`, "/")
	dir := strings.TrimSuffix(strings.ReplaceAll(directory, `\`, "/"), "/")
	rel := p
	hasRel := false
	if dir != "" && strings.HasPrefix(p, dir+"/") {
		rel = strings.TrimPrefix(p, dir+"/")
		hasRel = true
	} else if !strings.HasPrefix(p, "/") && (len(p) < 2 || p[1] != ':') {
		hasRel = true
	}
	// CMake Ninja Multi-Config layout: [subdir/]CMakeFiles/<target>.dir/<Config>/...
	if idx := strings.Index(rel, "CMakeFiles/"); idx >= 0 {
		rest := rel[idx+len("CMakeFiles/"):]
		if _, afterDir, ok := strings.Cut(rest, ".dir/"); ok {
			return strings.HasPrefix(afterDir, config+"/")
		}
	}
	if hasRel {
		return strings.HasPrefix(rel, config+"/") || strings.Contains(rel, "/"+config+"/")
	}
	return false
}

// lintCompilationDatabase returns a directory containing exactly the compile
// commands for the configuration clang-tidy should inspect. Single-config
// builds use CMake's database directly. Multi-config builds get a stable,
// filtered database under the build tree so clang-tidy does not run once for
// every configuration recorded for a translation unit.
func (t *buildTree) lintCompilationDatabase() (string, string, error) {
	buildDir := t.dir
	config, multi := t.lintConfiguration()
	source := filepath.Join(buildDir, "compile_commands.json")
	if !multi {
		data, err := os.ReadFile(source)
		if err != nil {
			return "", "", err
		}
		if !hasSyntheticBMIEntries(data) {
			return buildDir, "", nil
		}
		filtered, err := filterCompileCommands(data, "")
		if err != nil {
			return "", "", fmt.Errorf("filtering synthetic BMI entries from compile_commands.json: %w", err)
		}
		dir := filepath.Join(buildDir, ".cmk", "lint", "default")
		if err := writeIfChanged(filepath.Join(dir, "compile_commands.json"), filtered); err != nil {
			return "", "", err
		}
		return dir, "", nil
	}

	data, err := os.ReadFile(source)
	if err != nil {
		return "", "", err
	}
	filtered, err := filterCompileCommands(data, config)
	if err != nil {
		return "", "", fmt.Errorf("filtering compile_commands.json to %s: %w", config, err)
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(filtered, &entries); err != nil {
		return "", "", err
	}
	if len(entries) == 0 {
		return "", "", fmt.Errorf("no compile commands for configuration %q in %s", config, source)
	}

	component := "config-" + strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || strings.ContainsRune("._-", r) {
			return r
		}
		return '_'
	}, config)
	dir := filepath.Join(buildDir, ".cmk", "lint", component)
	if err := writeIfChanged(filepath.Join(dir, "compile_commands.json"), filtered); err != nil {
		return "", "", err
	}
	return dir, config, nil
}

func writeIfChanged(destination string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	if current, err := os.ReadFile(destination); err == nil && bytes.Equal(current, content) {
		return nil
	}
	return os.WriteFile(destination, content, 0o644)
}

// prepareLintModuleDeps materializes CMake's `@*.modmap` response files and
// any referenced `-fmodule-file=` BMIs via Ninja before clang-tidy runs,
// without compiling object files or linking targets. Non-Ninja trees and
// projects without module response files return immediately.
func (t *buildTree) prepareLintModuleDeps(lintDBDir string, selectedFiles []string, jobs int, verbose bool) error {
	if !fileExists(filepath.Join(t.dir, "build.ninja")) {
		return nil
	}
	ninjaBin := t.p.ninjaBin()
	if !filepath.IsAbs(ninjaBin) {
		if _, err := exec.LookPath(ninjaBin); err != nil {
			return nil
		}
	}
	data, err := os.ReadFile(filepath.Join(lintDBDir, "compile_commands.json"))
	if err != nil {
		return nil
	}
	var entries []compileCommandEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil
	}

	inspectAll := false
	selectedSet := make(map[string]bool, len(selectedFiles))
	for _, f := range selectedFiles {
		clean := filepath.Clean(f)
		selectedSet[clean] = true
		if cppHeaderExts[filepath.Ext(clean)] {
			inspectAll = true
		}
	}

	type modmapRef struct {
		entryDir string
		absPath  string
		target   string
	}
	var modmaps []modmapRef
	seenModmaps := map[string]bool{}
	for i := range entries {
		e := &entries[i]
		if !inspectAll && !selectedSet[e.resolvedFile(t.dir)] {
			continue
		}
		entryDir := e.Directory
		if entryDir == "" {
			entryDir = t.dir
		}
		for _, abs := range e.modmapFiles(t.dir) {
			target, ok := ninjaTargetInDir(t.dir, abs)
			if !ok || seenModmaps[target] {
				continue
			}
			seenModmaps[target] = true
			modmaps = append(modmaps, modmapRef{entryDir: entryDir, absPath: abs, target: target})
		}
	}
	if len(modmaps) == 0 {
		return nil
	}

	modmapTargets := make([]string, len(modmaps))
	for i, m := range modmaps {
		modmapTargets[i] = m.target
	}
	if err := t.runNinjaTargets(modmapTargets, jobs, verbose); err != nil {
		return fmt.Errorf("preparing C++ module modmaps: %w", err)
	}

	var bmiTargets []string
	seenBMIs := map[string]bool{}
	for _, m := range modmaps {
		for _, bmi := range bmiTargetsFromModmap(t.dir, m.entryDir, m.absPath) {
			if !seenBMIs[bmi] {
				seenBMIs[bmi] = true
				bmiTargets = append(bmiTargets, bmi)
			}
		}
	}
	if len(bmiTargets) > 0 {
		if err := t.runNinjaTargets(bmiTargets, jobs, verbose); err != nil {
			return fmt.Errorf("preparing C++ module BMIs: %w", err)
		}
	}
	return nil
}

func ninjaTargetInDir(buildDir, absPath string) (string, bool) {
	rel, err := filepath.Rel(buildDir, absPath)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

func bmiTargetsFromModmap(buildDir, entryDir, modmapPath string) []string {
	data, err := os.ReadFile(modmapPath)
	if err != nil {
		return nil
	}
	var targets []string
	for _, tok := range splitCommandTokens(string(data)) {
		if !strings.HasPrefix(tok, "-fmodule-file=") {
			continue
		}
		rest := strings.TrimPrefix(tok, "-fmodule-file=")
		bmiPath := rest
		if _, p, ok := strings.Cut(rest, "="); ok {
			bmiPath = p
		}
		bmiPath = strings.Trim(bmiPath, `"'`)
		if bmiPath == "" {
			continue
		}
		if !filepath.IsAbs(bmiPath) {
			bmiPath = filepath.Join(entryDir, filepath.FromSlash(bmiPath))
		}
		if target, ok := ninjaTargetInDir(buildDir, filepath.Clean(bmiPath)); ok {
			targets = append(targets, target)
		}
	}
	return targets
}

func (t *buildTree) runNinjaTargets(targets []string, jobs int, verbose bool) error {
	env, err := t.p.buildEnv()
	if err != nil {
		return err
	}
	args := []string{"-C", t.dir}
	if t.config != "" {
		args = append(args, "-f", "build-"+t.config+".ninja")
	}
	args = append(args, "-j", fmt.Sprint(jobs))
	args = append(args, targets...)
	ninjaBin := t.p.ninjaBin()
	if verbose {
		return runStreaming(env, ninjaBin, args...)
	}
	cmd := exec.Command(ninjaBin, args...)
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// lintConfiguration reports the configuration whose compile commands
// clang-tidy should read, and whether the tree is multi-config at all. In a
// foreign tree resolveVariant already resolved it against the cache, and it
// is non-empty exactly for multi-config trees.
func (t *buildTree) lintConfiguration() (string, bool) {
	if t.foreign {
		return t.config, t.config != ""
	}
	if !isMultiConfig(t.p.Cfg, t.preset()) {
		return "", false
	}
	return t.config, true
}

func readCMakeCache(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	values := map[string]string{}
	for line := range strings.Lines(string(data)) {
		declaration, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || strings.HasPrefix(declaration, "//") || strings.HasPrefix(declaration, "#") {
			continue
		}
		name, _, ok := strings.Cut(declaration, ":")
		if ok {
			values[name] = value
		}
	}
	return values, nil
}
