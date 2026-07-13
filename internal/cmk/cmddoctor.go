package cmk

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// doctorChecker prints ✓/•/!/✗ lines and remembers whether anything failed.
type doctorChecker struct{ failed bool }

func (c *doctorChecker) ok(f string, a ...any)   { fmt.Printf("  ✓ "+f+"\n", a...) }
func (c *doctorChecker) note(f string, a ...any) { fmt.Printf("  • "+f+"\n", a...) }
func (c *doctorChecker) warn(f string, a ...any) { fmt.Printf("  ! "+f+"\n", a...) }
func (c *doctorChecker) fail(f string, a ...any) {
	c.failed = true
	fmt.Printf("  ✗ "+f+"\n", a...)
}

// cmdDoctor reports the project's resolved setup in one place: toolchain,
// build tools, compiler launcher, deps in the store, build dirs, and where
// cmk keeps things. It never installs or builds anything.
func cmdDoctor() error {
	p, err := openProject()
	if err != nil {
		return err
	}
	c := &doctorChecker{}

	fmt.Println("project:")
	c.ok("root %s", p.Root)
	if !p.hasCmkConfig() {
		c.note("foreign CMake project (cmk build uses existing build trees)")
	} else if _, err := os.Stat(filepath.Join(p.Root, lockFileName)); err == nil {
		c.ok("cmk.yaml + cmk.lock present")
	} else {
		c.note("cmk.yaml present; cmk.lock appears on first sync/config")
	}

	fmt.Println("toolchain:")
	doctorToolchain(c, p)

	fmt.Println("build tools:")
	checkTool(c, "cmake")
	for _, preset := range p.Cfg.Configure.Presets {
		if strings.HasPrefix(strings.ToLower(effectiveGenerator(p.Cfg, preset)), "ninja") {
			checkTool(c, "ninja")
			break
		}
	}

	if hasMultiConfigPreset(p.Cfg) {
		fmt.Println("configurations (multi-config):")
		doctorConfigurations(c, p)
	}

	if l := p.Cfg.Configure.CompilerLauncher; l != "" {
		fmt.Println("compiler launcher:")
		doctorLauncher(c, p, l)
	}

	if len(p.Cfg.Deps) > 0 {
		fmt.Println("deps (shared store):")
		doctorDeps(c, p)
	}

	fmt.Println("build dirs:")
	if len(p.BuildDirs) == 0 {
		c.note("none yet (run cmk config)")
	} else {
		tc := doctorStaleToolchain(p)
		for _, d := range p.listBuildDirs() {
			abs := p.BuildDirs[d]
			switch {
			case !p.hasCmkConfig():
				c.note("%s — existing CMake build tree", d)
			case tc == nil:
				c.ok("%s", d) // toolchain unavailable; staleness not assessable
			default:
				if reason := p.reconfigureReason(abs, tc, presetForDir(p, abs)); reason != "" {
					c.note("%s — stale: %s (next build reconfigures)", d, reason)
				} else {
					c.ok("%s — configuration current", d)
				}
			}
		}
	}

	if p.Cfg.Install.Prefix != "" {
		fmt.Println("install:")
		if pfx, err := p.installPrefix(""); err == nil {
			c.ok("prefix %s", pfx)
		}
	}

	fmt.Println("locations:")
	fmt.Printf("  store      %s\n", storeDir())
	fmt.Printf("  downloads  %s\n", downloadsDir())
	fmt.Printf("  presets    %s (machine-local, gitignored)\n", filepath.Join(p.Root, "CMakeUserPresets.json"))

	if c.failed {
		return fmt.Errorf("problems found")
	}
	fmt.Println("all good")
	return nil
}

// doctorStaleToolchain resolves the toolchain for staleness assessment
// without installing anything (doctor's contract). nil when unavailable.
func doctorStaleToolchain(p *Project) *Toolchain {
	if p.toolchainSelector() == "" {
		tc, err := systemToolchain()
		if err != nil {
			return nil
		}
		return tc
	}
	tc, err := locateToolchain(p.toolchainSelector(), p.Lock)
	if err != nil {
		return nil
	}
	return tc // may be nil: not installed
}

func checkTool(c *doctorChecker, name string) {
	if path, err := exec.LookPath(name); err == nil {
		c.ok("%s (%s)", name, path)
	} else {
		c.fail("%s not found on PATH", name)
	}
}

func doctorToolchain(c *doctorChecker, p *Project) {
	selector := p.toolchainSelector()
	if selector == "" {
		tc, err := systemToolchain()
		if err != nil {
			c.fail("%v", err)
			return
		}
		c.ok("system compiler %s", tc.CXX)
		c.note("no toolchain selector set — existing build directories keep their configured compiler")
		return
	}
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		c.warn("clangup toolchains are not available on %s; this host would use the system compiler", runtime.GOOS)
		return
	}

	tc, err := locateToolchain(selector, p.Lock)
	if err != nil {
		c.fail("%v", err)
		return
	}
	if tc == nil {
		c.fail("clangup %s not installed (cmk config installs it, or run: clangup ensure %s)", selector, selector)
		return
	}
	c.ok("clangup %s", tc.Selector)
	if tc.File != "" {
		c.ok("toolchain.cmake present — configures via -DCMAKE_TOOLCHAIN_FILE")
	} else {
		c.warn("no toolchain.cmake — configures with explicit C/C++ compiler paths")
	}
	stdlib := tc.CXXStdlib
	if stdlib == "" {
		stdlib = "system"
	}
	c.note("default C++ stdlib: %s", stdlib)
}

func doctorConfigurations(c *doctorChecker, p *Project) {
	for _, name := range presetNames(p.Cfg.Configure.Presets) {
		preset := p.Cfg.Configure.Presets[name]
		if !isMultiConfig(p.Cfg, preset) {
			continue
		}
		c.ok("%s — %s (default: %s)", name,
			strings.Join(effectiveConfigurations(p.Cfg, preset), ", "),
			effectiveDefaultConfiguration(p.Cfg, preset))
	}
	if len(orderedConfigurationEdits(p.Cfg)) > 0 {
		c.note("configuration flag edits are included via CMAKE_PROJECT_INCLUDE; written to %s", configFlagsFileRel)
	}
}

func doctorLauncher(c *doctorChecker, p *Project, launcher string) {
	path, err := exec.LookPath(launcher)
	if err != nil {
		c.fail("launcher %q not found on PATH (builds proceed without it)", launcher)
		return
	}
	c.ok("%s (%s)", launcher, path)
	if filepath.Base(launcher) == "ccache" {
		if env := p.ccacheEnv(); len(env) > 0 {
			c.ok("CCACHE_BASEDIR=%s, CCACHE_NOHASHDIR — cross-worktree cache reuse", env["CCACHE_BASEDIR"])
		} else {
			c.note("CCACHE_BASEDIR/CCACHE_NOHASHDIR taken from your environment")
		}
	}
}

func doctorDeps(c *doctorChecker, p *Project) {
	for _, name := range sortedDepNames(p.Cfg.Deps) {
		ld := p.Lock.Deps[name]
		stamp := ld.stampFor(runtime.GOOS, runtime.GOARCH)
		if stamp == "" {
			c.warn("%s not synced (run cmk sync)", name)
			continue
		}
		entry := entryDir(name, stamp)
		if fileExists(filepath.Join(entry, completeMarker)) {
			c.ok("%s (%s)", name, filepath.Base(entry))
		} else {
			c.fail("%s pinned in cmk.lock but missing from the store (run cmk sync to rebuild)", name)
		}
	}
}
