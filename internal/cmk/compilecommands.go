package cmk

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
func (p *Project) syncRootCompileCommands(buildDir string, preset *PresetCfg) error {
	sel := p.Cfg.Configure.CompileCommands
	if sel == "" {
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

// filterCompileCommands keeps only the entries a Ninja Multi-Config build
// emitted for the given configuration. Each object path nests the
// configuration as a path segment (…/<target>.dir/<Config>/foo.cc.o), so a
// "/<Config>/" match on the output, command, or arguments selects one
// configuration's entries. Entry
// contents are preserved; the file is re-indented.
func filterCompileCommands(data []byte, config string) ([]byte, error) {
	var raws []json.RawMessage
	if err := json.Unmarshal(data, &raws); err != nil {
		return nil, err
	}
	marker := "/" + config + "/"
	kept := make([]json.RawMessage, 0, len(raws))
	for _, r := range raws {
		var e struct {
			Output    string   `json:"output"`
			Command   string   `json:"command"`
			Arguments []string `json:"arguments"`
		}
		if err := json.Unmarshal(r, &e); err != nil {
			return nil, err
		}
		haystacks := append([]string{e.Output, e.Command}, e.Arguments...)
		matched := false
		for _, haystack := range haystacks {
			haystack = strings.ReplaceAll(haystack, `\`, "/")
			if strings.Contains(haystack, marker) {
				matched = true
				break
			}
		}
		if matched {
			kept = append(kept, r)
		}
	}
	return json.MarshalIndent(kept, "", "  ")
}

// lintCompilationDatabase returns a directory containing exactly the compile
// commands for the configuration clang-tidy should inspect. Single-config
// builds use CMake's database directly. Multi-config builds get a stable,
// filtered database under the build tree so clang-tidy does not run once for
// every configuration recorded for a translation unit.
func (p *Project) lintCompilationDatabase(buildDir string, preset *PresetCfg, resolvedConfig string) (string, string, error) {
	config, multi, err := p.lintConfiguration(buildDir, preset, resolvedConfig)
	if err != nil {
		return "", "", err
	}
	if !multi {
		return buildDir, "", nil
	}

	source := filepath.Join(buildDir, "compile_commands.json")
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
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	destination := filepath.Join(dir, "compile_commands.json")
	if current, readErr := os.ReadFile(destination); readErr != nil || !bytes.Equal(current, filtered) {
		if err := os.WriteFile(destination, filtered, 0o644); err != nil {
			return "", "", err
		}
	}
	return dir, config, nil
}

func (p *Project) lintConfiguration(buildDir string, preset *PresetCfg, resolved string) (string, bool, error) {
	if p.hasCmkConfig() {
		if !isMultiConfig(p.Cfg, preset) {
			return "", false, nil
		}
		return resolved, true, nil
	}

	cache, err := readCMakeCache(filepath.Join(buildDir, "CMakeCache.txt"))
	if err != nil {
		return "", false, err
	}
	if !isMultiConfigGenerator(cache["CMAKE_GENERATOR"]) {
		return "", false, nil
	}
	configurations := strings.FieldsFunc(cache["CMAKE_CONFIGURATION_TYPES"], func(r rune) bool { return r == ';' })
	selected := resolved
	if selected == "" {
		selected = cache["CMAKE_DEFAULT_BUILD_TYPE"]
	}
	if selected == "" && len(configurations) > 0 {
		selected = configurations[0]
	}
	if selected == "" {
		return "", true, fmt.Errorf("cannot select a configuration from %s", filepath.Join(buildDir, "CMakeCache.txt"))
	}
	if len(configurations) > 0 {
		known := false
		for _, configuration := range configurations {
			known = known || configuration == selected
		}
		if !known {
			return "", true, fmt.Errorf("configuration %q not found (known: %s)", selected, strings.Join(configurations, ", "))
		}
	}
	return selected, true, nil
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
