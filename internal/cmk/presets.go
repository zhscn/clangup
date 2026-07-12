package cmk

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// presetPrefix namespaces every cmk-generated preset name. A project may
// ship its own CMakePresets.json, and CMakeUserPresets.json implicitly
// includes it — duplicate names across the two are a fatal parse error
// that breaks *all* `cmake --preset` (including the project's own). The
// prefix keeps cmk's presets collision-free; the bare, friendly name
// lives in displayName.
const presetPrefix = "cmk-"

type configurePreset struct {
	Name           string            `json:"name"`
	DisplayName    string            `json:"displayName"`
	Generator      string            `json:"generator"`
	BinaryDir      string            `json:"binaryDir"`
	CacheVariables map[string]string `json:"cacheVariables"`
	Environment    map[string]string `json:"environment,omitempty"`
}
type buildPreset struct {
	Name            string `json:"name"`
	ConfigurePreset string `json:"configurePreset"`
	Configuration   string `json:"configuration,omitempty"`
}
type testPreset struct {
	Name            string `json:"name"`
	ConfigurePreset string `json:"configurePreset"`
	Configuration   string `json:"configuration,omitempty"`
}
type presetsFile struct {
	Version          int               `json:"version"`
	ConfigurePresets []configurePreset `json:"configurePresets"`
	BuildPresets     []buildPreset     `json:"buildPresets"`
	TestPresets      []testPreset      `json:"testPresets"`
	Vendor           map[string]any    `json:"vendor"`
}

// writeUserPresets generates CMakeUserPresets.json mirroring cmk's
// injection, so IDEs and plain CMake presets reproduce the same
// configuration without cmk in the loop. Multi-config projects get one
// configure preset plus build/test presets per configuration; single-config
// preset mode gets one configure/build/test preset set per [config.preset].
// The file is
// machine-local (it embeds .deps paths relocated to ${sourceDir}) and
// belongs in .gitignore.
//
// A file cmk authored before (it carries the vendor marker) is rewritten
// wholesale. A file the user owns is merged into instead — cmk's
// namespaced presets are refreshed in place and everything else is kept
// (see finalizeUserPresets).
func writeUserPresets(p *Project, tc *Toolchain) error {
	path := filepath.Join(p.Root, "CMakeUserPresets.json")
	existing, _ := os.ReadFile(path)
	merge := false
	if len(existing) > 0 {
		var probe struct {
			Vendor map[string]any `json:"vendor"`
		}
		if json.Unmarshal(existing, &probe) != nil {
			fmt.Fprintf(os.Stderr, "cmk: warning: %s is not valid JSON; leaving it alone\n", path)
			return nil
		}
		merge = probe.Vendor["cmk"] == nil
	}

	gen := p.Cfg.Configure.Generator
	if gen == "" {
		gen = "Ninja"
	}
	vars := p.vars()

	// Mirror the toolchain env so IDE / `cmake --preset` configures run
	// with the same CC/CXX as cmk config — sub-builds that detect the
	// compiler from the environment (vcpkg ports) then stay consistent.
	// ${sourceDir} keeps CCACHE_BASEDIR worktree-relative, so `cmake
	// --preset` reuse matches cmk's.
	presetEnv := map[string]string{}
	if tc.Root != "" {
		presetEnv["CC"], presetEnv["CXX"] = tc.CC, tc.CXX
	}
	if filepath.Base(p.Cfg.Configure.CompilerLauncher) == "ccache" {
		presetEnv["CCACHE_BASEDIR"] = "${sourceDir}"
		presetEnv["CCACHE_NOHASHDIR"] = "true"
	}
	if len(presetEnv) == 0 {
		presetEnv = nil
	}

	// The presets mirror the same injection cmk's own configure uses —
	// one source of truth (injectionParts), two projections. parts.argvOnly
	// (CMAKE_SUPPRESS_REGENERATION) is deliberately not mirrored: it
	// belongs to cmk-managed build dirs where ensureConfigured runs the
	// staleness checks; IDE-driven configures keep CMake's regen rules.
	parts, err := p.injectionParts(tc, nil, nil)
	if err != nil {
		return err
	}
	base := map[string]string{}
	addDefines(base, parts.common)
	addDefines(base, parts.exports)
	addDefines(base, parts.launcher)
	addDefines(base, parts.userArgs)
	cfgArgs := parts.userArgs

	relocate := func(s string) string {
		return strings.ReplaceAll(s, p.Root+string(filepath.Separator), "${sourceDir}/")
	}

	out := presetsFile{
		Version: 4, // requires cmake >= 3.23
		Vendor:  map[string]any{"cmk": map[string]any{"generated": true}},
	}

	if isMultiConfig(p.Cfg) {
		cache := map[string]string{}
		addDefines(cache, tc.cmakeArgs(cfgArgs))
		addDefines(cache, parts.multiConfig)
		for k, v := range base {
			cache[k] = relocate(v)
		}
		// Relocate the flags include too: parts carries the absolute
		// path, presets want it worktree-relative.
		if v, ok := cache["CMAKE_PROJECT_INCLUDE"]; ok {
			cache["CMAKE_PROJECT_INCLUDE"] = relocate(v)
		}
		cfgs := effectiveConfigurations(p.Cfg)
		dir := expandVars(p.multiConfigDir(), vars)
		if !filepath.IsAbs(dir) {
			dir = "${sourceDir}/" + filepath.ToSlash(dir)
		}
		cfgPreset := presetPrefix + "default"
		out.ConfigurePresets = append(out.ConfigurePresets, configurePreset{
			Name:           cfgPreset,
			DisplayName:    "default (cmk)",
			Generator:      gen,
			BinaryDir:      dir,
			CacheVariables: cache,
			Environment:    presetEnv,
		})
		for _, c := range cfgs {
			out.BuildPresets = append(out.BuildPresets, buildPreset{
				Name:            presetPrefix + c,
				ConfigurePreset: cfgPreset,
				Configuration:   c,
			})
			out.TestPresets = append(out.TestPresets, testPreset{
				Name:            presetPrefix + c,
				ConfigurePreset: cfgPreset,
				Configuration:   c,
			})
		}
		return finalizeUserPresets(path, merge, existing, out)
	}

	presets := p.Cfg.Configure.Presets
	names := presetNames(presets)
	if len(names) == 0 {
		presets = map[string]*PresetCfg{"default": {Dir: "build"}}
		names = []string{"default"}
	}

	for _, name := range names {
		pr := presets[name]
		prArgs := expandAll(pr.Args, vars)
		cache := map[string]string{}
		// Same toolchain-file-vs-compiler-vars decision as cmk config,
		// per preset (a preset may bring its own CMAKE_TOOLCHAIN_FILE).
		addDefines(cache, tc.cmakeArgs(append(append([]string{}, cfgArgs...), prArgs...)))
		for k, v := range base {
			cache[k] = relocate(v)
		}
		extra := map[string]string{}
		addDefines(extra, prArgs)
		for k, v := range extra {
			cache[k] = relocate(v)
		}
		dir := pr.Dir
		if dir == "" {
			dir = "build"
		}
		dir = expandVars(dir, vars)
		if !filepath.IsAbs(dir) {
			dir = "${sourceDir}/" + filepath.ToSlash(dir)
		}
		prName := presetPrefix + name
		out.ConfigurePresets = append(out.ConfigurePresets, configurePreset{
			Name:           prName,
			DisplayName:    name + " (cmk)",
			Generator:      gen,
			BinaryDir:      dir,
			CacheVariables: cache,
			Environment:    presetEnv,
		})
		out.BuildPresets = append(out.BuildPresets, buildPreset{Name: prName, ConfigurePreset: prName})
		out.TestPresets = append(out.TestPresets, testPreset{Name: prName, ConfigurePreset: prName})
	}

	return finalizeUserPresets(path, merge, existing, out)
}

// finalizeUserPresets writes out to path. A cmk-owned or brand-new file is
// written wholesale (carrying cmk's vendor marker). A user-owned file is
// merged into via mergeUserPresets, which preserves the user's content and
// deliberately does NOT stamp the cmk marker — so the file keeps being
// merged rather than overwritten on the next `cmk config`.
func finalizeUserPresets(path string, merge bool, existing []byte, out presetsFile) error {
	var data []byte
	var err error
	if merge {
		if data, err = mergeUserPresets(existing, out); err != nil {
			return fmt.Errorf("merging into %s: %w", path, err)
		}
		fmt.Fprintf(os.Stderr, "cmk: merged cmk-* presets into existing %s\n", filepath.Base(path))
	} else if data, err = json.MarshalIndent(out, "", "  "); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// mergeUserPresets folds cmk's generated presets (out) into an existing,
// user-authored CMakeUserPresets.json, preserving every key cmk doesn't
// own (the user's own presets, include, vendor, workflow/package presets).
// cmk's presets are all presetPrefix-namespaced, so each array drops the
// prior cmk-* entries and appends the fresh ones. version is only raised,
// never lowered (a user on a newer schema keeps it).
func mergeUserPresets(existing []byte, out presetsFile) ([]byte, error) {
	var doc map[string]any
	if err := json.Unmarshal(existing, &doc); err != nil {
		return nil, err
	}
	if v, _ := doc["version"].(float64); int(v) < out.Version {
		doc["version"] = out.Version
	}
	doc["configurePresets"] = mergePresetList(doc["configurePresets"], out.ConfigurePresets)
	doc["buildPresets"] = mergePresetList(doc["buildPresets"], out.BuildPresets)
	doc["testPresets"] = mergePresetList(doc["testPresets"], out.TestPresets)
	return json.MarshalIndent(doc, "", "  ")
}

// mergePresetList merges cmk's generated presets into one existing preset
// array. It drops every cmk-namespaced entry (stale, or about to be
// re-added) and collapses pre-existing duplicate names, then appends cmk's
// fresh (internally unique) presets — so the result can never carry a
// duplicate preset name, which CMake rejects at parse time.
func mergePresetList[T any](existing any, generated []T) []any {
	regen := map[string]bool{}
	for _, g := range generated {
		regen[presetItemName(g)] = true
	}
	merged := []any{}
	seen := map[string]bool{}
	if arr, ok := existing.([]any); ok {
		for _, it := range arr {
			name := presetItemName(it)
			if strings.HasPrefix(name, presetPrefix) || regen[name] {
				continue // cmk-owned or being regenerated → drop
			}
			if name != "" {
				if seen[name] {
					continue // collapse a duplicate already in the user's file
				}
				seen[name] = true
			}
			merged = append(merged, it)
		}
	}
	for _, g := range generated {
		merged = append(merged, g)
	}
	return merged
}

// presetItemName reads the "name" of a preset, whether it's a decoded
// map[string]any from the existing file or one of cmk's typed structs.
func presetItemName(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	var m struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(b, &m) != nil {
		return ""
	}
	return m.Name
}

// notedNonDefines dedupes the not-representable notes below: addDefines
// runs once per generated preset, and repeating the same note for every
// preset is noise.
var notedNonDefines = map[string]bool{}

// addDefines folds -DNAME[=:TYPE]=VALUE args into dst. Other arg forms
// can't be represented as preset cache variables; they still apply to
// cmk's own configure, so just note the skip (once per arg).
func addDefines(dst map[string]string, args []string) {
	for _, a := range args {
		if !strings.HasPrefix(a, "-D") {
			if !notedNonDefines[a] {
				notedNonDefines[a] = true
				fmt.Fprintf(os.Stderr, "cmk: note: %q is not representable in CMakeUserPresets.json\n", a)
			}
			continue
		}
		k, v, ok := strings.Cut(a[2:], "=")
		if !ok {
			continue
		}
		if i := strings.IndexByte(k, ':'); i >= 0 {
			k = k[:i]
		}
		dst[k] = v
	}
}
