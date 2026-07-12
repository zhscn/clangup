package cmk

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// configFlagsFileRel is the project-relative path of the generated CMake
// include that defines the per-configuration flag edits. It lives under
// .cache/ (gitignored) and is regenerated from cmk.toml by every
// `cmk config`.
const configFlagsFileRel = ".cache/cmk/configurations.cmake"

var configNameRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

// standardConfigs are CMake's default CMAKE_CONFIGURATION_TYPES, used when
// a multi-config project doesn't list its own.
var standardConfigs = []string{"Debug", "Release", "RelWithDebInfo", "MinSizeRel"}

// isMultiConfig reports whether the configured generator is a CMake
// multi-config generator. Only "Ninja Multi-Config" is exercised, but the
// substring also matches the Xcode / Visual Studio families, which behave
// the same way (one build tree, --config picks the configuration).
func isMultiConfig(cfg *Config) bool {
	return strings.Contains(cfg.Configure.Generator, "Multi-Config") ||
		strings.HasPrefix(cfg.Configure.Generator, "Visual Studio") ||
		cfg.Configure.Generator == "Xcode"
}

func isStandardConfig(name string) bool {
	for _, c := range standardConfigs {
		if strings.EqualFold(c, name) {
			return true
		}
	}
	return false
}

// effectiveConfigurations is the ordered CMAKE_CONFIGURATION_TYPES list:
// the explicit [config] configurations (or the CMake standard four when
// unset), plus any [config.configuration.*] names not already present
// (defining or editing a configuration auto-enables it), appended
// deterministically. The default is validated against this list at load
// time rather than auto-added, so a typo is caught instead of silently
// creating a new configuration.
func effectiveConfigurations(cfg *Config) []string {
	list := append([]string{}, cfg.Configure.Configurations...)
	if len(list) == 0 {
		list = append(list, standardConfigs...)
	}
	seen := map[string]bool{}
	for _, c := range list {
		seen[c] = true
	}
	extra := make([]string, 0, len(cfg.Configure.Configuration))
	for name := range cfg.Configure.Configuration {
		if !seen[name] {
			extra = append(extra, name)
			seen[name] = true
		}
	}
	sort.Strings(extra)
	return append(list, extra...)
}

// multiConfigDir is the single build directory used in multi-config mode
// ([config] dir, default "build"), relative unless explicitly absolute.
func (p *Project) multiConfigDir() string {
	dir := p.Cfg.Configure.Dir
	if dir == "" {
		dir = "build"
	}
	return expandVars(dir, p.vars())
}

// resolveConfig picks the configuration for a build-time command in
// multi-config mode: explicit -c, then [config] default, then the first
// configuration (which is what a bare `ninja` would build). It returns ""
// for single-config generators, where --config is not used.
func (p *Project) resolveConfig(explicit string) (string, error) {
	if !isMultiConfig(p.Cfg) {
		if explicit != "" {
			return "", fmt.Errorf("--config %q only applies with a multi-config generator "+
				"(set [config] generator = \"Ninja Multi-Config\")", explicit)
		}
		return "", nil
	}
	cfgs := effectiveConfigurations(p.Cfg)
	if explicit != "" {
		for _, c := range cfgs {
			if c == explicit {
				return explicit, nil
			}
		}
		return "", fmt.Errorf("configuration %q not found (known: %s)", explicit, strings.Join(cfgs, ", "))
	}
	if d := p.Cfg.Configure.Default; d != "" {
		return d, nil
	}
	return cfgs[0], nil
}

// resolveVariant resolves a build-time command's target tree and configuration
// from the -b (build dir) and -c (variant) flags, unifying variant selection
// across the two project models so `cmk build -c <name>` works the same way in
// both:
//
//   - multi-config: -c names a CMAKE_CONFIGURATION_TYPES configuration, returned
//     as config and passed to cmake as --config; -b selects the single build
//     tree (rarely needed).
//   - presets / plain single-config: -c names a [config.preset.*] and resolves
//     to that preset's build dir; --config does not apply, so config is "".
//
// So `cmk build -c debug` selects the debug preset just as `cmk build -c Asan`
// selects the Asan configuration, and both mirror `cmk config <name>`.
func (p *Project) resolveVariant(buildDir, variant string) (dir, config string, err error) {
	if isMultiConfig(p.Cfg) {
		if dir, err = p.resolveBuildDir(buildDir); err != nil {
			return "", "", err
		}
		config, err = p.resolveConfig(variant)
		return dir, config, err
	}
	// Single-config: -c names a preset, resolved to its build dir.
	if variant != "" {
		if len(p.Cfg.Configure.Presets) == 0 {
			return "", "", fmt.Errorf("-c %q: nothing to select — this project is single-config with no "+
				"[config.preset.*] (multi-config selects a configuration; presets select a build dir)", variant)
		}
		if buildDir != "" {
			return "", "", fmt.Errorf("pass either -b <dir> or -c <preset>, not both")
		}
		pr, perr := resolvePreset(p.Cfg, variant)
		if perr != nil {
			return "", "", perr
		}
		buildDir = filepath.Clean(expandVars(pr.Dir, p.vars()))
	}
	dir, err = p.resolveBuildDir(buildDir)
	return dir, "", err
}

// configFlagsFile computes the CMake include carrying the per-configuration
// flag edits from [config.configuration.*]: its absolute path and content
// (content "" means there is nothing to include and the file should not
// exist). Pure — writeConfigFlagsFile materializes it — so the staleness
// check can hash the would-be content without touching the working tree.
//
// cmk config injects the file via CMAKE_PROJECT_INCLUDE so the cache flags
// are set after project() has enabled languages and initialized the base
// configuration flags. That ordering is what lets `inherits` reference them
// as ${CMAKE_CXX_FLAGS_<BASE>}. The hook can run for nested project()
// calls, so the generated content is deliberately idempotent.
func configFlagsFile(p *Project) (path, content string) {
	custom := p.Cfg.Configure.Configuration
	path = filepath.Join(p.Root, filepath.FromSlash(configFlagsFileRel))
	if !isMultiConfig(p.Cfg) || len(custom) == 0 {
		return path, ""
	}
	vars := p.vars()

	names := make([]string, 0, len(custom))
	for n := range custom {
		names = append(names, n)
	}
	sort.Strings(names)

	var b strings.Builder
	b.WriteString("# Generated by cmk from cmk.toml — do not edit.\n")
	b.WriteString("# Per-configuration flag edits for the multi-config generator.\n\n")
	if hasAppendConfigFlags(custom) {
		b.WriteString("function(cmk_append_config_flag var suffix desc)\n")
		b.WriteString("  if(\"${suffix}\" STREQUAL \"\")\n")
		b.WriteString("    return()\n")
		b.WriteString("  endif()\n")
		b.WriteString("  set(orig_var \"CMK_ORIGINAL_${var}\")\n")
		b.WriteString("  if(NOT DEFINED ${orig_var})\n")
		b.WriteString("    set(${orig_var} \"${${var}}\" CACHE INTERNAL \"cmk: original ${var}\")\n")
		b.WriteString("  endif()\n")
		b.WriteString("  string(STRIP \"${${orig_var}} ${suffix}\" value)\n")
		b.WriteString("  set(${var} \"${value}\" CACHE STRING \"${desc}\" FORCE)\n")
		b.WriteString("endfunction()\n\n")
	}
	for _, name := range names {
		cc := custom[name]
		if cc == nil {
			cc = &ConfigurationCfg{}
		}
		suf := strings.ToUpper(name)
		inh := ""
		if cc.Inherits != "" {
			inh = strings.ToUpper(cc.Inherits)
		}
		replace := cc.hasReplacementFlags()

		seed := func(prefix string) string {
			if inh == "" {
				return ""
			}
			return "${" + prefix + inh + "} "
		}
		cflags := expandVars(strings.TrimSpace(seed("CMAKE_C_FLAGS_")+joinFlags(cc.Flags, cc.CFlags)), vars)
		cxxflags := expandVars(strings.TrimSpace(seed("CMAKE_CXX_FLAGS_")+joinFlags(cc.Flags, cc.CxxFlags)), vars)
		link := expandVars(strings.TrimSpace(cc.LinkFlags), vars)
		appendCFlags := expandVars(joinFlags(cc.AppendFlags, cc.AppendCFlags), vars)
		appendCxxFlags := expandVars(joinFlags(cc.AppendFlags, cc.AppendCxxFlags), vars)
		appendLink := expandVars(strings.TrimSpace(cc.AppendLinkFlags), vars)

		fmt.Fprintf(&b, "# --- %s", name)
		if cc.Inherits != "" {
			fmt.Fprintf(&b, " (inherits %s)", cc.Inherits)
		}
		if cc.hasAppendFlags() {
			fmt.Fprintf(&b, " (append)")
		}
		b.WriteString(" ---\n")
		if replace {
			fmt.Fprintf(&b, "set(CMAKE_C_FLAGS_%s %s CACHE STRING \"cmk: %s C flags\" FORCE)\n",
				suf, cmakeQuote(joinFlags(cflags, appendCFlags)), name)
			fmt.Fprintf(&b, "set(CMAKE_CXX_FLAGS_%s %s CACHE STRING \"cmk: %s C++ flags\" FORCE)\n",
				suf, cmakeQuote(joinFlags(cxxflags, appendCxxFlags)), name)
		} else {
			writeAppendConfigFlag(&b, "CMAKE_C_FLAGS_"+suf, appendCFlags, fmt.Sprintf("cmk: %s C flags", name))
			writeAppendConfigFlag(&b, "CMAKE_CXX_FLAGS_"+suf, appendCxxFlags, fmt.Sprintf("cmk: %s C++ flags", name))
		}
		for _, kind := range []string{"EXE", "SHARED", "MODULE"} {
			lf := link
			if inh != "" {
				lf = strings.TrimSpace("${CMAKE_" + kind + "_LINKER_FLAGS_" + inh + "} " + link)
			}
			varName := "CMAKE_" + kind + "_LINKER_FLAGS_" + suf
			desc := fmt.Sprintf("cmk: %s %s linker flags", name, strings.ToLower(kind))
			if replace {
				fmt.Fprintf(&b, "set(%s %s CACHE STRING %s FORCE)\n",
					varName, cmakeQuote(joinFlags(lf, appendLink)), cmakeQuote(desc))
			} else {
				writeAppendConfigFlag(&b, varName, appendLink, desc)
			}
		}
		b.WriteString("\n")
	}

	return path, b.String()
}

// writeConfigFlagsFile materializes configFlagsFile on disk: written when
// its content changed, left untouched when identical (the file is a CMake
// input, and a fresh mtime would make every staleness check reconfigure),
// removed when no configuration edits remain.
func writeConfigFlagsFile(p *Project) error {
	path, content := configFlagsFile(p)
	if content == "" {
		os.Remove(path) // stale file from a previous config set
		return nil
	}
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, []byte(content)) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// cmakeQuote wraps a value as a CMake quoted argument, escaping the two
// characters that are special inside one. ${...} references pass through so
// `inherits` expands.
func cmakeQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

func joinFlags(parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return strings.Join(out, " ")
}

func hasAppendConfigFlags(configs map[string]*ConfigurationCfg) bool {
	for _, cc := range configs {
		if cc != nil && cc.hasAppendFlags() {
			return true
		}
	}
	return false
}

func (cc *ConfigurationCfg) hasReplacementFlags() bool {
	if cc == nil {
		return false
	}
	return cc.Inherits != "" || cc.Flags != "" || cc.CFlags != "" ||
		cc.CxxFlags != "" || cc.LinkFlags != ""
}

func (cc *ConfigurationCfg) hasAppendFlags() bool {
	if cc == nil {
		return false
	}
	return cc.AppendFlags != "" || cc.AppendCFlags != "" ||
		cc.AppendCxxFlags != "" || cc.AppendLinkFlags != ""
}

func writeAppendConfigFlag(b *strings.Builder, varName, suffix, desc string) {
	if strings.TrimSpace(suffix) == "" {
		return
	}
	fmt.Fprintf(b, "cmk_append_config_flag(%s %s %s)\n", varName, cmakeQuote(suffix), cmakeQuote(desc))
}
