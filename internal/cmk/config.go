package cmk

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

const configFileName = "cmk.toml"

// Config is the cmk.toml schema.
type Config struct {
	Toolchain ToolchainCfg                 `toml:"toolchain"`
	Deps      map[string]*DepCfg           `toml:"deps"`
	Configure ConfigureCfg                 `toml:"config"`
	Build     BuildCfg                     `toml:"build"`
	Install   InstallCfg                   `toml:"install"`
	Env       map[string]string            `toml:"env"`
	TargetEnv map[string]map[string]string `toml:"target-env"`
	Fmt       FmtCfg                       `toml:"fmt"`
	Lint      LintCfg                      `toml:"lint"`
}

type ToolchainCfg struct {
	// Selector is a clangup channel selector such as "default",
	// "libcxx", or the exact "libcxx@22.1.8-1".
	// Empty leaves an existing CMake build directory in charge of its
	// compiler and uses the system compiler when cmk configures a new tree.
	Selector string `toml:"selector"`
}

type SourceCfg struct {
	URL    string `toml:"url"`
	SHA256 string `toml:"sha256"`
	Git    string `toml:"git"`
	Ref    string `toml:"ref"`
}

type DepCfg struct {
	// Script is the build recipe, relative to the project root. It runs
	// with bash, cwd $CMK_WORK, and must install into $CMK_PREFIX.
	Script string `toml:"script"`
	// Needs are deps that must be built first; their prefixes are exposed
	// to the script as $CMK_DEP_<NAME>_PREFIX.
	Needs []string `toml:"needs"`
	// CMakeName is the find_package() name used for the default
	// -D<name>_ROOT export. Defaults to the dep key. Case matters
	// (CMP0074).
	CMakeName string `toml:"cmake_name"`
	// Source, when set, is fetched and unpacked by cmk into $CMK_SRC.
	// Either url+sha256 (tarball) or git+ref (commit locked in cmk.lock).
	Source *SourceCfg `toml:"source"`
	// Env is exported to the recipe AND hashed into the stamp. Recipes
	// run with a sanitized environment, so build knobs must come
	// through here — never from the caller's shell.
	Env map[string]string `toml:"env"`
	// Patches are root-relative globs applied (patch -p1) to $CMK_SRC
	// after unpacking; their contents are hashed into the stamp.
	Patches []string `toml:"patches"`
	// ExtraInputs are root-relative globs hashed into the stamp without
	// being applied — for files the recipe reads on its own.
	ExtraInputs []string `toml:"extra_inputs"`
}

type PresetCfg struct {
	Dir  string   `toml:"dir"`
	Args []string `toml:"args"`
}

type ConfigureCfg struct {
	Generator string   `toml:"generator"`
	Args      []string `toml:"args"`
	// Default selects the default preset in single-config mode, or the
	// default --config (a configuration name) in multi-config mode.
	Default string `toml:"default"`
	// CompilerLauncher wraps every compile via
	// CMAKE_<LANG>_COMPILER_LAUNCHER, e.g. "ccache" or "sccache". Empty
	// disables it. For ccache, cmk also configures cross-worktree reuse
	// (see Project.ccacheEnv).
	CompilerLauncher string                `toml:"compiler_launcher"`
	Presets          map[string]*PresetCfg `toml:"preset"`

	// --- multi-config (generator = "Ninja Multi-Config") ---
	// One build tree holds every configuration; you pick at build time
	// with `cmk build -c <config>`. Presets are not used in this mode.

	// Dir is the single build directory (default "build").
	Dir string `toml:"dir"`
	// CompileCommands, when set, makes cmk mirror one configuration's
	// compile_commands.json to the project root. A Ninja Multi-Config build
	// exports every configuration into the build dir's database (one entry
	// per file *per config*), so clangd — which uses the nearest database
	// walking up from a source file — picks an arbitrary configuration. This
	// narrows the root copy to the named configuration; "default" resolves
	// to [config].default. For a single-config generator the database is
	// already one-per-file and is mirrored verbatim. Empty disables it (cmk
	// leaves any existing root file untouched). Keep it gitignored.
	CompileCommands string `toml:"compile_commands"`
	// Configurations is CMAKE_CONFIGURATION_TYPES; empty means CMake's
	// standard Debug/Release/RelWithDebInfo/MinSizeRel.
	Configurations []string `toml:"configurations"`
	// Configuration holds per-configuration flag edits, keyed by configuration
	// name (e.g. "Asan" or "RelWithDebInfo"). Custom configurations normally
	// define a flag bundle; built-in configurations should usually use
	// append_* fields to preserve CMake's defaults.
	Configuration map[string]*ConfigurationCfg `toml:"configuration"`
}

// ConfigurationCfg is a multi-config configuration flag edit. flags/c_flags/
// cxx_flags/link_flags define or replace a configuration's flag bundle, usually
// for a custom configuration such as Asan. append_* fields add to the existing
// CMake flags for that configuration, which is the safe way to tweak built-in
// configurations such as RelWithDebInfo.
type ConfigurationCfg struct {
	// Inherits seeds the flags from another configuration's, e.g.
	// inherits = "Debug" prepends ${CMAKE_CXX_FLAGS_DEBUG}.
	Inherits string `toml:"inherits"`
	// Flags apply to both C and C++; CFlags/CxxFlags add language-specific
	// flags on top.
	Flags    string `toml:"flags"`
	CFlags   string `toml:"c_flags"`
	CxxFlags string `toml:"cxx_flags"`
	// LinkFlags apply to executable, shared, and module linking.
	LinkFlags string `toml:"link_flags"`
	// Append* fields preserve the current CMake flags for this configuration
	// and append the requested flags. AppendFlags applies to both C and C++,
	// while AppendCFlags and AppendCxxFlags add language-specific compile
	// flags. AppendLinkFlags applies to executable, shared, and module linking.
	AppendFlags     string `toml:"append_flags"`
	AppendCFlags    string `toml:"append_c_flags"`
	AppendCxxFlags  string `toml:"append_cxx_flags"`
	AppendLinkFlags string `toml:"append_link_flags"`
}

type BuildCfg struct {
	// Default build dir (relative to root) used when none is given, the
	// PWD isn't inside one, and more than one exists.
	Default string `toml:"default"`
}

// InstallCfg configures `cmk install`. An empty Prefix means the prefix
// baked at configure time (CMAKE_INSTALL_PREFIX) is used; set it (or pass
// --prefix) to override at install time.
type InstallCfg struct {
	// Prefix is the install prefix; ${PROJECT_ROOT}/${DEP_*} expand, and a
	// relative path is taken from the project root.
	Prefix string `toml:"prefix"`
	// Component installs only the named install component.
	Component string `toml:"component"`
	// Strip removes symbols from installed binaries.
	Strip bool `toml:"strip"`
}

type FmtCfg struct {
	Ignore []string `toml:"ignore"`
}

type LintCfg struct {
	Ignore           []string `toml:"ignore"`
	WarningsAsErrors bool     `toml:"warnings_as_errors"`
	HeaderFilter     string   `toml:"header_filter"`
	ExtraArgs        []string `toml:"extra_args"`
}

var depNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

func loadConfig(root string) (*Config, error) {
	cfg := &Config{}
	path := filepath.Join(root, configFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, err
	}
	md, err := toml.Decode(string(data), cfg)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if un := md.Undecoded(); len(un) > 0 {
		fmt.Fprintf(os.Stderr, "cmk: warning: unknown keys in %s: %v\n", configFileName, un)
	}
	for name, d := range cfg.Deps {
		if !depNameRe.MatchString(name) {
			return nil, fmt.Errorf("[deps.%s]: invalid dep name", name)
		}
		if d.Script == "" {
			return nil, fmt.Errorf("[deps.%s]: missing script", name)
		}
		if d.Source != nil {
			urlSet, gitSet := d.Source.URL != "", d.Source.Git != ""
			if urlSet == gitSet {
				return nil, fmt.Errorf("[deps.%s].source: set exactly one of url or git", name)
			}
			if urlSet && !sha256Re.MatchString(d.Source.SHA256) {
				return nil, fmt.Errorf("[deps.%s].source: url requires a 64-hex sha256", name)
			}
			if gitSet && d.Source.Ref == "" {
				return nil, fmt.Errorf("[deps.%s].source: git requires ref", name)
			}
		}
		if len(d.Patches) > 0 && d.Source == nil {
			return nil, fmt.Errorf("[deps.%s]: patches require a source (there is no $CMK_SRC to patch)", name)
		}
		for _, n := range d.Needs {
			if _, ok := cfg.Deps[n]; !ok {
				return nil, fmt.Errorf("[deps.%s]: needs unknown dep %q", name, n)
			}
		}
	}
	if isMultiConfig(cfg) {
		if len(cfg.Configure.Presets) > 0 {
			return nil, fmt.Errorf("[config]: a multi-config generator uses one build dir with [config.configuration.*], " +
				"not [config.preset.*] (which create separate build dirs)")
		}
		cfgs := effectiveConfigurations(cfg)
		known := map[string]bool{}
		for _, c := range cfgs {
			known[c] = true
		}
		for name, cc := range cfg.Configure.Configuration {
			if !configNameRe.MatchString(name) {
				return nil, fmt.Errorf("[config.configuration.%s]: invalid configuration name", name)
			}
			if cc.Inherits != "" && !known[cc.Inherits] && !isStandardConfig(cc.Inherits) {
				fmt.Fprintf(os.Stderr, "cmk: warning: [config.configuration.%s].inherits = %q is not a known configuration\n", name, cc.Inherits)
			}
		}
		if d := cfg.Configure.Default; d != "" && !known[d] {
			return nil, fmt.Errorf("[config].default = %q is not one of the configurations: %s", d, strings.Join(cfgs, ", "))
		}
		if cc := cfg.Configure.CompileCommands; cc != "" && cc != "default" && !known[cc] {
			return nil, fmt.Errorf("[config].compile_commands = %q is not one of the configurations: %s (or \"default\")", cc, strings.Join(cfgs, ", "))
		}
	}
	return cfg, nil
}

var sha256Re = regexp.MustCompile(`^[0-9a-f]{64}$`)
