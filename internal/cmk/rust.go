package cmk

import (
	"bytes"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// RustCrateCfg describes an in-tree Rust staticlib crate managed by cmk.
// cmk drives Cargo with the pinned C/C++ toolchain environment and exports a
// standard <cmake-name>Config.cmake package so CMake consumes it via
// find_package(<cmake-name> REQUIRED) without Corrosion or CMake-side Rust logic.
type RustCrateCfg struct {
	Manifest          string            `yaml:"manifest"`
	Package           string            `yaml:"package,omitempty"`
	LibName           string            `yaml:"lib-name,omitempty"`
	CMakeName         string            `yaml:"cmake-name,omitempty"`
	Features          []string          `yaml:"features,omitempty"`
	NoDefaultFeatures bool              `yaml:"no-default-features,omitempty"`
	Target            string            `yaml:"target,omitempty"`
	CxxBridge         bool              `yaml:"cxxbridge,omitempty"`
	Env               map[string]string `yaml:"env,omitempty"`
}

func validateRust(cfg *Config) error {
	for name, c := range cfg.Rust {
		if !depNameRe.MatchString(name) {
			return fmt.Errorf("rust.%s: invalid crate name", name)
		}
		if c == nil {
			return fmt.Errorf("rust.%s: configuration is empty", name)
		}
		if strings.TrimSpace(c.Manifest) == "" {
			return fmt.Errorf("rust.%s: manifest is required", name)
		}
		if c.CMakeName != "" && !cmakeCacheNameRe.MatchString(c.CMakeName) {
			return fmt.Errorf("rust.%s.cmake-name: invalid CMake package name %q", name, c.CMakeName)
		}
	}
	return nil
}

func sortedRustNames(crates map[string]*RustCrateCfg) []string {
	return slices.Sorted(maps.Keys(crates))
}

func rustCMakeName(name string, c *RustCrateCfg) string {
	if c != nil && c.CMakeName != "" {
		return c.CMakeName
	}
	return name
}

func rustPackageName(name string, c *RustCrateCfg) string {
	if c != nil && c.Package != "" {
		return c.Package
	}
	return name
}

func rustLibName(name string, c *RustCrateCfg) string {
	if c != nil && c.LibName != "" {
		return strings.ReplaceAll(c.LibName, "-", "_")
	}
	return strings.ReplaceAll(rustPackageName(name, c), "-", "_")
}

func rustRootDir(buildDir string) string {
	return filepath.Join(buildDir, "cmk-rust")
}

func rustCargoTargetDir(buildDir string) string {
	return filepath.Join(rustRootDir(buildDir), "cargo-target")
}

func rustPackageConfigDir(buildDir, cmakeName string) string {
	return filepath.Join(rustRootDir(buildDir), "cmake", cmakeName)
}

func rustCxxBridgeDir(buildDir, name string) string {
	return filepath.Join(rustRootDir(buildDir), name, "cxxbridge")
}

func rustProfileForConfig(configName string) (release bool, profileDir string) {
	switch strings.ToLower(strings.TrimSpace(configName)) {
	case "release", "relwithdebinfo", "minsizerel":
		return true, "release"
	default:
		return false, "debug"
	}
}

func rustArtifactPath(buildDir, name string, c *RustCrateCfg, profileDir string) string {
	prefix, suffix := "lib", ".a"
	if runtime.GOOS == "windows" {
		prefix, suffix = "", ".lib"
	}
	parts := []string{rustCargoTargetDir(buildDir)}
	if c.Target != "" {
		parts = append(parts, c.Target)
	}
	parts = append(parts, profileDir, prefix+rustLibName(name, c)+suffix)
	return filepath.Join(parts...)
}

func allRustExports(p *Project, buildDir string) []string {
	if len(p.Cfg.Rust) == 0 {
		return nil
	}
	var out []string
	for _, name := range sortedRustNames(p.Cfg.Rust) {
		cmakeName := rustCMakeName(name, p.Cfg.Rust[name])
		out = append(out, "-D"+cmakeName+"_DIR="+rustPackageConfigDir(buildDir, cmakeName))
	}
	return out
}

func rustStampEntries(p *Project) []string {
	if len(p.Cfg.Rust) == 0 {
		return nil
	}
	var out []string
	for _, name := range sortedRustNames(p.Cfg.Rust) {
		c := p.Cfg.Rust[name]
		out = append(out,
			fmt.Sprintf("rust:%s:manifest=%s", name, c.Manifest),
			fmt.Sprintf("rust:%s:pkg=%s", name, rustPackageName(name, c)),
			fmt.Sprintf("rust:%s:lib=%s", name, rustLibName(name, c)),
			fmt.Sprintf("rust:%s:cmake=%s", name, rustCMakeName(name, c)),
			fmt.Sprintf("rust:%s:target=%s", name, c.Target),
			fmt.Sprintf("rust:%s:cxxbridge=%v", name, c.CxxBridge),
			fmt.Sprintf("rust:%s:no-default-features=%v", name, c.NoDefaultFeatures),
			fmt.Sprintf("rust:%s:features=%s", name, strings.Join(c.Features, ",")),
		)
		for _, k := range slices.Sorted(maps.Keys(c.Env)) {
			out = append(out, fmt.Sprintf("rust:%s:env:%s=%s", name, k, c.Env[k]))
		}
	}
	return out
}

func detectRustHostTriple(env []string) string {
	cmd := exec.Command("rustc", "-vV")
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	for line := range strings.Lines(string(out)) {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "host:"); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

// probeRustNativeLibs asks rustc what native system libraries a staticlib
// requires on the target platform. C++ stdlib entries (-lstdc++, -lc++) are
// intentionally omitted so CMake's own C++ compiler/stdlib stays authoritative.
func probeRustNativeLibs(env []string, target string) ([]string, error) {
	rustcBin, err := exec.LookPath("rustc")
	if err != nil {
		return nil, fmt.Errorf("rustc not found on PATH (required by cmk.yaml rust section)")
	}
	tmpDir, err := os.MkdirTemp("", "cmk-rust-probe-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	args := []string{
		"--crate-type", "staticlib",
		"--print", "native-static-libs",
		"-o", filepath.Join(tmpDir, "libprobe.a"),
		"-",
	}
	if target != "" {
		args = append(args, "--target", target)
	}
	cmd := exec.Command(rustcBin, args...)
	cmd.Env = env
	cmd.Stdin = bytes.NewReader(nil)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("rustc native-static-libs probe failed: %w\n%s", err, strings.TrimSpace(stderr.String()))
	}
	return parseNativeStaticLibs(stderr.String()), nil
}

func parseNativeStaticLibs(stderr string) []string {
	var libs []string
	for line := range strings.Lines(stderr) {
		line = strings.TrimSpace(line)
		rest, ok := strings.CutPrefix(line, "note: native-static-libs:")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		for i := 0; i < len(fields); i++ {
			tok := fields[i]
			if tok == "-framework" && i+1 < len(fields) {
				libs = append(libs, "-framework "+fields[i+1])
				i++
				continue
			}
			if lib, ok := strings.CutPrefix(tok, "-l"); ok && lib != "" {
				if lib == "stdc++" || lib == "c++" {
					continue
				}
				libs = append(libs, lib)
				continue
			}
			if strings.HasSuffix(tok, ".lib") || strings.HasSuffix(tok, ".a") {
				libs = append(libs, tok)
			}
		}
	}
	return uniqueStrings(libs)
}

func buildRustCrate(p *Project, tc *Toolchain, buildDir, name string, c *RustCrateCfg, configName string, verbose bool) error {
	cargoBin, err := exec.LookPath("cargo")
	if err != nil {
		return fmt.Errorf("cargo not found on PATH (required by rust.%s)", name)
	}

	manifest := c.Manifest
	if !filepath.IsAbs(manifest) {
		manifest = filepath.Join(p.Root, manifest)
	}
	manifest = filepath.Clean(manifest)
	if !fileExists(manifest) {
		return fmt.Errorf("rust.%s: manifest not found: %s", name, manifest)
	}

	targetDir := rustCargoTargetDir(buildDir)
	release, _ := rustProfileForConfig(configName)

	args := []string{
		"build",
		"--lib",
		"--manifest-path", manifest,
		"--target-dir", targetDir,
	}
	if pkg := rustPackageName(name, c); pkg != "" {
		args = append(args, "--package", pkg)
	}
	if release {
		args = append(args, "--release")
	}
	if c.NoDefaultFeatures {
		args = append(args, "--no-default-features")
	}
	if len(c.Features) > 0 {
		args = append(args, "--features", strings.Join(c.Features, ","))
	}
	if c.Target != "" {
		args = append(args, "--target", c.Target)
	}
	if verbose {
		args = append(args, "--verbose")
	}

	baseEnv := p.commandEnv(tc.envMap())
	triple := c.Target
	if triple == "" {
		triple = detectRustHostTriple(baseEnv)
	}

	extraEnv := map[string]string{}
	for k, v := range c.Env {
		extraEnv[k] = v
	}
	if triple != "" {
		ccKey := strings.ReplaceAll(triple, "-", "_")
		if tc.CC != "" {
			extraEnv["CC_"+ccKey] = tc.CC
		}
		if tc.CXX != "" {
			extraEnv["CXX_"+ccKey] = tc.CXX
		}
	}
	if c.CxxBridge {
		extraEnv["CXXBRIDGE_OUT_DIR"] = rustCxxBridgeDir(buildDir, name)
	}
	if strings.EqualFold(strings.TrimSpace(configName), "RelWithDebInfo") {
		extraEnv["CARGO_PROFILE_RELEASE_DEBUG"] = "true"
	}

	env := p.commandEnv(tc.envMap(), extraEnv)
	fmt.Fprintf(os.Stderr, "+ %s %s\n", shellQuote([]string{cargoBin}), shellQuote(args))
	if err := runStreaming(env, cargoBin, args...); err != nil {
		return fmt.Errorf("rust.%s cargo build failed: %w", name, err)
	}
	return nil
}

func buildAllRustCrates(p *Project, tc *Toolchain, buildDir, configName string, verbose bool) error {
	if len(p.Cfg.Rust) == 0 {
		return nil
	}
	for _, name := range sortedRustNames(p.Cfg.Rust) {
		if err := buildRustCrate(p, tc, buildDir, name, p.Cfg.Rust[name], configName, verbose); err != nil {
			return err
		}
	}
	return nil
}

func writeRustCMakeConfig(buildDir, name string, c *RustCrateCfg, defaultConfig string, nativeLibs []string) error {
	cmakeName := rustCMakeName(name, c)
	cfgDir := rustPackageConfigDir(buildDir, cmakeName)
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		return err
	}

	_, defaultProfile := rustProfileForConfig(defaultConfig)
	defaultLib := filepath.ToSlash(rustArtifactPath(buildDir, name, c, defaultProfile))
	debugLib := filepath.ToSlash(rustArtifactPath(buildDir, name, c, "debug"))
	releaseLib := filepath.ToSlash(rustArtifactPath(buildDir, name, c, "release"))

	var b strings.Builder
	fmt.Fprintf(&b, "# Generated by cmk for rust.%s — do not edit.\n", name)
	fmt.Fprintf(&b, "if(NOT TARGET %s::%s)\n", cmakeName, cmakeName)
	fmt.Fprintf(&b, "    add_library(%s::%s STATIC IMPORTED GLOBAL)\n", cmakeName, cmakeName)
	fmt.Fprintf(&b, "    set_target_properties(%s::%s PROPERTIES\n", cmakeName, cmakeName)
	fmt.Fprintf(&b, "        IMPORTED_LOCATION %q\n", defaultLib)
	fmt.Fprintf(&b, "        IMPORTED_LOCATION_DEBUG %q\n", debugLib)
	fmt.Fprintf(&b, "        IMPORTED_LOCATION_RELEASE %q\n", releaseLib)
	fmt.Fprintf(&b, "        IMPORTED_LOCATION_RELWITHDEBINFO %q\n", releaseLib)
	fmt.Fprintf(&b, "        IMPORTED_LOCATION_MINSIZEREL %q\n", releaseLib)
	fmt.Fprintf(&b, "    )\n")

	if len(nativeLibs) > 0 {
		fmt.Fprintf(&b, "    set_property(TARGET %s::%s APPEND PROPERTY INTERFACE_LINK_LIBRARIES", cmakeName, cmakeName)
		for _, lib := range nativeLibs {
			fmt.Fprintf(&b, " %q", lib)
		}
		b.WriteString(")\n")
	}

	if c.CxxBridge {
		bridgeDir := rustCxxBridgeDir(buildDir, name)
		incDir := filepath.ToSlash(filepath.Join(bridgeDir, "include"))
		bridgeCC := filepath.ToSlash(filepath.Join(bridgeDir, "bridge.cc"))
		fmt.Fprintf(&b, "    set_property(TARGET %s::%s APPEND PROPERTY INTERFACE_INCLUDE_DIRECTORIES %q)\n", cmakeName, cmakeName, incDir)
		fmt.Fprintf(&b, "    set(%s_CXXBRIDGE_INCLUDE_DIR %q)\n", cmakeName, incDir)
		fmt.Fprintf(&b, "    set(%s_CXXBRIDGE_SOURCE %q)\n", cmakeName, bridgeCC)
	}

	b.WriteString("endif()\n")
	fmt.Fprintf(&b, "set(%s_FOUND TRUE)\n", cmakeName)

	cfgPath := filepath.Join(cfgDir, cmakeName+"Config.cmake")
	return writeFileIfChanged(cfgPath, []byte(b.String()), 0o644)
}

func writeFileIfChanged(path string, content []byte, perm os.FileMode) error {
	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, content) {
		return nil
	}
	return os.WriteFile(path, content, perm)
}

// prepareRustCrates runs during `cmk config`: it builds each configured crate
// for the default configuration (producing the initial staticlib and any
// cxxbridge generated headers/sources), probes required native static libs,
// and writes <cmake-name>Config.cmake files consumed by find_package().
func prepareRustCrates(p *Project, tc *Toolchain, buildDir string, preset *PresetCfg) error {
	if len(p.Cfg.Rust) == 0 {
		return nil
	}
	defaultCfg := defaultRustConfig(p, preset)
	if err := buildAllRustCrates(p, tc, buildDir, defaultCfg, false); err != nil {
		return err
	}

	baseEnv := p.commandEnv(tc.envMap())
	probed := map[string][]string{}
	for _, name := range sortedRustNames(p.Cfg.Rust) {
		c := p.Cfg.Rust[name]
		libs, ok := probed[c.Target]
		if !ok {
			var err error
			libs, err = probeRustNativeLibs(baseEnv, c.Target)
			if err != nil {
				return err
			}
			probed[c.Target] = libs
		}
		if err := writeRustCMakeConfig(buildDir, name, c, defaultCfg, libs); err != nil {
			return err
		}
	}
	return nil
}

func defaultRustConfig(p *Project, preset *PresetCfg) string {
	if preset != nil && preset.BuildType != "" {
		return preset.BuildType
	}
	if p.Cfg.Configure.DefaultConfiguration != "" {
		return p.Cfg.Configure.DefaultConfiguration
	}
	return "Debug"
}

func effectiveBuildConfig(t *buildTree) string {
	if t.config != "" {
		return t.config
	}
	if preset := t.preset(); preset != nil && preset.BuildType != "" {
		return preset.BuildType
	}
	val, _ := readCMakeCacheVar(t.dir, "CMAKE_BUILD_TYPE")
	return val
}
