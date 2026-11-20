package cmk

import (
	"fmt"
	"maps"
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
	return filepath.Join(buildDir, "rust")
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
	out := []string{"rust:cmake-build-rules=6"}
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

// prepareRustPackages writes the build rules CMake uses to obtain Cargo's
// archive and native linker arguments.
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
	for _, name := range sortedRustNames(p.Cfg.Rust) {
		c := p.Cfg.Rust[name]
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
		if err := writeRustCMakeConfig(p.Root, buildDir, name, c, cargo, p.commandEnvOverrides(tc.envMap(), env)); err != nil {
			return err
		}
	}
	return nil
}
