package cmk

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/zhscn/clangup/internal/clangup"
	"github.com/zhscn/clangup/internal/platform"
)

// Toolchain is the compiler set returned by clangup's consumer interface.
// ID includes the exact channel release, target and artifact digest so
// dependency stamps change whenever the selected toolchain changes.
type Toolchain struct {
	ID        string
	Selector  string
	Root      string
	CC        string
	CXX       string
	File      string
	CXXStdlib string
	Tools     map[string]string
}

var (
	clangupEnsure    = clangup.Ensure
	clangupLocate    = clangup.Locate
	clangupSyncIndex = clangup.SyncIndex
)

func exactSelector(result *clangup.ResolveResult) string {
	return fmt.Sprintf("%s@%s-%d", result.Channel, result.Version, result.Release)
}

func effectiveSelector(selector string, pin *LockToolchain) string {
	if pin == nil || pin.Selector == "" {
		return selector
	}
	if strings.Contains(selector, "@") {
		return selector
	}
	if strings.HasPrefix(pin.Selector, selector+"@") {
		return pin.Selector
	}
	return selector
}

func toolchainFromResult(result *clangup.ResolveResult) (*Toolchain, error) {
	if result == nil || result.Channel == "" ||
		result.Version == "" || result.Release < 1 || result.Target == "" ||
		result.Install == nil || result.Install.Prefix == "" ||
		result.Install.CC == "" || result.Install.CXX == "" {
		return nil, fmt.Errorf("clangup returned an incomplete ensure result")
	}
	if !fileExists(result.Install.CC) || !fileExists(result.Install.CXX) {
		return nil, fmt.Errorf("clangup toolchain is missing its C/C++ compiler")
	}
	selector := exactSelector(result)
	return &Toolchain{
		ID:        selector + "#" + result.Target + ":sha256:" + result.ArtifactSHA256,
		Selector:  selector,
		Root:      result.Install.Prefix,
		CC:        result.Install.CC,
		CXX:       result.Install.CXX,
		File:      result.Install.ToolchainFile,
		CXXStdlib: result.CXXStdlib(),
		Tools:     result.Install.Tools,
	}, nil
}

// resolveToolchain resolves and installs the requested channel through
// clangup's Go consumer API. A floating channel is pinned to the exact release
// and target returned by clangup.
func resolveToolchain(selector string, lock *Lock) (tc *Toolchain, lockDirty bool, err error) {
	if selector == "" {
		tc, err := systemToolchain()
		return tc, false, err
	}
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return nil, false, fmt.Errorf("clangup toolchains are unavailable on %s", runtime.GOOS)
	}
	lockDirty = lock.dirty
	pin := lock.toolchainFor(runtime.GOOS, runtime.GOARCH)
	requested := effectiveSelector(selector, pin)
	target := pin.Target
	if target == "" {
		target = hostTarget()
	}
	result, err := clangupEnsure(requested, target)
	if err != nil {
		return nil, false, err
	}
	tc, err = toolchainFromResult(result)
	if err != nil {
		return nil, false, err
	}
	if pin.Selector != tc.Selector ||
		pin.Target != result.Target ||
		pin.ManifestSHA256 != result.ManifestSHA256 ||
		pin.ArtifactSHA256 != result.ArtifactSHA256 {
		*pin = LockToolchain{
			Selector:       tc.Selector,
			Target:         result.Target,
			ManifestSHA256: result.ManifestSHA256,
			ArtifactSHA256: result.ArtifactSHA256,
		}
		lockDirty = true
	}
	return tc, lockDirty, nil
}

// locateToolchain reports an already installed toolchain without changing
// local state. It is used by doctor; a missing installation returns nil.
func locateToolchain(selector string, lock *Lock) (*Toolchain, error) {
	if selector == "" {
		return systemToolchain()
	}
	requested := effectiveSelector(selector, lock.toolchainFor(runtime.GOOS, runtime.GOARCH))
	target := lock.toolchainFor(runtime.GOOS, runtime.GOARCH).Target
	if target == "" {
		target = hostTarget()
	}
	resolved, err := clangupLocate(requested, target)
	if err != nil {
		return nil, err
	}
	if resolved.Install == nil {
		return nil, nil
	}
	return toolchainFromResult(resolved)
}

func hostTarget() string { return platform.HostTarget() }

func systemToolchain() (*Toolchain, error) {
	cc := os.Getenv("CC")
	cxx := os.Getenv("CXX")
	if cc == "" && cxx == "" {
		for _, pair := range [][2]string{{"clang", "clang++"}, {"gcc", "g++"}, {"cc", "c++"}} {
			if ccPath, err1 := exec.LookPath(pair[0]); err1 == nil {
				if cxxPath, err2 := exec.LookPath(pair[1]); err2 == nil {
					cc, cxx = ccPath, cxxPath
					break
				}
			}
		}
	} else {
		if cc == "" {
			cc = findCompiler(companionCompiler(cxx, true), "clang", "gcc", "cc")
		}
		if cxx == "" {
			cxx = findCompiler(companionCompiler(cc, false), "clang++", "g++", "c++")
		}
	}
	if cc == "" || cxx == "" {
		return nil, fmt.Errorf("no C/C++ compiler found (set CC/CXX or install clang)")
	}
	verLine := "unknown"
	if out, err := exec.Command(cxx, "--version").Output(); err == nil {
		verLine, _, _ = strings.Cut(strings.TrimSpace(string(out)), "\n")
	}
	return &Toolchain{
		ID:    "system:" + verLine,
		CC:    cc,
		CXX:   cxx,
		Tools: map[string]string{},
	}, nil
}

func companionCompiler(compiler string, wantC bool) string {
	base := filepath.Base(compiler)
	if wantC {
		switch base {
		case "clang++":
			return "clang"
		case "g++":
			return "gcc"
		case "c++":
			return "cc"
		}
	} else {
		switch base {
		case "clang":
			return "clang++"
		case "gcc":
			return "g++"
		case "cc":
			return "c++"
		}
	}
	return ""
}

func findCompiler(candidates ...string) string {
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if path, err := exec.LookPath(candidate); err == nil {
			return path
		}
	}
	return ""
}

func (tc *Toolchain) command(name string) (string, error) {
	if path := tc.Tools[name]; path != "" && fileExists(path) {
		return path, nil
	}
	if tc.Root != "" {
		path := filepath.Join(tc.Root, "bin", name)
		if fileExists(path) {
			return path, nil
		}
		return "", fmt.Errorf("toolchain %s does not provide %s", tc.Selector, name)
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%s not found on PATH", name)
	}
	return path, nil
}

// scriptEnv is the toolchain part of a dep script's environment.
func (tc *Toolchain) scriptEnv() []string {
	env := []string{"CC=" + tc.CC, "CXX=" + tc.CXX}
	if tc.File != "" {
		env = append(env, "CMK_TOOLCHAIN_FILE="+tc.File)
	}
	if tc.Root != "" {
		env = append(env, "PATH="+filepath.Join(tc.Root, "bin")+":"+os.Getenv("PATH"))
		// macOS deliberately uses the system ar/ranlib/nm — the darwin
		// toolchain.cmake sets only the compilers — so don't point these at
		// the bundled llvm-* there. Keep in step with toolchainCMakeDarwin.
		if runtime.GOOS != "darwin" {
			for tool, name := range map[string]string{"AR": "llvm-ar", "RANLIB": "llvm-ranlib", "NM": "llvm-nm"} {
				p := filepath.Join(tc.Root, "bin", name)
				if _, err := os.Stat(p); err == nil {
					env = append(env, tool+"="+p)
				}
			}
		}
	}
	sort.Strings(env)
	return env
}

// envMap is scriptEnv as a commandEnv layer, so the configure process
// itself runs inside the toolchain env. For the top-level project these
// are redundant — toolchain.cmake sets the compiler and CMake reads
// CC/CXX only as a fallback when it isn't already set. They are here for
// independent sub-configures (vcpkg ports, ExternalProject) that run their
// own cmake and can't inherit -DCMAKE_TOOLCHAIN_FILE, since CMake has no
// environment variable for a toolchain file. So this env and toolchain.cmake
// are two representations of one toolchain; keep them equivalent.
func (tc *Toolchain) envMap() map[string]string {
	m := map[string]string{}
	for _, kv := range tc.scriptEnv() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			m[k] = v
		}
	}
	return m
}

// cmakeArgs is the toolchain part of `cmk config` injection.
//
//   - No clangup toolchain file (system compiler, or an artifact predating
//     it): inject raw compiler variables.
//   - The project brings no toolchain file of its own: hand it the clangup
//     toolchain file directly.
//   - The project brings its own toolchain file (it would win as the later
//     -DCMAKE_TOOLCHAIN_FILE): for vcpkg, chainload the clangup toolchain —
//     vcpkg.cmake includes it for both the main build and the ports, and it
//     already sets CC/CXX *and* llvm-ar/ranlib/nm, so explicit compiler vars
//     would be redundant. For any other toolchain file, inject compiler
//     variables (nothing else pulls the clangup toolchain in).
func (tc *Toolchain) cmakeArgs(userArgs []string) []string {
	if tc.File == "" {
		return tc.compilerArgs()
	}
	userTC, hasUserTC := lookupDefine(userArgs, "CMAKE_TOOLCHAIN_FILE")
	if !hasUserTC {
		return []string{"-DCMAKE_TOOLCHAIN_FILE=" + tc.File}
	}
	if isVcpkgToolchain(userTC) && !definesVar(userArgs, "VCPKG_CHAINLOAD_TOOLCHAIN_FILE") {
		return []string{"-DVCPKG_CHAINLOAD_TOOLCHAIN_FILE=" + tc.File}
	}
	return tc.compilerArgs()
}

func (tc *Toolchain) compilerArgs() []string {
	return []string{
		"-DCMAKE_C_COMPILER=" + tc.CC,
		"-DCMAKE_CXX_COMPILER=" + tc.CXX,
	}
}

func isVcpkgToolchain(path string) bool {
	return strings.Contains(strings.ToLower(path), "vcpkg")
}

// lookupDefine returns the value of -D<name>[:TYPE]=<value> in args, if present.
func lookupDefine(args []string, name string) (string, bool) {
	for _, a := range args {
		if !strings.HasPrefix(a, "-D") {
			continue
		}
		k, v, ok := strings.Cut(a[2:], "=")
		if !ok {
			continue
		}
		if i := strings.IndexByte(k, ':'); i >= 0 {
			k = k[:i]
		}
		if k == name {
			return v, true
		}
	}
	return "", false
}

// definesVar reports whether args contain -D<name>[:TYPE]=...
func definesVar(args []string, name string) bool {
	_, ok := lookupDefine(args, name)
	return ok
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}
