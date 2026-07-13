package cmk

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

const configFileName = "cmk.yaml"

// Config is the cmk.yaml schema. CMake presets identify configure/build
// trees; configurations identify build-time configurations within a
// multi-config tree.
type Config struct {
	Version   int                          `yaml:"version"`
	Toolchain ToolchainCfg                 `yaml:"toolchain"`
	Deps      map[string]*DepCfg           `yaml:"dependencies"`
	Configure ConfigureCfg                 `yaml:"cmake"`
	Install   InstallCfg                   `yaml:"install"`
	Env       map[string]string            `yaml:"env"`
	TargetEnv map[string]map[string]string `yaml:"target-env"`
	Fmt       FmtCfg                       `yaml:"format"`
	Lint      LintCfg                      `yaml:"lint"`
}

// ToolchainCfg maps platform names to clangup selectors. Exact
// OS-architecture keys override OS keys, then default is used.
type ToolchainCfg map[string]string

func (cfg ToolchainCfg) selectorFor(goos, goarch string) string {
	if selector := cfg[hostPlatform(goos, goarch)]; selector != "" {
		return selector
	}
	switch goos {
	case "linux":
		if cfg["linux"] != "" {
			return cfg["linux"]
		}
	case "darwin":
		if cfg["macos"] != "" {
			return cfg["macos"]
		}
	}
	return cfg["default"]
}

type SourceCfg struct {
	URL    string `yaml:"url,omitempty"`
	SHA256 string `yaml:"sha256,omitempty"`
	Git    string `yaml:"git,omitempty"`
	Ref    string `yaml:"ref,omitempty"`
}

type DepCfg struct {
	Script      string            `yaml:"script"`
	Needs       []string          `yaml:"needs,omitempty"`
	CMakeName   string            `yaml:"cmake-name,omitempty"`
	Source      *SourceCfg        `yaml:"source,omitempty"`
	Env         map[string]string `yaml:"env,omitempty"`
	Patches     []string          `yaml:"patches,omitempty"`
	ExtraInputs []string          `yaml:"extra-inputs,omitempty"`
}

type PresetCfg struct {
	Name                 string         `yaml:"-"`
	BuildDir             string         `yaml:"build-dir"`
	BuildType            string         `yaml:"build-type"`
	Inherits             string         `yaml:"inherits"`
	Generator            string         `yaml:"generator"`
	DefaultConfiguration string         `yaml:"default-configuration"`
	Configurations       []string       `yaml:"configurations"`
	Variables            map[string]any `yaml:"variables"`
	Args                 []string       `yaml:"args"`
}

type ConfigureCfg struct {
	Generator            string                `yaml:"generator"`
	DefaultPreset        string                `yaml:"default-preset"`
	DefaultConfiguration string                `yaml:"default-configuration"`
	CompileCommands      string                `yaml:"compile-commands"`
	CompilerLauncher     string                `yaml:"launcher"`
	Variables            map[string]any        `yaml:"variables"`
	Args                 []string              `yaml:"args"`
	Presets              map[string]*PresetCfg `yaml:"presets"`
	Configurations       []*ConfigurationCfg   `yaml:"configurations"`
	configurationByName  map[string]*ConfigurationCfg
}

// ConfigurationCfg appends flags to one multi-config configuration. Inherits
// selects the configuration whose initialized CMake flags form the base.
type ConfigurationCfg struct {
	Name     string   `yaml:"name"`
	Inherits string   `yaml:"inherits"`
	Compile  []string `yaml:"compile"`
	C        []string `yaml:"c"`
	CXX      []string `yaml:"cxx"`
	Link     []string `yaml:"link"`
}

type InstallCfg struct {
	Prefix    string `yaml:"prefix"`
	Component string `yaml:"component"`
	Strip     bool   `yaml:"strip"`
}

type FmtCfg struct {
	Ignore []string `yaml:"ignore"`
}

type LintCfg struct {
	Ignore           []string `yaml:"ignore"`
	WarningsAsErrors bool     `yaml:"warnings-as-errors"`
	HeaderFilter     string   `yaml:"header-filter"`
	ExtraArgs        []string `yaml:"extra-args"`
}

var depNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
var presetNameRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]*$`)
var cmakeCacheNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var toolchainPlatformRe = regexp.MustCompile(`^(linux|macos)-(x86_64|aarch64)$`)
var sha256Re = regexp.MustCompile(`^[0-9a-f]{64}$`)

func loadConfig(root string) (*Config, error) {
	cfg := &Config{}
	path := filepath.Join(root, configFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			if err := normalizeConfig(cfg); err != nil {
				return nil, err
			}
			return cfg, nil
		}
		return nil, err
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if cfg.Version != 1 {
		return nil, fmt.Errorf("%s: version must be 1", path)
	}
	if err := normalizeConfig(cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := validateConfig(cfg, root); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

func normalizeConfig(cfg *Config) error {
	if cfg.Toolchain == nil {
		cfg.Toolchain = ToolchainCfg{}
	}
	if cfg.Deps == nil {
		cfg.Deps = map[string]*DepCfg{}
	}
	if cfg.Env == nil {
		cfg.Env = map[string]string{}
	}
	if cfg.TargetEnv == nil {
		cfg.TargetEnv = map[string]map[string]string{}
	}
	if cfg.Configure.Generator == "" {
		cfg.Configure.Generator = "Ninja"
	}
	if cfg.Configure.Presets == nil {
		cfg.Configure.Presets = map[string]*PresetCfg{}
	}
	if len(cfg.Configure.Presets) == 0 {
		cfg.Configure.Presets["default"] = &PresetCfg{BuildDir: "build"}
	}
	for name, preset := range cfg.Configure.Presets {
		if preset == nil {
			preset = &PresetCfg{}
			cfg.Configure.Presets[name] = preset
		}
		preset.Name = name
	}
	if err := resolvePresetInheritance(cfg); err != nil {
		return err
	}
	if cfg.Configure.DefaultPreset == "" {
		if cfg.Configure.Presets["default"] != nil {
			cfg.Configure.DefaultPreset = "default"
		} else if len(cfg.Configure.Presets) == 1 {
			for name := range cfg.Configure.Presets {
				cfg.Configure.DefaultPreset = name
			}
		}
	}
	if hasMultiConfigPreset(cfg) && len(cfg.Configure.Configurations) == 0 {
		for _, name := range standardConfigs {
			cfg.Configure.Configurations = append(cfg.Configure.Configurations, &ConfigurationCfg{Name: name})
		}
	}
	cfg.Configure.configurationByName = map[string]*ConfigurationCfg{}
	for _, configuration := range cfg.Configure.Configurations {
		if configuration != nil {
			cfg.Configure.configurationByName[configuration.Name] = configuration
		}
	}
	if hasMultiConfigPreset(cfg) && cfg.Configure.DefaultConfiguration == "" && len(cfg.Configure.Configurations) > 0 {
		cfg.Configure.DefaultConfiguration = cfg.Configure.Configurations[0].Name
	}
	return nil
}

func resolvePresetInheritance(cfg *Config) error {
	raw := cfg.Configure.Presets
	resolved := make(map[string]*PresetCfg, len(raw))
	visiting := map[string]bool{}
	var resolve func(string) (*PresetCfg, error)
	resolve = func(name string) (*PresetCfg, error) {
		if preset := resolved[name]; preset != nil {
			return preset, nil
		}
		preset := raw[name]
		if preset == nil {
			return nil, fmt.Errorf("cmake preset %q does not exist", name)
		}
		if visiting[name] {
			return nil, fmt.Errorf("cmake preset inheritance contains a cycle at %q", name)
		}
		visiting[name] = true
		var parent *PresetCfg
		if preset.Inherits != "" {
			if raw[preset.Inherits] == nil {
				return nil, fmt.Errorf("cmake preset %q inherits unknown preset %q", name, preset.Inherits)
			}
			var err error
			parent, err = resolve(preset.Inherits)
			if err != nil {
				return nil, err
			}
		}
		merged := mergePreset(parent, preset)
		merged.Name = name
		if merged.BuildDir == "" {
			merged.BuildDir = filepath.Join("build", name)
		}
		delete(visiting, name)
		resolved[name] = &merged
		return &merged, nil
	}
	for name := range raw {
		if _, err := resolve(name); err != nil {
			return err
		}
	}
	cfg.Configure.Presets = resolved
	return nil
}

func mergePreset(parent, child *PresetCfg) PresetCfg {
	merged := *child
	merged.Configurations = cloneStrings(child.Configurations)
	merged.Args = append([]string(nil), child.Args...)
	merged.Variables = mergePresetVariables(nil, child.Variables)
	if parent == nil {
		return merged
	}
	if merged.BuildType == "" {
		merged.BuildType = parent.BuildType
	}
	if merged.Generator == "" {
		merged.Generator = parent.Generator
	}
	if merged.DefaultConfiguration == "" {
		merged.DefaultConfiguration = parent.DefaultConfiguration
	}
	if child.Configurations == nil {
		merged.Configurations = cloneStrings(parent.Configurations)
	}
	merged.Variables = mergePresetVariables(parent.Variables, child.Variables)
	merged.Args = append(append([]string(nil), parent.Args...), child.Args...)
	// Every preset owns its build directory. A child never inherits the
	// parent's path and receives build/<name> when build-dir is omitted.
	merged.BuildDir = child.BuildDir
	return merged
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string{}, values...)
}

func mergePresetVariables(parent, child map[string]any) map[string]any {
	if len(parent) == 0 && len(child) == 0 {
		return nil
	}
	merged := make(map[string]any, len(parent)+len(child))
	for name, value := range parent {
		merged[name] = value
	}
	for name, value := range child {
		merged[name] = value
	}
	return merged
}

func validateConfig(cfg *Config, root string) error {
	for platform, selector := range cfg.Toolchain {
		if platform != "default" && platform != "linux" && platform != "macos" && !toolchainPlatformRe.MatchString(platform) {
			return fmt.Errorf("toolchain.%s: unsupported platform", platform)
		}
		if selector == "" {
			return fmt.Errorf("toolchain.%s: selector is empty", platform)
		}
	}
	if err := validateDeps(cfg); err != nil {
		return err
	}
	if err := validateVariables("cmake.variables", cfg.Configure.Variables); err != nil {
		return err
	}
	if err := validateCMakeArgs("cmake.args", cfg.Configure.Args); err != nil {
		return err
	}
	seenBuilds := map[string]string{}
	for name, preset := range cfg.Configure.Presets {
		if !presetNameRe.MatchString(name) {
			return fmt.Errorf("cmake.presets.%s: invalid preset name", name)
		}
		if err := validateVariables("cmake.presets."+name+".variables", preset.Variables); err != nil {
			return err
		}
		if err := validateCMakeArgs("cmake.presets."+name+".args", preset.Args); err != nil {
			return err
		}
		if preset.BuildType != "" {
			if isMultiConfig(cfg, preset) {
				return fmt.Errorf("cmake.presets.%s.build-type requires a single-config generator", name)
			}
			if _, ok := cfg.Configure.Variables["CMAKE_BUILD_TYPE"]; ok {
				return fmt.Errorf("cmake.presets.%s.build-type conflicts with cmake.variables.CMAKE_BUILD_TYPE", name)
			}
			if _, ok := preset.Variables["CMAKE_BUILD_TYPE"]; ok {
				return fmt.Errorf("cmake.presets.%s sets both build-type and variables.CMAKE_BUILD_TYPE", name)
			}
			if definesVar(cfg.Configure.Args, "CMAKE_BUILD_TYPE") || definesVar(preset.Args, "CMAKE_BUILD_TYPE") {
				return fmt.Errorf("cmake.presets.%s.build-type conflicts with a CMAKE_BUILD_TYPE argument", name)
			}
		}
		build := expandVars(preset.BuildDir, map[string]string{"PROJECT_ROOT": root})
		if !filepath.IsAbs(build) {
			build = filepath.Join(root, build)
		}
		build = filepath.Clean(build)
		if previous := seenBuilds[build]; previous != "" {
			return fmt.Errorf("cmake presets %q and %q use the same build directory %q", previous, name, build)
		}
		seenBuilds[build] = name
	}
	if cfg.Configure.DefaultPreset == "" {
		return fmt.Errorf("cmake.default-preset is required when multiple presets are defined")
	}
	if cfg.Configure.Presets[cfg.Configure.DefaultPreset] == nil {
		return fmt.Errorf("cmake.default-preset %q does not name a preset", cfg.Configure.DefaultPreset)
	}
	if hasMultiConfigPreset(cfg) {
		if err := validateConfigurations(cfg); err != nil {
			return err
		}
	} else {
		if len(cfg.Configure.Configurations) > 0 || cfg.Configure.DefaultConfiguration != "" {
			return fmt.Errorf("cmake configurations and default-configuration require a multi-config generator")
		}
		for name, preset := range cfg.Configure.Presets {
			if preset.Configurations != nil || preset.DefaultConfiguration != "" {
				return fmt.Errorf("cmake.presets.%s configurations require a multi-config generator", name)
			}
		}
		if selection := cfg.Configure.CompileCommands; selection != "" && selection != "default" {
			return fmt.Errorf("cmake.compile-commands must be default with a single-config generator")
		}
	}
	return nil
}

func validateDeps(cfg *Config) error {
	for name, dep := range cfg.Deps {
		if !depNameRe.MatchString(name) {
			return fmt.Errorf("dependencies.%s: invalid dependency name", name)
		}
		if dep == nil || dep.Script == "" {
			return fmt.Errorf("dependencies.%s: missing script", name)
		}
		if dep.Source != nil {
			urlSet, gitSet := dep.Source.URL != "", dep.Source.Git != ""
			if urlSet == gitSet {
				return fmt.Errorf("dependencies.%s.source: set exactly one of url or git", name)
			}
			if urlSet && !sha256Re.MatchString(dep.Source.SHA256) {
				return fmt.Errorf("dependencies.%s.source: url requires a 64-hex sha256", name)
			}
			if gitSet && dep.Source.Ref == "" {
				return fmt.Errorf("dependencies.%s.source: git requires ref", name)
			}
		}
		if len(dep.Patches) > 0 && dep.Source == nil {
			return fmt.Errorf("dependencies.%s: patches require a source", name)
		}
		for _, need := range dep.Needs {
			if cfg.Deps[need] == nil {
				return fmt.Errorf("dependencies.%s: needs unknown dependency %q", name, need)
			}
		}
	}
	return nil
}

func validateConfigurations(cfg *Config) error {
	known := map[string]*ConfigurationCfg{}
	folded := map[string]string{}
	for i, configuration := range cfg.Configure.Configurations {
		if configuration == nil || !configNameRe.MatchString(configuration.Name) {
			return fmt.Errorf("cmake.configurations[%d]: invalid configuration name", i)
		}
		key := strings.ToLower(configuration.Name)
		if previous := folded[key]; previous != "" {
			return fmt.Errorf("cmake configurations %q and %q differ only by case", previous, configuration.Name)
		}
		folded[key] = configuration.Name
		known[configuration.Name] = configuration
	}
	for _, configuration := range cfg.Configure.Configurations {
		if base := configuration.Inherits; base != "" && known[base] == nil && !isStandardConfig(base) {
			return fmt.Errorf("cmake configuration %q inherits unknown configuration %q", configuration.Name, base)
		}
	}
	visiting, visited := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(name string) error {
		if visiting[name] {
			return fmt.Errorf("cmake configuration inheritance contains a cycle at %q", name)
		}
		if visited[name] {
			return nil
		}
		visiting[name] = true
		if base := known[name].Inherits; known[base] != nil {
			if err := visit(base); err != nil {
				return err
			}
		}
		delete(visiting, name)
		visited[name] = true
		return nil
	}
	for name := range known {
		if err := visit(name); err != nil {
			return err
		}
	}
	if known[cfg.Configure.DefaultConfiguration] == nil {
		return fmt.Errorf("cmake.default-configuration %q is not configured", cfg.Configure.DefaultConfiguration)
	}
	if selection := cfg.Configure.CompileCommands; selection != "" && selection != "default" && known[selection] == nil {
		return fmt.Errorf("cmake.compile-commands %q is not configured", selection)
	}
	for name, preset := range cfg.Configure.Presets {
		path := "cmake.presets." + name
		if !isMultiConfig(cfg, preset) {
			if preset.Configurations != nil || preset.DefaultConfiguration != "" {
				return fmt.Errorf("%s configurations require a multi-config generator", path)
			}
			continue
		}
		selected := effectiveConfigurations(cfg, preset)
		if len(selected) == 0 {
			return fmt.Errorf("%s.configurations must not be empty", path)
		}
		included := map[string]bool{}
		for _, configuration := range selected {
			if known[configuration] == nil {
				return fmt.Errorf("%s.configurations contains unknown configuration %q", path, configuration)
			}
			if included[configuration] {
				return fmt.Errorf("%s.configurations contains duplicate configuration %q", path, configuration)
			}
			included[configuration] = true
		}
		defaultConfiguration := effectiveDefaultConfiguration(cfg, preset)
		if !included[defaultConfiguration] {
			return fmt.Errorf("%s.default-configuration %q is not selected", path, defaultConfiguration)
		}
		if selection := cfg.Configure.CompileCommands; selection != "" && selection != "default" && !included[selection] {
			return fmt.Errorf("cmake.compile-commands %q is not selected by %s", selection, path)
		}
	}
	return nil
}

func validateVariables(path string, variables map[string]any) error {
	for name, value := range variables {
		if !cmakeCacheNameRe.MatchString(name) {
			return fmt.Errorf("%s.%s: invalid CMake cache variable", path, name)
		}
		if _, err := cacheValueString(value); err != nil {
			return fmt.Errorf("%s.%s: %w", path, name, err)
		}
		if cmkOwnedCacheVariables[name] {
			return fmt.Errorf("%s.%s is managed by cmk", path, name)
		}
	}
	return nil
}

var cmkOwnedCacheVariables = map[string]bool{
	"CMAKE_CONFIGURATION_TYPES":     true,
	"CMAKE_DEFAULT_BUILD_TYPE":      true,
	"CMAKE_EXPORT_COMPILE_COMMANDS": true,
	"CMAKE_PROJECT_INCLUDE":         true,
	"CMAKE_SUPPRESS_REGENERATION":   true,
	"CMAKE_C_COMPILER_LAUNCHER":     true,
	"CMAKE_CXX_COMPILER_LAUNCHER":   true,
}

func validateCMakeArgs(path string, args []string) error {
	for _, argument := range args {
		ownsConfigureLocation := !strings.HasPrefix(argument, "-D") &&
			(strings.HasPrefix(argument, "-G") || strings.HasPrefix(argument, "-S") || strings.HasPrefix(argument, "-B"))
		if ownsConfigureLocation || argument == "--preset" || strings.HasPrefix(argument, "--preset=") || argument == "--fresh" {
			return fmt.Errorf("%s contains %q, which is managed by cmk", path, argument)
		}
		if !strings.HasPrefix(argument, "-D") {
			continue
		}
		name, _, _ := strings.Cut(argument[2:], "=")
		if index := strings.IndexByte(name, ':'); index >= 0 {
			name = name[:index]
		}
		if cmkOwnedCacheVariables[name] {
			return fmt.Errorf("%s defines %s, which is managed by cmk", path, name)
		}
	}
	return nil
}

func variableArgs(variables map[string]any, vars map[string]string) []string {
	keys := make([]string, 0, len(variables))
	for key := range variables {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	args := make([]string, 0, len(keys))
	for _, key := range keys {
		value, _ := cacheValueString(variables[key])
		args = append(args, "-D"+key+"="+expandVars(value, vars))
	}
	return args
}

func cacheValueString(value any) (string, error) {
	switch value := value.(type) {
	case string:
		return value, nil
	case bool:
		if value {
			return "ON", nil
		}
		return "OFF", nil
	case int:
		return strconv.Itoa(value), nil
	case int64:
		return strconv.FormatInt(value, 10), nil
	case uint64:
		return strconv.FormatUint(value, 10), nil
	case float64:
		return strconv.FormatFloat(value, 'g', -1, 64), nil
	case nil:
		return "", fmt.Errorf("value must be a scalar")
	default:
		return "", fmt.Errorf("value must be a string, boolean, or number")
	}
}
