package cmk

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestExpandVars(t *testing.T) {
	vars := map[string]string{
		"PROJECT_ROOT": "/proj",
		"DEPS_DIR":     "${PROJECT_ROOT}/.deps",
	}
	cases := map[string]string{
		"${PROJECT_ROOT}/bin":   "/proj/bin",
		"${DEPS_DIR}/zlib":      "/proj/.deps/zlib",
		"${UNKNOWN_XYZ}/x":      "${UNKNOWN_XYZ}/x",
		"plain":                 "plain",
		"a${PROJECT_ROOT}b}":    "a/projb}",
		"${PROJECT_ROOT":        "${PROJECT_ROOT",
		"-Dfmt_ROOT=${DEP_ABC}": "-Dfmt_ROOT=${DEP_ABC}",
	}
	for in, want := range cases {
		if got := expandVars(in, vars); got != want {
			t.Errorf("expandVars(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCobraBuildConvenienceParsing(t *testing.T) {
	command := newBuildCommand()
	if err := command.ParseFlags([]string{"-pminimal", "-cAsan", "-j8", "-iv", "-tfoo", "--target=bar"}); err != nil {
		t.Fatal(err)
	}
	config, _ := command.Flags().GetString("config")
	preset, _ := command.Flags().GetString("preset")
	jobs, _ := command.Flags().GetInt("jobs")
	interactive, _ := command.Flags().GetBool("interactive")
	verbose, _ := command.Flags().GetBool("verbose")
	targets, _ := command.Flags().GetStringArray("target")
	if preset != "minimal" || config != "Asan" || jobs != 8 || !interactive || !verbose {
		t.Fatalf("flags parsed as preset=%q config=%q jobs=%d interactive=%v verbose=%v", preset, config, jobs, interactive, verbose)
	}
	if !eqStrings(targets, []string{"foo", "bar"}) {
		t.Fatalf("targets = %v, want [foo bar]", targets)
	}
}

func TestCobraPassthrough(t *testing.T) {
	var positional, passthrough []string
	command := &cobra.Command{
		Use: "test",
		Run: func(command *cobra.Command, args []string) {
			positional, passthrough = splitPassthrough(command, args)
		},
	}
	command.SetArgs([]string{"target", "--", "--native"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if !eqStrings(positional, []string{"target"}) || !eqStrings(passthrough, []string{"--native"}) {
		t.Fatalf("positional=%v passthrough=%v", positional, passthrough)
	}
}

func TestCobraVersionCompatibility(t *testing.T) {
	for _, args := range [][]string{{"--version"}, {"-V"}, {"version"}} {
		var stdout, stderr bytes.Buffer
		if code := Run(args, &stdout, &stderr, "1.2.3"); code != 0 {
			t.Fatalf("Run(%v) returned %d: %s", args, code, stderr.String())
		}
		if stdout.String() != "cmk 1.2.3\n" {
			t.Fatalf("Run(%v) output %q", args, stdout.String())
		}
	}
}

func TestCMakeBuildArgsMultipleTargets(t *testing.T) {
	got := cmakeBuildArgs("build", "Asan", 8, []string{"a", "b"}, true, true, []string{"--explain"})
	want := []string{
		"--build", "build", "-j", "8",
		"--config", "Asan",
		"--target", "a",
		"--target", "b",
		"--clean-first",
		"--verbose",
		"--",
		"--explain",
	}
	if !eqStrings(got, want) {
		t.Fatalf("cmakeBuildArgs = %v, want %v", got, want)
	}
}

func TestJoinRegexAlternatives(t *testing.T) {
	if got := joinRegexAlternatives(nil); got != "" {
		t.Fatalf("empty pattern = %q", got)
	}
	if got := joinRegexAlternatives([]string{"foo"}); got != "foo" {
		t.Fatalf("single pattern = %q", got)
	}
	if got := joinRegexAlternatives([]string{"foo", "bar.*"}); got != "(foo)|(bar.*)" {
		t.Fatalf("multi pattern = %q", got)
	}
}

func TestTopoOrder(t *testing.T) {
	deps := map[string]*DepCfg{
		"a": {Script: "a.sh"},
		"b": {Script: "b.sh", Needs: []string{"a"}},
		"c": {Script: "c.sh", Needs: []string{"b", "a"}},
	}
	order, err := topoOrder(deps, nil)
	if err != nil {
		t.Fatal(err)
	}
	pos := map[string]int{}
	for i, n := range order {
		pos[n] = i
	}
	if !(pos["a"] < pos["b"] && pos["b"] < pos["c"]) {
		t.Errorf("bad order: %v", order)
	}

	// want subset pulls in needs
	order, err = topoOrder(deps, []string{"b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != 2 || order[0] != "a" || order[1] != "b" {
		t.Errorf("subset order: %v", order)
	}

	// cycle detection
	deps["a"].Needs = []string{"c"}
	if _, err := topoOrder(deps, nil); err == nil {
		t.Error("expected cycle error")
	}
}

func TestNeedsClosure(t *testing.T) {
	deps := map[string]*DepCfg{
		"fmt":    {Script: "fmt.sh"},
		"spdlog": {Script: "spdlog.sh", Needs: []string{"fmt"}},
		"fdb":    {Script: "fdb.sh", Needs: []string{"spdlog"}},
		"other":  {Script: "other.sh"},
	}
	// transitive: fdb -> spdlog -> fmt, but never "other" or fdb itself
	got := needsClosure(deps, "fdb")
	if len(got) != 2 || got[0] != "fmt" || got[1] != "spdlog" {
		t.Errorf("closure(fdb) = %v, want [fmt spdlog]", got)
	}
	// no needs -> empty, NOT all deps
	if got := needsClosure(deps, "fmt"); len(got) != 0 {
		t.Errorf("closure(fmt) = %v, want []", got)
	}
}

func TestGlobMatch(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"third_party/**", "third_party/x/y.cc", true},
		{"third_party/**", "src/a.cc", false},
		{"*.pb.h", "gen/deep/foo.pb.h", true},
		{"*.pb.h", "gen/foo.pb.cc", false},
		{"src/*.cc", "src/a.cc", true},
		{"src/*.cc", "src/sub/a.cc", false},
		{"**/test_*.cc", "a/b/test_x.cc", true},
		{"**/test_*.cc", "test_x.cc", true},
	}
	for _, c := range cases {
		if got := globMatch(c.pattern, c.path); got != c.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

func TestIgnored(t *testing.T) {
	patterns := []string{"third_party/**", "*.pb.h", "src/gen/*.cc"}
	cases := map[string]bool{
		"third_party/fmt/format.cc": true,
		"proto/api.pb.h":            true,
		"src/gen/lexer.cc":          true,
		"src/gen/sub/lexer.cc":      false,
		"src/main.cc":               false,
	}
	for path, want := range cases {
		if got := ignored(path, patterns); got != want {
			t.Errorf("ignored(%q) = %v, want %v", path, got, want)
		}
	}
	if ignored("src/main.cc", nil) {
		t.Error("nil patterns must ignore nothing")
	}
}

func TestDepStampInvalidation(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "build.sh")
	if err := os.WriteFile(script, []byte("make\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	d := &DepCfg{Script: "build.sh"}

	stamp := func(d *DepCfg, tcID string, ns map[string]string, patches, extras []string) string {
		s, err := depStamp(root, "x", d, tcID, nil, ns, patches, extras)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}

	s1 := stamp(d, "tc1", nil, nil, nil)
	if s1 != stamp(d, "tc1", nil, nil, nil) {
		t.Error("stamp not deterministic")
	}
	if stamp(d, "tc2", nil, nil, nil) == s1 {
		t.Error("toolchain change must change stamp")
	}
	os.WriteFile(script, []byte("make -j2\n"), 0o755)
	if stamp(d, "tc1", nil, nil, nil) == s1 {
		t.Error("script change must change stamp")
	}
	os.WriteFile(script, []byte("make\n"), 0o755)
	if stamp(d, "tc1", map[string]string{"up": "h1"}, nil, nil) != s1 {
		// needs not declared, so needStamps content is irrelevant
		t.Error("unrelated needStamps must not change stamp")
	}
	d.Needs = []string{"up"}
	if stamp(d, "tc1", map[string]string{"up": "h1"}, nil, nil) ==
		stamp(d, "tc1", map[string]string{"up": "h2"}, nil, nil) {
		t.Error("upstream stamp change must cascade")
	}
	d.Needs = nil

	// env knobs are hashed
	d.Env = map[string]string{"KNOB": "1"}
	withKnob := stamp(d, "tc1", nil, nil, nil)
	if withKnob == s1 {
		t.Error("adding an env knob must change stamp")
	}
	d.Env["KNOB"] = "2"
	if stamp(d, "tc1", nil, nil, nil) == withKnob {
		t.Error("changing an env knob must change stamp")
	}
	d.Env = nil

	// patch content is hashed
	patch := filepath.Join(root, "fix.patch")
	os.WriteFile(patch, []byte("--- a\n+++ b\n"), 0o644)
	withPatch := stamp(d, "tc1", nil, []string{"fix.patch"}, nil)
	if withPatch == s1 {
		t.Error("adding a patch must change stamp")
	}
	os.WriteFile(patch, []byte("--- a\n+++ c\n"), 0o644)
	if stamp(d, "tc1", nil, []string{"fix.patch"}, nil) == withPatch {
		t.Error("editing a patch must change stamp")
	}
	if stamp(d, "tc1", nil, nil, []string{"fix.patch"}) == withPatch {
		t.Error("patch vs extra_input must hash differently")
	}
}

func TestResolveInputGlobs(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "p"), 0o755)
	os.WriteFile(filepath.Join(root, "p", "b.patch"), []byte("b"), 0o644)
	os.WriteFile(filepath.Join(root, "p", "a.patch"), []byte("a"), 0o644)

	got, err := resolveInputGlobs(root, []string{"p/*.patch"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "p/a.patch" || got[1] != "p/b.patch" {
		t.Errorf("glob result: %v", got)
	}
	if _, err := resolveInputGlobs(root, []string{"p/missing-*.patch"}); err == nil {
		t.Error("non-matching pattern must error")
	}
}

func TestAddDefines(t *testing.T) {
	dst := map[string]string{}
	addDefines(dst, []string{
		"-DCMAKE_BUILD_TYPE=Debug",
		"-DZLIB_ROOT:PATH=/x/.deps/zlib",
		"-DFOO=a=b",
	})
	if dst["CMAKE_BUILD_TYPE"] != "Debug" || dst["ZLIB_ROOT"] != "/x/.deps/zlib" || dst["FOO"] != "a=b" {
		t.Errorf("addDefines: %v", dst)
	}
}

func TestLockRoundTrip(t *testing.T) {
	root := t.TempDir()
	lk := &Lock{
		Toolchains: map[string]*LockToolchain{
			"linux-x86_64": {
				Selector:       "libcxx@22.1.8-1",
				Target:         "x86_64-unknown-linux-gnu",
				ManifestSHA256: strings.Repeat("a", 64),
				ArtifactSHA256: strings.Repeat("b", 64),
			},
			"macos-aarch64": {
				Selector:       "default@22.1.8-1",
				Target:         "arm64-apple-darwin",
				ManifestSHA256: strings.Repeat("c", 64),
				ArtifactSHA256: strings.Repeat("d", 64),
			},
		},
		Deps: map[string]*LockDep{
			"fdb":  {Git: "https://x/y.git", Ref: "release-7.4", Commit: "0123456789012345678901234567890123456789", Stamps: map[string]string{"linux-x86_64": "aabbccdd", "macos-aarch64": "eeff0011"}},
			"zlib": {Stamps: map[string]string{"linux-x86_64": "deadbeef00112233"}}, // url dep: stamp only
		},
	}
	if err := saveLock(root, lk); err != nil {
		t.Fatal(err)
	}
	got, err := loadLock(root)
	if err != nil {
		t.Fatal(err)
	}
	if got.Toolchains["linux-x86_64"].Selector != "libcxx@22.1.8-1" || got.Toolchains["linux-x86_64"].Target != "x86_64-unknown-linux-gnu" || got.Toolchains["macos-aarch64"].Selector != "default@22.1.8-1" || got.Deps["fdb"] == nil || got.Deps["fdb"].Commit != lk.Deps["fdb"].Commit {
		t.Errorf("round trip mismatch: %+v", got)
	}
	if got.Deps["fdb"].Stamps["linux-x86_64"] != "aabbccdd" || got.Deps["fdb"].Stamps["macos-aarch64"] != "eeff0011" || got.Deps["zlib"] == nil || got.Deps["zlib"].Stamps["linux-x86_64"] != "deadbeef00112233" {
		t.Errorf("stamp round trip mismatch: %+v", got)
	}
}

func TestLegacyToolchainLockMigratesByTargetPlatform(t *testing.T) {
	root := t.TempDir()
	legacy := `schema = 1

[toolchain]
selector = "default@22.1.8-1"
target = "arm64-apple-darwin"
manifest_sha256 = "manifest"
artifact_sha256 = "artifact"

[deps.fmt]
stamp = "legacy-stamp"
`
	if err := os.WriteFile(filepath.Join(root, lockFileName), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	lk, err := loadLock(root)
	if err != nil {
		t.Fatal(err)
	}
	if !lk.dirty || lk.Toolchains["macos-aarch64"] == nil || lk.Toolchains["macos-aarch64"].Selector != "default@22.1.8-1" || lk.Deps["fmt"].Stamps["macos-aarch64"] != "legacy-stamp" {
		t.Fatalf("legacy lock was not migrated: %#v", lk)
	}
	if err := saveLock(root, lk); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(root, lockFileName))
	if err != nil {
		t.Fatal(err)
	}
	text := string(contents)
	if !strings.Contains(text, "schema = 2") || !strings.Contains(text, "[toolchains.macos-aarch64]") || !strings.Contains(text, "[deps.fmt.stamps]") || strings.Contains(text, "\n[toolchain]\n") || strings.Contains(text, "stamp = \"legacy-stamp\"") {
		t.Fatalf("unexpected migrated lock:\n%s", text)
	}
}

func TestStorePaths(t *testing.T) {
	t.Setenv("CMK_STORE_DIR", "/store")
	stamp := strings.Repeat("ab", 32)
	if got := entryDir("fmt", stamp); got != "/store/fmt-abababababababab" {
		t.Errorf("entryDir: %q", got)
	}
	p := &Project{Lock: &Lock{Deps: map[string]*LockDep{"fmt": {Stamps: map[string]string{hostPlatform(runtime.GOOS, runtime.GOARCH): stamp}}}}}
	pfx, err := p.depPrefix("fmt")
	if err != nil || pfx != "/store/fmt-abababababababab/prefix" {
		t.Errorf("depPrefix: %q, %v", pfx, err)
	}
	if _, err := p.depPrefix("missing"); err == nil {
		t.Error("unsynced dep must error")
	}
}

func TestToolchainCmakeArgs(t *testing.T) {
	tc := &Toolchain{CC: "/tc/bin/clang", CXX: "/tc/bin/clang++", File: "/tc/toolchain.cmake"}
	if got := tc.cmakeArgs(nil); len(got) != 1 || got[0] != "-DCMAKE_TOOLCHAIN_FILE=/tc/toolchain.cmake" {
		t.Errorf("toolchain file not preferred: %v", got)
	}
	// project brings a vcpkg toolchain file -> chainload only (the
	// chainloaded toolchain.cmake sets CC/CXX, so no explicit compiler vars)
	user := []string{"-DCMAKE_TOOLCHAIN_FILE=/vcpkg/scripts/buildsystems/vcpkg.cmake"}
	got := tc.cmakeArgs(user)
	if len(got) != 1 || got[0] != "-DVCPKG_CHAINLOAD_TOOLCHAIN_FILE=/tc/toolchain.cmake" {
		t.Errorf("vcpkg toolchain file must add chainload only: %v", got)
	}
	// a non-vcpkg toolchain file -> compiler vars only, no chainload
	if got := tc.cmakeArgs([]string{"-DCMAKE_TOOLCHAIN_FILE=/custom/cross.cmake"}); len(got) != 2 {
		t.Errorf("non-vcpkg toolchain file must not add chainload: %v", got)
	}
	// user already set a chainload file -> don't override it
	if definesVar(tc.cmakeArgs([]string{
		"-DCMAKE_TOOLCHAIN_FILE=/vcpkg/vcpkg.cmake",
		"-DVCPKG_CHAINLOAD_TOOLCHAIN_FILE=/mine.cmake",
	}), "VCPKG_CHAINLOAD_TOOLCHAIN_FILE") {
		t.Error("must not inject chainload when user already set one")
	}
	// typed define still detected
	if !definesVar([]string{"-DCMAKE_TOOLCHAIN_FILE:FILEPATH=/x.cmake"}, "CMAKE_TOOLCHAIN_FILE") {
		t.Error("typed -D form not detected")
	}
	// toolchain without a file (old artifact / system compiler)
	tc.File = ""
	if got := tc.cmakeArgs(nil); len(got) != 2 || got[1] != "-DCMAKE_CXX_COMPILER=/tc/bin/clang++" {
		t.Errorf("fallback without file: %v", got)
	}
}

func TestLauncherArgs(t *testing.T) {
	if got := launcherArgs(""); got != nil {
		t.Errorf("empty launcher must inject nothing: %v", got)
	}
	if got := launcherArgs("definitely-not-a-real-binary-xyz"); got != nil {
		t.Errorf("missing launcher must warn and inject nothing: %v", got)
	}
	// "sh" is reliably on PATH; assert both -D vars resolve to its abs path.
	got := launcherArgs("sh")
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh on PATH")
	}
	if len(got) != 2 || got[0] != "-DCMAKE_C_COMPILER_LAUNCHER="+sh || got[1] != "-DCMAKE_CXX_COMPILER_LAUNCHER="+sh {
		t.Errorf("launcherArgs(sh) = %v", got)
	}
}

func TestCcacheEnv(t *testing.T) {
	t.Setenv("CCACHE_BASEDIR", "")
	t.Setenv("CCACHE_NOHASHDIR", "")
	p := &Project{Root: "/proj", Cfg: &Config{}}

	p.Cfg.Configure.CompilerLauncher = "sccache"
	if got := p.ccacheEnv(); got != nil {
		t.Errorf("non-ccache launcher must not set ccache env: %v", got)
	}
	p.Cfg.Configure.CompilerLauncher = "ccache"
	got := p.ccacheEnv()
	if got["CCACHE_BASEDIR"] != "/proj" || got["CCACHE_NOHASHDIR"] != "true" {
		t.Errorf("ccache env: %v", got)
	}
	// an explicit CCACHE_BASEDIR in the environment is respected
	t.Setenv("CCACHE_BASEDIR", "/elsewhere")
	if _, set := p.ccacheEnv()["CCACHE_BASEDIR"]; set {
		t.Error("must defer to an env-provided CCACHE_BASEDIR")
	}
}

func TestIsMultiConfig(t *testing.T) {
	cases := map[string]bool{
		"":                      false,
		"Ninja":                 false,
		"Unix Makefiles":        false,
		"Ninja Multi-Config":    true,
		"Xcode":                 true,
		"Visual Studio 17 2022": true,
	}
	for gen, want := range cases {
		cfg := &Config{Configure: ConfigureCfg{Generator: gen}}
		if got := isMultiConfig(cfg, nil); got != want {
			t.Errorf("isMultiConfig(%q) = %v, want %v", gen, got, want)
		}
	}
	cfg := &Config{Configure: ConfigureCfg{Generator: "Ninja Multi-Config"}}
	if isMultiConfig(cfg, &PresetCfg{Generator: "Ninja"}) {
		t.Error("preset generator must override the global multi-config generator")
	}
	cfg.Configure.Generator = "Ninja"
	if !isMultiConfig(cfg, &PresetCfg{Generator: "Ninja Multi-Config"}) {
		t.Error("preset generator must override the global single-config generator")
	}
}

func TestYAMLConfigMatrix(t *testing.T) {
	root := t.TempDir()
	body := `version: 1
toolchain:
  default: default
  linux: libcxx
cmake:
  generator: Ninja Multi-Config
  default-preset: default
  default-configuration: Debug
  variables:
    ENABLE_LOGGING: true
  presets:
    default:
      build-dir: build/default
    minimal:
      build-dir: build/minimal
      default-configuration: Asan
      configurations: [Asan]
      variables:
        ENABLE_OPTIONAL: false
    single:
      build-dir: build/single
      generator: Ninja
      variables:
        CMAKE_BUILD_TYPE: Release
  configurations:
    - name: Debug
    - name: Asan
      inherits: Debug
      compile: [-fsanitize=address]
      link: [-fsanitize=address]
`
	if err := os.WriteFile(filepath.Join(root, configFileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(effectiveConfigurations(cfg, nil), ","); got != "Debug,Asan" {
		t.Fatalf("configurations = %s", got)
	}
	if cfg.Configure.Presets["minimal"].BuildDir != "build/minimal" || cfg.Toolchain.selectorFor("linux", "x86_64") != "libcxx" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	p := &Project{Root: root, Cfg: cfg, Lock: &Lock{}, BuildDirs: map[string]string{}}
	dir, configuration, err := p.resolveVariant("", "minimal", "Asan")
	if err != nil || dir != filepath.Join(root, "build/minimal") || configuration != "Asan" {
		t.Fatalf("variant = %q, %q, %v", dir, configuration, err)
	}
	if _, _, err := p.resolveVariant("", "minimal", "Debug"); err == nil {
		t.Fatal("preset accepted a configuration outside its selected subset")
	}
	if _, _, err := p.resolveVariant("", "single", "Asan"); err == nil {
		t.Fatal("single-config preset accepted --config")
	}
	_, content := configFlagsFile(p)
	if !strings.Contains(content, `cmk_set_config_flag(CMAKE_CXX_FLAGS_ASAN "CMAKE_CXX_FLAGS_DEBUG" "-fsanitize=address"`) {
		t.Fatalf("configuration flags missing:\n%s", content)
	}
}

func TestYAMLConfigValidation(t *testing.T) {
	for name, body := range map[string]string{
		"unknown field": `version: 1
cmake:
  typo: true
`,
		"inheritance cycle": `version: 1
cmake:
  generator: Ninja Multi-Config
  configurations:
    - {name: A, inherits: B}
    - {name: B, inherits: A}
`,
		"duplicate build": `version: 1
cmake:
  presets:
    a: {build-dir: build}
    b: {build-dir: build}
`,
		"cmk-owned variable": `version: 1
cmake:
  variables:
    CMAKE_CONFIGURATION_TYPES: Debug
`,
		"unknown preset configuration": `version: 1
cmake:
  generator: Ninja Multi-Config
  presets:
    default:
      configurations: [Unknown]
`,
		"preset inheritance cycle": `version: 1
cmake:
  presets:
    first: {inherits: second}
    second: {inherits: first}
`,
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, configFileName), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := loadConfig(root); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
}

func TestPresetInheritance(t *testing.T) {
	root := t.TempDir()
	body := `version: 1
cmake:
  default-preset: child
  presets:
    base:
      build-dir: build/base
      build-type: Debug
      variables:
        ENABLE_ASAN: true
        MODE: base
      args: [-DPARENT=ON]
    child:
      inherits: base
      variables:
        MODE: child
        ENABLE_COVERAGE: true
      args: [-DCHILD=ON]
`
	if err := os.WriteFile(filepath.Join(root, configFileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	child := cfg.Configure.Presets["child"]
	if child.BuildDir != "build/child" || child.BuildType != "Debug" {
		t.Fatalf("child paths/type = %q/%q", child.BuildDir, child.BuildType)
	}
	if child.Variables["ENABLE_ASAN"] != true || child.Variables["MODE"] != "child" || child.Variables["ENABLE_COVERAGE"] != true {
		t.Fatalf("child variables = %#v", child.Variables)
	}
	if got := strings.Join(child.Args, ","); got != "-DPARENT=ON,-DCHILD=ON" {
		t.Fatalf("child args = %s", got)
	}
}

func TestWriteUserPresetsMatrix(t *testing.T) {
	root := t.TempDir()
	cfg := &Config{Configure: ConfigureCfg{
		Generator:            "Ninja Multi-Config",
		DefaultPreset:        "default",
		DefaultConfiguration: "Debug",
		Presets: map[string]*PresetCfg{
			"default": {
				Name:                 "default",
				BuildDir:             "build/default",
				Configurations:       []string{"Release"},
				DefaultConfiguration: "Release",
			},
			"minimal": {Name: "minimal", BuildDir: "build/minimal", Generator: "Ninja", BuildType: "Release"},
		},
		Configurations: []*ConfigurationCfg{{Name: "Debug"}, {Name: "Release"}},
	}}
	if err := normalizeConfig(cfg); err != nil {
		t.Fatal(err)
	}
	p := &Project{Root: root, Cfg: cfg, Lock: &Lock{}}
	tc := &Toolchain{CC: "/opt/clang/bin/clang", CXX: "/opt/clang/bin/clang++"}
	if err := writeUserPresets(p, tc); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "CMakeUserPresets.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got presetsFile
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.ConfigurePresets) != 2 || len(got.BuildPresets) != 2 || len(got.TestPresets) != 2 {
		t.Fatalf("preset matrix sizes = %d/%d/%d", len(got.ConfigurePresets), len(got.BuildPresets), len(got.TestPresets))
	}
	if got.BuildPresets[0].Name != "cmk-default-Release" || got.ConfigurePresets[0].CacheVariables["CMAKE_CONFIGURATION_TYPES"] != "Release" {
		t.Fatalf("preset configuration subset was not preserved: %+v / %+v", got.ConfigurePresets[0], got.BuildPresets[0])
	}
	if got.ConfigurePresets[1].Generator != "Ninja" ||
		got.ConfigurePresets[1].CacheVariables["CMAKE_BUILD_TYPE"] != "Release" ||
		got.BuildPresets[1].Name != "cmk-minimal" {
		t.Fatalf("preset generator override was not preserved: %+v / %+v", got.ConfigurePresets[1], got.BuildPresets[1])
	}
}

func TestInstallPrefix(t *testing.T) {
	p := &Project{Root: "/proj", Cfg: &Config{}}
	// nothing configured -> empty (respect the configure-time prefix)
	if got, _ := p.installPrefix(""); got != "" {
		t.Errorf("no prefix -> empty, got %q", got)
	}
	// [install] prefix: ${PROJECT_ROOT} expands
	p.Cfg.Install.Prefix = "${PROJECT_ROOT}/stage"
	if got, _ := p.installPrefix(""); got != "/proj/stage" {
		t.Errorf("config prefix expansion: %q", got)
	}
	// relative [install] prefix is taken from the project root
	p.Cfg.Install.Prefix = "out"
	if got, _ := p.installPrefix(""); got != "/proj/out" {
		t.Errorf("relative config prefix: %q", got)
	}
	// an absolute --prefix flag overrides the config
	if got, _ := p.installPrefix("/abs/dest"); got != "/abs/dest" {
		t.Errorf("flag prefix overrides: %q", got)
	}
}

func TestEnvName(t *testing.T) {
	for in, want := range map[string]string{
		"zlib": "ZLIB", "open-ssl": "OPEN_SSL", "fdb.core": "FDB_CORE",
	} {
		if got := envName(in); got != want {
			t.Errorf("envName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMergeUserPresets(t *testing.T) {
	// A user-authored file: a higher schema version, a key cmk doesn't
	// model (must survive), the user's own preset, a stale cmk-* preset
	// from a prior run, and a duplicate name to collapse.
	existing := []byte(`{
	  "version": 6,
	  "include": ["extra.json"],
	  "configurePresets": [
	    {"name": "mydebug", "generator": "Ninja"},
	    {"name": "cmk-old", "generator": "Ninja"},
	    {"name": "dup", "generator": "Ninja"},
	    {"name": "dup", "generator": "Ninja"}
	  ],
	  "buildPresets": [{"name": "mydebug", "configurePreset": "mydebug"}]
	}`)
	out := presetsFile{
		Version:          4,
		ConfigurePresets: []configurePreset{{Name: "cmk-debug", Generator: "Ninja"}},
		BuildPresets:     []buildPreset{{Name: "cmk-debug", ConfigurePreset: "cmk-debug"}},
		TestPresets:      []testPreset{{Name: "cmk-debug", ConfigurePreset: "cmk-debug"}},
		Vendor:           map[string]any{"cmk": map[string]any{"generated": true}},
	}

	data, err := mergeUserPresets(existing, out)
	if err != nil {
		t.Fatalf("mergeUserPresets: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}

	// version is only raised, never lowered: 6 stays 6.
	if v, _ := doc["version"].(float64); int(v) != 6 {
		t.Errorf("version = %v, want 6 (must not be lowered to 4)", doc["version"])
	}
	// Unmodelled keys survive untouched.
	if inc, ok := doc["include"].([]any); !ok || len(inc) != 1 || inc[0] != "extra.json" {
		t.Errorf("include not preserved: %v", doc["include"])
	}
	// The cmk vendor marker must NOT be stamped onto a merged user file,
	// or the next run would overwrite it wholesale.
	if _, ok := doc["vendor"]; ok {
		t.Errorf("merge must not add a vendor marker, got %v", doc["vendor"])
	}

	names := func(key string) []string {
		var got []string
		for _, it := range doc[key].([]any) {
			got = append(got, it.(map[string]any)["name"].(string))
		}
		return got
	}
	// User preset kept, dup collapsed once, stale cmk-old dropped, fresh
	// cmk-debug appended at the end.
	if got := names("configurePresets"); !eqStrings(got, []string{"mydebug", "dup", "cmk-debug"}) {
		t.Errorf("configurePresets = %v, want [mydebug dup cmk-debug]", got)
	}
	if got := names("buildPresets"); !eqStrings(got, []string{"mydebug", "cmk-debug"}) {
		t.Errorf("buildPresets = %v, want [mydebug cmk-debug]", got)
	}
	// testPresets key was absent; merge creates it with just cmk's entry.
	if got := names("testPresets"); !eqStrings(got, []string{"cmk-debug"}) {
		t.Errorf("testPresets = %v, want [cmk-debug]", got)
	}
}

func eqStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
