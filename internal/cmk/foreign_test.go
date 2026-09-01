package cmk

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// foreignTree scaffolds a project without cmk.yaml holding one existing
// CMake build tree with the given cache, chdirs into it, and puts a fake
// cmake on PATH that records its arguments. Nothing else is on PATH, so a
// command that insists on a compiler of cmk's own fails the test loudly.
func foreignTree(t *testing.T, cache string) (root, build, log string) {
	t.Helper()
	root = t.TempDir()
	build = filepath.Join(root, "build")
	bin := filepath.Join(root, "bin")
	for _, dir := range []string{build, bin} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(build, "CMakeCache.txt"), []byte(cache), 0o644); err != nil {
		t.Fatal(err)
	}
	log = filepath.Join(root, "cmake.args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" > " + shellQuote([]string{log}) + "\n"
	if err := os.WriteFile(filepath.Join(bin, "cmake"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	t.Setenv("PATH", bin)
	t.Setenv("CC", "")
	t.Setenv("CXX", "")
	return root, build, log
}

// variantFor is the build-time flag set a command would see for one
// tree: -b <build> [-c <config>] -j <jobs>.
func variantFor(build, config string, jobs int) variantOptions {
	return variantOptions{treeOptions: treeOptions{BuildDir: build, Config: config}, Jobs: jobs}
}

func readLog(t *testing.T, log string) string {
	t.Helper()
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSuffix(string(data), "\n")
}

const multiConfigCache = "CMAKE_GENERATOR:INTERNAL=Ninja Multi-Config\n" +
	"CMAKE_CONFIGURATION_TYPES:STRING=Debug;Release\n" +
	"CMAKE_DEFAULT_BUILD_TYPE:STRING=Release\n"

const singleConfigCache = "CMAKE_GENERATOR:INTERNAL=Ninja\n" +
	"CMAKE_BUILD_TYPE:STRING=Debug\n"

func TestBuildUsesExistingCMakeTreeWithoutManagedToolchain(t *testing.T) {
	_, build, log := foreignTree(t, singleConfigCache)

	if err := cmdBuild([]string{"app"}, []string{"-v"}, buildOptions{variantOptions: variantFor(build, "", 3)}); err != nil {
		t.Fatal(err)
	}
	want := "--build " + build + " -j 3 --target app -- -v"
	if got := readLog(t, log); got != want {
		t.Fatalf("cmake arguments = %q, want %q", got, want)
	}
	if fileExists(filepath.Join(build, injectionStampFile)) {
		t.Fatal("pass-through build wrote a cmk injection stamp")
	}
}

// A foreign multi-config tree builds in the configuration its own cache
// names as the default, so build, run, test and lint all address the same
// one — there is no cmake.default-configuration for cmk to consult here.
func TestBuildSelectsForeignDefaultConfiguration(t *testing.T) {
	_, build, log := foreignTree(t, multiConfigCache)

	if err := cmdBuild(nil, nil, buildOptions{variantOptions: variantFor(build, "", 1)}); err != nil {
		t.Fatal(err)
	}
	if want := "--build " + build + " -j 1 --config Release"; readLog(t, log) != want {
		t.Fatalf("cmake arguments = %q, want %q", readLog(t, log), want)
	}

	if err := cmdBuild(nil, nil, buildOptions{variantOptions: variantFor(build, "Debug", 1)}); err != nil {
		t.Fatal(err)
	}
	if want := "--build " + build + " -j 1 --config Debug"; readLog(t, log) != want {
		t.Fatalf("explicit --config: cmake arguments = %q, want %q", readLog(t, log), want)
	}
}

func TestForeignConfigRejectsUnusableConfigurations(t *testing.T) {
	_, build, _ := foreignTree(t, multiConfigCache)

	if _, err := foreignConfig(build, "Asan"); err == nil || !strings.Contains(err.Error(), "known: Debug, Release") {
		t.Fatalf("unknown configuration error = %v", err)
	}

	single := filepath.Join(t.TempDir(), "build")
	if err := os.MkdirAll(single, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(single, "CMakeCache.txt"), []byte(singleConfigCache), 0o644); err != nil {
		t.Fatal(err)
	}
	if config, err := foreignConfig(single, ""); err != nil || config != "" {
		t.Fatalf("single-config tree = (%q, %v), want (\"\", nil)", config, err)
	}
	if _, err := foreignConfig(single, "Release"); err == nil || !strings.Contains(err.Error(), "multi-config generator") {
		t.Fatalf("--config on a single-config tree = %v", err)
	}
}

// A tree cmk did not configure must survive being asked for a file API
// reply or a compile database: a plain in-place reconfigure, no --fresh,
// no injection, no stamp — only the queries and the requested define.
func TestRegenerateForeignLeavesConfigurationAlone(t *testing.T) {
	_, build, log := foreignTree(t, singleConfigCache)
	p, err := openProject()
	if err != nil {
		t.Fatal(err)
	}

	if err := p.treeAt(build, "").regenerate("-DCMAKE_EXPORT_COMPILE_COMMANDS=ON"); err != nil {
		t.Fatal(err)
	}
	if want := "-B " + build + " -DCMAKE_EXPORT_COMPILE_COMMANDS=ON"; readLog(t, log) != want {
		t.Fatalf("cmake arguments = %q, want %q", readLog(t, log), want)
	}
	for _, query := range []string{"codemodel-v2", "cmakeFiles-v1", "toolchains-v1"} {
		if !fileExists(filepath.Join(build, ".cmake/api/v1/query/"+query)) {
			t.Errorf("file API query was not planted: %s", query)
		}
	}
	if fileExists(filepath.Join(build, injectionStampFile)) {
		t.Error("foreign reconfigure wrote a cmk injection stamp")
	}
	if fileExists(filepath.Join(p.Root, "CMakeUserPresets.json")) {
		t.Error("foreign reconfigure generated CMakeUserPresets.json")
	}
}

func TestRegenerateForeignRequiresAConfiguredTree(t *testing.T) {
	root, _, _ := foreignTree(t, singleConfigCache)
	p, err := openProject()
	if err != nil {
		t.Fatal(err)
	}

	err = p.treeAt(filepath.Join(root, "never-configured"), "").regenerate()
	if err == nil || !strings.Contains(err.Error(), "cmake -B") {
		t.Fatalf("error = %v, want advice to configure the tree first", err)
	}
}

// clang-format and clang-tidy come from PATH when no toolchain selector is
// set: formatting and linting must not depend on cmk finding a compiler.
func TestToolResolvesFromPathWithoutACompiler(t *testing.T) {
	root, _, _ := foreignTree(t, singleConfigCache)
	formatter := filepath.Join(root, "bin", "clang-format")
	if err := os.WriteFile(formatter, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := &Project{Root: root, Cfg: &Config{}}

	if path, err := p.tool("clang-format"); err != nil || path != formatter {
		t.Fatalf("tool(clang-format) = (%q, %v), want %q", path, err, formatter)
	}
	if _, err := p.tool("clang-tidy"); err == nil || !strings.Contains(err.Error(), "not found on PATH") {
		t.Fatalf("tool(clang-tidy) = %v, want a PATH error", err)
	}
}

func TestConfiguredToolOverridesToolchainAndResolvesFromProjectRoot(t *testing.T) {
	root := t.TempDir()
	tool := filepath.Join(root, "tools", "clang-format")
	if err := os.MkdirAll(filepath.Dir(tool), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tool, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := &Project{Root: root, Cfg: &Config{Toolchain: ToolchainCfg{"default": "unavailable"}}}

	got, err := p.configuredTool("clang-format", "${PROJECT_ROOT}/tools/clang-format")
	if err != nil || got != tool {
		t.Fatalf("configuredTool = (%q, %v), want %q", got, err, tool)
	}
	got, err = p.configuredTool("clang-format", "tools/clang-format")
	if err != nil || got != tool {
		t.Fatalf("relative configuredTool = (%q, %v), want %q", got, err, tool)
	}
	if _, err := p.configuredTool("clang-tidy", "tools/missing"); err == nil || !strings.Contains(err.Error(), "configured clang-tidy") {
		t.Fatalf("missing configuredTool error = %v", err)
	}
}

// Building an existing tree uses the compiler recorded in its cache, so
// cmk resolves no toolchain of its own — and needs none to be available.
func TestBuildEnvSkipsToolchainForForeignTree(t *testing.T) {
	root, _, _ := foreignTree(t, singleConfigCache)
	p := &Project{Root: root, Cfg: &Config{}}

	env, err := p.buildEnv()
	if err != nil {
		t.Fatalf("buildEnv on a foreign tree = %v, want no toolchain resolution", err)
	}
	for _, entry := range env {
		if strings.HasPrefix(entry, "CMK_TOOLCHAIN_FILE=") {
			t.Errorf("foreign build env carries %q", entry)
		}
	}
	if p.tc != nil {
		t.Error("foreign build env resolved a toolchain")
	}
}
