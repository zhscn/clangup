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
		hay := e.Output
		if hay == "" {
			hay = e.Command
		}
		if hay == "" {
			hay = strings.Join(e.Arguments, " ")
		}
		if strings.Contains(hay, marker) {
			kept = append(kept, r)
		}
	}
	return json.MarshalIndent(kept, "", "  ")
}
