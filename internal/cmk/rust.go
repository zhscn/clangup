package cmk

import (
	"bytes"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// RustCrateCfg describes an in-tree Rust staticlib crate managed by cmk.
// cmk exports a CMake package that drives Cargo with the resolved C/C++ toolchain
// environment when a target needs the crate.
type RustCrateCfg struct {
	Manifest          string            `yaml:"manifest"`
	Package           string            `yaml:"package,omitempty"`
	LibName           string            `yaml:"lib-name,omitempty"`
	CMakeName         string            `yaml:"cmake-name,omitempty"`
	Features          []string          `yaml:"features,omitempty"`
	NoDefaultFeatures bool              `yaml:"no-default-features,omitempty"`
	Target            string            `yaml:"target,omitempty"`
	CxxBridge         bool              `yaml:"cxxbridge,omitempty"`
	CxxBridgeIncludes []string          `yaml:"cxxbridge-includes,omitempty"`
	LTO               string            `yaml:"lto,omitempty"`
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
		switch c.LTO {
		case "", "off", "thin", "fat", "linker-plugin":
		default:
			return fmt.Errorf("rust.%s.lto: unsupported mode %q (expected off, thin, fat, or linker-plugin)", name, c.LTO)
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

func rustRootDir(buildDir string) string {
	return filepath.Join(buildDir, "cmk-rust")
}

func rustPackageConfigDir(buildDir, cmakeName string) string {
	return filepath.Join(rustRootDir(buildDir), "cmake", cmakeName)
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
	out := []string{"rust:cmake-build-rules=2"}
	for _, name := range sortedRustNames(p.Cfg.Rust) {
		c := p.Cfg.Rust[name]
		out = append(out,
			fmt.Sprintf("rust:%s:manifest=%s", name, c.Manifest),
			fmt.Sprintf("rust:%s:pkg=%s", name, rustPackageName(name, c)),
			fmt.Sprintf("rust:%s:lib=%s", name, c.LibName),
			fmt.Sprintf("rust:%s:cmake=%s", name, rustCMakeName(name, c)),
			fmt.Sprintf("rust:%s:target=%s", name, c.Target),
			fmt.Sprintf("rust:%s:cxxbridge=%v", name, c.CxxBridge),
			fmt.Sprintf("rust:%s:cxxbridge-includes=%s", name, strings.Join(c.CxxBridgeIncludes, ",")),
			fmt.Sprintf("rust:%s:lto=%s", name, c.LTO),
			fmt.Sprintf("rust:%s:no-default-features=%v", name, c.NoDefaultFeatures),
			fmt.Sprintf("rust:%s:features=%s", name, strings.Join(c.Features, ",")),
		)
		for _, k := range slices.Sorted(maps.Keys(c.Env)) {
			out = append(out, fmt.Sprintf("rust:%s:env:%s=%s", name, k, c.Env[k]))
		}
	}
	return out
}

func detectRustHostTriple(root string, env []string) string {
	cmd := exec.Command("rustc", "-vV")
	cmd.Dir = root
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
func probeRustNativeLibs(root string, env []string, target string) ([]string, error) {
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
	cmd.Dir = root
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

// prepareRustPackages probes the Rust runtime link requirements and writes the
// packages consumed by CMake before any project crate is built.
func prepareRustPackages(p *Project, tc *Toolchain, buildDir string) error {
	if len(p.Cfg.Rust) == 0 {
		return nil
	}
	cargo, err := exec.LookPath("cargo")
	if err != nil {
		return fmt.Errorf("cargo not found on PATH (required by cmk.yaml rust section): %w", err)
	}
	cargo, err = filepath.Abs(cargo)
	if err != nil {
		return err
	}
	baseEnv := p.commandEnv(tc.envMap())
	host := detectRustHostTriple(p.Root, baseEnv)
	probed := map[string][]string{}
	for _, name := range sortedRustNames(p.Cfg.Rust) {
		c := p.Cfg.Rust[name]
		libs, ok := probed[c.Target]
		if !ok {
			libs, err = probeRustNativeLibs(p.Root, baseEnv, c.Target)
			if err != nil {
				return err
			}
			probed[c.Target] = libs
		}
		env := maps.Clone(c.Env)
		if env == nil {
			env = make(map[string]string)
		}
		triple := c.Target
		if triple == "" {
			triple = host
		}
		if triple != "" {
			key := strings.ReplaceAll(triple, "-", "_")
			if tc.CC != "" {
				env["CC_"+key] = tc.CC
			}
			if tc.CXX != "" {
				env["CXX_"+key] = tc.CXX
			}
		}
		if err := writeRustCMakeConfig(p.Root, buildDir, name, c, cargo, p.commandEnvOverrides(tc.envMap(), env), libs); err != nil {
			return err
		}
	}
	return nil
}
