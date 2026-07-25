package cmk

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

var configNameRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

var standardConfigs = []string{"Debug", "Release", "RelWithDebInfo", "MinSizeRel"}

func effectiveGenerator(cfg *Config, preset *PresetCfg) string {
	if preset != nil && preset.Generator != "" {
		return preset.Generator
	}
	if cfg.Configure.Generator != "" {
		return cfg.Configure.Generator
	}
	return "Ninja"
}

func isMultiConfigGenerator(generator string) bool {
	return strings.Contains(generator, "Multi-Config") ||
		strings.HasPrefix(generator, "Visual Studio") ||
		generator == "Xcode"
}

func isMultiConfig(cfg *Config, preset *PresetCfg) bool {
	return isMultiConfigGenerator(effectiveGenerator(cfg, preset))
}

func hasMultiConfigPreset(cfg *Config) bool {
	for _, preset := range cfg.Configure.Presets {
		if isMultiConfig(cfg, preset) {
			return true
		}
	}
	return false
}

func configuredConfigurations(cfg *Config) []string {
	names := make([]string, 0, len(cfg.Configure.Configurations))
	for _, configuration := range cfg.Configure.Configurations {
		if configuration != nil {
			names = append(names, configuration.Name)
		}
	}
	return names
}

func (p *Project) resolveConfig(preset *PresetCfg, explicit string) (string, error) {
	if !isMultiConfig(p.Cfg, preset) {
		if explicit != "" {
			return "", fmt.Errorf("--config %q requires a multi-config generator", explicit)
		}
		return "", nil
	}
	configurations := configuredConfigurations(p.Cfg)
	if explicit == "" {
		return p.Cfg.Configure.DefaultConfiguration, nil
	}
	if !slices.Contains(configurations, explicit) {
		return "", fmt.Errorf("configuration %q not found (known: %s)", explicit, strings.Join(configurations, ", "))
	}
	return explicit, nil
}

// foreignConfig resolves the configuration of a build tree cmk did not
// configure, reading it out of the tree's own cache: an explicit --config
// must name one of CMAKE_CONFIGURATION_TYPES, otherwise
// CMAKE_DEFAULT_BUILD_TYPE (then the first configured type) decides — the
// same configuration a bare `cmake --build` would use. Single-config trees
// have none, so --config is an error there, exactly as in a managed project.
// Every command resolves through here, so build, run, test and lint of one
// foreign tree can never address different configurations.
func foreignConfig(buildDir, explicit string) (string, error) {
	cache, err := readCMakeCache(filepath.Join(buildDir, "CMakeCache.txt"))
	if err != nil {
		return "", err
	}
	if !isMultiConfigGenerator(cache["CMAKE_GENERATOR"]) {
		if explicit != "" {
			return "", fmt.Errorf("--config %q requires a multi-config generator (%s uses %s)",
				explicit, buildDir, cache["CMAKE_GENERATOR"])
		}
		return "", nil
	}
	configurations := strings.FieldsFunc(cache["CMAKE_CONFIGURATION_TYPES"], func(r rune) bool { return r == ';' })
	selected := explicit
	if selected == "" {
		selected = cache["CMAKE_DEFAULT_BUILD_TYPE"]
	}
	if selected == "" && len(configurations) > 0 {
		selected = configurations[0]
	}
	if selected == "" {
		return "", fmt.Errorf("cannot select a configuration from %s", filepath.Join(buildDir, "CMakeCache.txt"))
	}
	if len(configurations) > 0 && !slices.Contains(configurations, selected) {
		return "", fmt.Errorf("configuration %q not found (known: %s)", selected, strings.Join(configurations, ", "))
	}
	return selected, nil
}

// resolveVariant selects a managed preset and an optional multi-config
// configuration. Without cmk.yaml it selects an existing foreign CMake build
// tree and reads the configuration out of that tree (see foreignConfig).
func (p *Project) resolveVariant(buildDir, presetName, configName string) (dir, configuration string, err error) {
	if !p.hasCmkConfig() {
		if presetName != "" {
			return "", "", fmt.Errorf("--preset requires a cmk.yaml project")
		}
		if dir, err = p.resolveBuildDir(buildDir); err != nil {
			return "", "", err
		}
		configuration, err = foreignConfig(dir, configName)
		return dir, configuration, err
	}
	if buildDir != "" && presetName != "" {
		return "", "", fmt.Errorf("pass either --build <dir> or --preset <preset>, not both")
	}
	var preset *PresetCfg
	if buildDir != "" {
		dir, err = p.resolveBuildDir(buildDir)
	} else {
		var presetErr error
		preset, presetErr = resolvePreset(p.Cfg, presetName)
		if presetErr != nil {
			return "", "", presetErr
		}
		dir = presetBuildDir(p, preset)
	}
	if err != nil {
		return "", "", err
	}
	if preset == nil {
		preset = presetForDir(p, dir)
	}
	configuration, err = p.resolveConfig(preset, configName)
	return dir, configuration, err
}

// configFlagArgs returns the -D cache arguments that populate the compile and
// link flags for every configuration that declares them. Each
// CMAKE_<LANG>_FLAGS_<CONFIG> / CMAKE_<KIND>_LINKER_FLAGS_<CONFIG> is an
// ordinary cache variable, so it injects like any other cmk-managed variable
// and mirrors into the generated presets. A custom configuration (Asan) gets
// its whole flag set here; setting flags on a standard configuration replaces
// CMake's initialized defaults.
func configFlagArgs(p *Project) []string {
	vars := p.vars()
	var args []string
	for _, configuration := range p.Cfg.Configure.Configurations {
		if configuration == nil {
			continue
		}
		name := strings.ToUpper(configuration.Name)
		compile := expandFlagList(configuration.Compile, vars)
		if cflags := joinFlags(compile, expandFlagList(configuration.C, vars)); cflags != "" {
			args = append(args, "-DCMAKE_C_FLAGS_"+name+"="+cflags)
		}
		if cxxflags := joinFlags(compile, expandFlagList(configuration.CXX, vars)); cxxflags != "" {
			args = append(args, "-DCMAKE_CXX_FLAGS_"+name+"="+cxxflags)
		}
		if link := expandFlagList(configuration.Link, vars); link != "" {
			for _, kind := range []string{"EXE", "SHARED", "MODULE"} {
				args = append(args, "-DCMAKE_"+kind+"_LINKER_FLAGS_"+name+"="+link)
			}
		}
	}
	return args
}

func expandFlagList(flags []string, vars map[string]string) string {
	expanded := make([]string, 0, len(flags))
	for _, flag := range flags {
		if flag = strings.TrimSpace(expandVars(flag, vars)); flag != "" {
			expanded = append(expanded, flag)
		}
	}
	return strings.Join(expanded, " ")
}

func joinFlags(parts ...string) string {
	var values []string
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			values = append(values, part)
		}
	}
	return strings.Join(values, " ")
}
