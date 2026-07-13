package cmk

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
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
}

type clangupResolveResult struct {
	Schema         string `json:"schema"`
	Channel        string `json:"channel"`
	Version        string `json:"version"`
	Release        int    `json:"release"`
	Target         string `json:"target"`
	ManifestSHA256 string `json:"manifest_sha256"`
	ArtifactSHA256 string `json:"artifact_sha256"`
	Driver         struct {
		CXXStdlib struct {
			Name string `json:"name"`
		} `json:"cxx_stdlib"`
	} `json:"driver"`
	Install *struct {
		Prefix        string            `json:"prefix"`
		CC            string            `json:"cc"`
		CXX           string            `json:"cxx"`
		ToolchainFile string            `json:"toolchain_file"`
		Tools         map[string]string `json:"tools"`
	} `json:"install"`
}

func exactSelector(result *clangupResolveResult) string {
	return fmt.Sprintf("%s@%s-%d", result.Channel, result.Version, result.Release)
}

func effectiveSelector(selector string, pin *LockToolchain) string {
	if pin == nil || pin.Selector == "" {
		return selector
	}
	if strings.Contains(selector, "@") {
		if selector == pin.Selector {
			return selector
		}
		return selector
	}
	if strings.HasPrefix(pin.Selector, selector+"@") {
		return pin.Selector
	}
	return selector
}

func toolchainFromResult(result *clangupResolveResult) (*Toolchain, error) {
	if result.Schema != "clangup.resolve/v1" || result.Channel == "" ||
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
		CXXStdlib: result.Driver.CXXStdlib.Name,
	}, nil
}

// resolveToolchain resolves and installs the requested channel through
// clangup's stable JSON consumer interface. A floating channel is pinned to
// the exact release and target returned by clangup.
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
	var result clangupResolveResult
	if err := runClangupJSON("ensure", requested, &result); err != nil {
		if !strings.Contains(err.Error(), "channel index is not cached") {
			return nil, false, err
		}
		if updateErr := runClangupUpdate(); updateErr != nil {
			return nil, false, updateErr
		}
		if err := runClangupJSON("ensure", requested, &result); err != nil {
			return nil, false, err
		}
	}
	tc, err = toolchainFromResult(&result)
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
	var resolved clangupResolveResult
	if err := runClangupJSON("resolve", requested, &resolved); err != nil {
		return nil, err
	}
	var path struct {
		Schema  string `json:"schema"`
		Prefix  string `json:"prefix"`
		Channel string `json:"channel"`
		Version string `json:"version"`
		Release int    `json:"release"`
		Target  string `json:"target"`
	}
	if err := runClangupJSON("path", exactSelector(&resolved), &path); err != nil {
		return nil, nil
	}
	resolved.Install = &struct {
		Prefix        string            `json:"prefix"`
		CC            string            `json:"cc"`
		CXX           string            `json:"cxx"`
		ToolchainFile string            `json:"toolchain_file"`
		Tools         map[string]string `json:"tools"`
	}{
		Prefix: path.Prefix,
		CC:     filepath.Join(path.Prefix, "bin", "clang"),
		CXX:    filepath.Join(path.Prefix, "bin", "clang++"),
	}
	if candidate := filepath.Join(path.Prefix, "toolchain.cmake"); fileExists(candidate) {
		resolved.Install.ToolchainFile = candidate
	}
	return toolchainFromResult(&resolved)
}

func clangupBin() string {
	if path, err := exec.LookPath("clangup"); err == nil {
		return path
	}
	home, err := os.UserHomeDir()
	if err == nil {
		path := filepath.Join(home, ".local", "bin", "clangup")
		if fileExists(path) {
			return path
		}
	}
	return ""
}

func runClangupJSON(command, selector string, output any) error {
	bin := clangupBin()
	if bin == "" {
		return fmt.Errorf("clangup is required to resolve toolchain %s", selector)
	}
	cmd := exec.Command(bin, command, selector, "--format=json")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("clangup %s %s: %s", command, selector, message)
	}
	if err := json.Unmarshal(stdout.Bytes(), output); err != nil {
		return fmt.Errorf("decode clangup %s output: %w", command, err)
	}
	return nil
}

func runClangupUpdate() error {
	bin := clangupBin()
	if bin == "" {
		return fmt.Errorf("clangup is required to update the channel index")
	}
	cmd := exec.Command(bin, "update")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("clangup update: %w", err)
	}
	return nil
}

func systemToolchain() (*Toolchain, error) {
	cc := os.Getenv("CC")
	cxx := os.Getenv("CXX")
	if cc == "" || cxx == "" {
		for _, pair := range [][2]string{{"clang", "clang++"}, {"gcc", "g++"}, {"cc", "c++"}} {
			ccPath, err1 := exec.LookPath(pair[0])
			cxxPath, err2 := exec.LookPath(pair[1])
			if err1 == nil && err2 == nil {
				cc, cxx = ccPath, cxxPath
				break
			}
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
		ID:  "system:" + verLine,
		CC:  cc,
		CXX: cxx,
	}, nil
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
