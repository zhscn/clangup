package cmk

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/zhscn/clangup/internal/clangup"
	"go.yaml.in/yaml/v3"
)

var (
	clangupHostToolchains = clangup.ListHostToolchains
	initLookPath          = exec.LookPath
)

type initOptions struct {
	Force        bool
	Yes          bool
	Toolchain    string
	Launcher     string
	Asan         bool
	Gitignore    bool
	ToolchainSet bool
	LauncherSet  bool
	AsanSet      bool
	GitignoreSet bool
}

type initEnv struct {
	Toolchains         []clangup.InstalledToolchain
	DefaultToolchain   string
	Launcher           string
	IgnoreGlobs        []string
	HasRepoOrGitignore bool
	MissingGitignore   []string
}

type initPlan struct {
	Toolchain       string
	Launcher        string
	Asan            bool
	IgnoreGlobs     []string
	UpdateGitignore bool
}

var cmkGitignoreEntries = []string{
	"build/",
	"compile_commands.json",
	"CMakeUserPresets.json",
	"cmk.dev.yaml",
}

func isTerminalFile(v any) bool {
	f, ok := v.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	return err == nil && (st.Mode()&os.ModeCharDevice) != 0
}

func cmdInit(options initOptions, in io.Reader, errOut io.Writer) error {
	root, err := findProjectRoot()
	if err != nil {
		root, err = os.Getwd()
		if err != nil {
			return err
		}
	}
	interactive := !options.Yes && isTerminalFile(in) && isTerminalFile(errOut)
	return runInit(root, options, in, errOut, interactive)
}

func runInit(root string, options initOptions, in io.Reader, errOut io.Writer, interactive bool) error {
	path := filepath.Join(root, configFileName)
	if _, err := os.Stat(path); err == nil && !options.Force {
		return fmt.Errorf("%s already exists (use --force to overwrite)", path)
	}

	env := detectInitEnv(root)
	plan := initPlan{
		Toolchain:       env.DefaultToolchain,
		Launcher:        env.Launcher,
		Asan:            options.Asan,
		IgnoreGlobs:     env.IgnoreGlobs,
		UpdateGitignore: options.Gitignore,
	}
	if options.ToolchainSet {
		plan.Toolchain = normalizeChoice(options.Toolchain)
	}
	if options.LauncherSet {
		plan.Launcher = normalizeChoice(options.Launcher)
	}

	if interactive {
		if err := promptInitPlan(bufio.NewScanner(in), errOut, env, options, &plan); err != nil {
			return err
		}
	}

	rendered := renderCmkYAML(plan)
	if err := validateRenderedConfig(rendered, root); err != nil {
		return fmt.Errorf("invalid generated %s: %w", configFileName, err)
	}
	if err := os.WriteFile(path, []byte(rendered), 0o644); err != nil {
		return err
	}

	fmt.Fprintf(errOut, "cmk: wrote %s%s\n", path, formatInitSummary(plan))

	if plan.UpdateGitignore {
		added, err := ensureGitignoreEntries(root, cmkGitignoreEntries)
		if err != nil {
			return err
		}
		if len(added) > 0 {
			fmt.Fprintf(errOut, "cmk: updated %s (+%s)\n", filepath.Join(root, ".gitignore"), strings.Join(added, ", +"))
		}
	} else if !interactive && env.HasRepoOrGitignore && len(env.MissingGitignore) > 0 {
		fmt.Fprintf(errOut, "cmk: hint: pass --gitignore to add %s to .gitignore\n", strings.Join(env.MissingGitignore, ", "))
	}
	return nil
}

func normalizeChoice(value string) string {
	trimmed := strings.TrimSpace(value)
	switch strings.ToLower(trimmed) {
	case "", "none", "system":
		return ""
	default:
		return trimmed
	}
}

func detectInitEnv(root string) initEnv {
	var env initEnv
	if installed, err := clangupHostToolchains(hostTarget()); err == nil && len(installed) > 0 {
		env.Toolchains = installed
		env.DefaultToolchain = installed[0].PreferredSelector()
	}
	for _, candidate := range []string{"ccache", "sccache"} {
		if _, err := initLookPath(candidate); err == nil {
			env.Launcher = candidate
			break
		}
	}
	for _, dir := range []string{"third_party", "vendor", "external"} {
		if st, err := os.Stat(filepath.Join(root, dir)); err == nil && st.IsDir() {
			env.IgnoreGlobs = append(env.IgnoreGlobs, dir+"/**")
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".gitignore")); err == nil {
		env.HasRepoOrGitignore = true
	} else if _, err := os.Stat(filepath.Join(root, ".git")); err == nil {
		env.HasRepoOrGitignore = true
	}
	env.MissingGitignore = missingGitignoreEntries(root, cmkGitignoreEntries)
	return env
}

type toolchainOption struct {
	selector string
	label    string
}

func buildToolchainMenu(installed []clangup.InstalledToolchain) []toolchainOption {
	var menu []toolchainOption
	seen := map[string]bool{}
	add := func(selector, label string) {
		if seen[selector] {
			return
		}
		seen[selector] = true
		menu = append(menu, toolchainOption{selector: selector, label: label})
	}
	for i, tc := range installed {
		preferred := tc.PreferredSelector()
		tag := "installed " + tc.Version + "-" + strconv.Itoa(tc.Release)
		if tc.IsDefault {
			tag += ", default"
		}
		add(preferred, fmt.Sprintf("%s (%s)", preferred, tag))
		if i == 0 && tc.InIndex {
			exact := tc.ExactSelector()
			add(exact, fmt.Sprintf("%s (pinned release)", exact))
		}
	}
	add("", "system (CC/CXX or PATH)")
	return menu
}

func readPromptLine(scanner *bufio.Scanner) (string, bool) {
	if !scanner.Scan() {
		return "", false
	}
	return strings.TrimSpace(scanner.Text()), true
}

func promptInitPlan(scanner *bufio.Scanner, out io.Writer, env initEnv, options initOptions, plan *initPlan) error {
	if !options.ToolchainSet {
		if len(env.Toolchains) > 0 {
			menu := buildToolchainMenu(env.Toolchains)
			fmt.Fprintln(out, "Detected clangup toolchains:")
			for i, item := range menu {
				fmt.Fprintf(out, "  %d) %s\n", i+1, item.label)
			}
			fmt.Fprint(out, "Toolchain [1, or selector]: ")
			if line, ok := readPromptLine(scanner); ok && line != "" {
				if idx, err := strconv.Atoi(line); err == nil && idx >= 1 && idx <= len(menu) {
					plan.Toolchain = menu[idx-1].selector
				} else {
					plan.Toolchain = normalizeChoice(line)
				}
			}
		} else {
			fmt.Fprint(out, "Toolchain selector (e.g. libcxx, default, or Enter for system) [system]: ")
			if line, ok := readPromptLine(scanner); ok && line != "" {
				plan.Toolchain = normalizeChoice(line)
			}
		}
	}

	if !options.LauncherSet && env.Launcher != "" {
		fmt.Fprintf(out, "Enable compiler launcher (%s)? [Y/n]: ", env.Launcher)
		if line, ok := readPromptLine(scanner); ok && line != "" {
			switch strings.ToLower(line) {
			case "y", "yes":
				plan.Launcher = env.Launcher
			case "n", "no", "none":
				plan.Launcher = ""
			default:
				plan.Launcher = line
			}
		}
	}

	if !options.AsanSet {
		fmt.Fprint(out, "Add an Asan configuration (-fsanitize=address,undefined)? [y/N]: ")
		if line, ok := readPromptLine(scanner); ok {
			switch strings.ToLower(line) {
			case "y", "yes":
				plan.Asan = true
			default:
				plan.Asan = false
			}
		}
	}

	if !options.GitignoreSet && env.HasRepoOrGitignore && len(env.MissingGitignore) > 0 {
		fmt.Fprintf(out, "Update .gitignore (%s)? [Y/n]: ", strings.Join(env.MissingGitignore, ", "))
		plan.UpdateGitignore = true
		if line, ok := readPromptLine(scanner); ok {
			switch strings.ToLower(line) {
			case "n", "no":
				plan.UpdateGitignore = false
			}
		}
	}
	return scanner.Err()
}

func formatInitSummary(plan initPlan) string {
	var parts []string
	if plan.Toolchain != "" {
		parts = append(parts, "toolchain: "+plan.Toolchain)
	} else {
		parts = append(parts, "toolchain: system")
	}
	if plan.Launcher != "" {
		parts = append(parts, "launcher: "+plan.Launcher)
	}
	if plan.Asan {
		parts = append(parts, "configurations: Debug/Release/Asan")
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

func validateRenderedConfig(rendered, root string) error {
	cfg := &Config{
		Toolchain: ToolchainCfg{},
		Deps:      map[string]*DepCfg{},
		Env:       map[string]string{},
		TargetEnv: map[string]map[string]string{},
	}
	dec := yaml.NewDecoder(bytes.NewReader([]byte(rendered)))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil {
		return err
	}
	if cfg.Version != 1 {
		return fmt.Errorf("version must be 1")
	}
	if err := normalizeConfig(cfg); err != nil {
		return err
	}
	return validateConfig(cfg, root)
}

func yamlInlineString(s string) string {
	out, err := yaml.Marshal(s)
	if err != nil {
		return strconv.Quote(s)
	}
	return strings.TrimSpace(string(out))
}

func renderCmkYAML(plan initPlan) string {
	var b strings.Builder
	b.WriteString(schemaModeline)
	b.WriteString("\nversion: 1\n\n")

	b.WriteString("# Toolchain selectors are resolved in exact-platform, OS, default order.\n")
	if plan.Toolchain != "" {
		channel, _, _ := strings.Cut(plan.Toolchain, "@")
		b.WriteString("toolchain:\n")
		if channel == "libcxx" {
			b.WriteString("  linux: " + yamlInlineString(plan.Toolchain) + "\n")
			b.WriteString("  # macos: default\n")
		} else {
			b.WriteString("  default: " + yamlInlineString(plan.Toolchain) + "\n")
		}
	} else {
		b.WriteString("# toolchain:\n")
		b.WriteString("#   default: default\n")
		b.WriteString("#   linux: libcxx\n")
		b.WriteString("#   macos: default\n")
	}

	b.WriteString("\n# Defaults when omitted: Ninja Multi-Config in ./build with Debug and Release.\n")
	b.WriteString("cmake:\n")
	b.WriteString("  compile-commands: default\n")
	if plan.Launcher != "" {
		b.WriteString("  launcher: " + yamlInlineString(plan.Launcher) + "\n")
	} else {
		b.WriteString("  # launcher: ccache\n")
	}
	b.WriteString("  # user-presets: false\n\n")

	b.WriteString("  # A preset is a separate configure/build tree. A configuration is selected\n")
	b.WriteString("  # inside every multi-config tree with cmk build -p <preset> -c <config>.\n")
	b.WriteString("  # presets:\n")
	b.WriteString("  #   default:\n")
	b.WriteString("  #     build-dir: build/default\n")
	b.WriteString("  #   minimal:\n")
	b.WriteString("  #     inherits: default\n")
	b.WriteString("  #     build-dir: build/minimal\n")
	b.WriteString("  #     variables:\n")
	b.WriteString("  #       ENABLE_OPTIONAL_FEATURES: false\n")
	b.WriteString("  #   release:\n")
	b.WriteString("  #     build-dir: build/release\n")
	b.WriteString("  #     generator: Ninja\n")
	b.WriteString("  #     build-type: Release\n\n")

	b.WriteString("  # A configuration's compile/link flags become CMAKE_<LANG>_FLAGS_<CONFIG>\n")
	b.WriteString("  # cache variables. A custom configuration lists its full flag set.\n")
	if plan.Asan {
		b.WriteString("  configurations:\n")
		b.WriteString("    - name: Debug\n")
		b.WriteString("    - name: Release\n")
		b.WriteString("    - name: Asan\n")
		b.WriteString("      compile: [\"-g\", \"-O1\", \"-fsanitize=address,undefined\", \"-fno-omit-frame-pointer\"]\n")
		b.WriteString("      link: [\"-fsanitize=address,undefined\"]\n")
	} else {
		b.WriteString("  # configurations:\n")
		b.WriteString("  #   - name: Debug\n")
		b.WriteString("  #   - name: Release\n")
		b.WriteString("  #   - name: Asan\n")
		b.WriteString("  #     compile: [\"-g\", \"-O1\", \"-fsanitize=address,undefined\", \"-fno-omit-frame-pointer\"]\n")
		b.WriteString("  #     link: [\"-fsanitize=address,undefined\"]\n")
	}

	b.WriteString("\n# External dependencies use immutable sources and project-owned recipes.\n")
	b.WriteString("# Archive sha256 digests and Git commits are pinned in cmk.lock.\n")
	b.WriteString("# dependencies:\n")
	b.WriteString("#   zlib:\n")
	b.WriteString("#     script: cmk/deps/zlib.sh\n")
	b.WriteString("#     cmake-name: ZLIB\n")
	b.WriteString("#     source:\n")
	b.WriteString("#       url: https://github.com/madler/zlib/releases/download/v1.3.1/zlib-1.3.1.tar.gz\n\n")

	b.WriteString("# env:\n")
	b.WriteString("#   MY_VAR: ${PROJECT_ROOT}/config\n\n")
	b.WriteString("# target-env:\n")
	b.WriteString("#   my_server:\n")
	b.WriteString("#     ASAN_OPTIONS: detect_leaks=1\n\n")

	if len(plan.IgnoreGlobs) > 0 {
		quoted := make([]string, len(plan.IgnoreGlobs))
		for i, g := range plan.IgnoreGlobs {
			quoted[i] = yamlInlineString(g)
		}
		list := "[" + strings.Join(quoted, ", ") + "]"
		b.WriteString("format:\n")
		b.WriteString("  ignore: " + list + "\n\n")
		b.WriteString("lint:\n")
		b.WriteString("  ignore: " + list + "\n")
	} else {
		b.WriteString("# format:\n")
		b.WriteString("#   command: tools/clang-format\n")
		b.WriteString("#   ignore: [third_party/**]\n\n")
		b.WriteString("# lint:\n")
		b.WriteString("#   command: tools/clang-tidy\n")
		b.WriteString("#   ignore: [third_party/**]\n")
		b.WriteString("#   header-filter: ^(src|include)/\n")
	}
	return b.String()
}

func normalizeGitignoreRule(line string) string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "/")
	line = strings.TrimSuffix(line, "/**")
	line = strings.TrimSuffix(line, "/*")
	line = strings.TrimSuffix(line, "/")
	return line
}

func missingGitignoreEntries(root string, desired []string) []string {
	data, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	covered := map[string]bool{}
	if err == nil {
		for line := range strings.Lines(string(data)) {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "!") {
				continue
			}
			covered[normalizeGitignoreRule(trimmed)] = true
		}
	}
	var missing []string
	for _, entry := range desired {
		if !covered[normalizeGitignoreRule(entry)] {
			missing = append(missing, entry)
		}
	}
	return missing
}

func ensureGitignoreEntries(root string, desired []string) ([]string, error) {
	missing := missingGitignoreEntries(root, desired)
	if len(missing) == 0 {
		return nil, nil
	}
	path := filepath.Join(root, ".gitignore")
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	var b strings.Builder
	b.Write(existing)
	if len(existing) > 0 && existing[len(existing)-1] != '\n' {
		b.WriteByte('\n')
	}
	for _, entry := range missing {
		b.WriteString(entry)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return nil, err
	}
	return missing, nil
}

func cmdNew(name string) error {
	if _, err := os.Stat(name); err == nil {
		return fmt.Errorf("%s already exists", name)
	}

	files := map[string]string{
		"CMakeLists.txt": strings.ReplaceAll(cmakeListsTemplate, "{name}", name),
		"cmk.yaml":       renderCmkYAML(initPlan{}),
		"src/main.cc":    mainCcTemplate,
		".gitignore":     gitignoreTemplate,
		".clang-format":  clangFormatTemplate,
		".clang-tidy":    clangTidyTemplate,
	}
	for rel, content := range files {
		path := filepath.Join(name, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return err
		}
	}
	if err := exec.Command("git", "init", "-q", name).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "cmk: warning: git init failed:", err)
	}
	fmt.Fprintf(os.Stderr, "cmk: created %s\n  cd %s && cmk config && cmk run\n", name, name)
	return nil
}

const cmakeListsTemplate = `cmake_minimum_required(VERSION 3.24)
project(
  {name}
  VERSION 0.1.0
  LANGUAGES CXX C
)

set(CMAKE_CXX_STANDARD 23)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)   # -std=c++23, not -std=gnu++23

add_executable({name} src/main.cc)

# Scope warnings and sanitizers to our own target. A global
# add_compile_options() would also hit FetchContent/CPM dependencies.
target_compile_options({name} PRIVATE -Wall -Wextra)
target_compile_options({name} PRIVATE $<$<CONFIG:Debug>:-fsanitize=address,undefined>)
target_link_options({name} PRIVATE $<$<CONFIG:Debug>:-fsanitize=address,undefined>)

# Install rules: cmk install (cmake --install)
include(GNUInstallDirs)
install(TARGETS {name} RUNTIME DESTINATION ${CMAKE_INSTALL_BINDIR})

# Tests: cmk test (ctest)
enable_testing()
add_test(NAME {name} COMMAND {name})
`

const mainCcTemplate = `#include <print>

int main() {
  std::println("Hello, world!");
  return 0;
}
`

const gitignoreTemplate = `build/
.cache/
compile_commands.json
CMakeUserPresets.json
cmk.dev.yaml
`

const clangFormatTemplate = `---
Language:        Cpp
BasedOnStyle:  Google
AccessModifierOffset: -2
IncludeBlocks: Preserve
IndentCaseLabels: false
PointerAlignment: Right
...
`

const clangTidyTemplate = `---
Checks: '
        bugprone-*,
        clang-analyzer-*,
        cppcoreguidelines-*,
        modernize-*,
        performance-*,
        portability-*,
        readability-*,
        -bugprone-easily-swappable-parameters,
        -cppcoreguidelines-avoid-magic-numbers,
        -cppcoreguidelines-non-private-member-variables-in-classes,
        -cppcoreguidelines-pro-type-vararg,
        -modernize-use-nodiscard,
        -modernize-use-ranges,
        -modernize-use-trailing-return-type,
        -readability-identifier-length,
        -readability-function-cognitive-complexity,
        -readability-magic-numbers,
        -readability-math-missing-parentheses,
        -readability-qualified-auto,
        -readability-static-accessed-through-instance
        '

CheckOptions:
  - key: cppcoreguidelines-special-member-functions.AllowImplicitlyDeletedCopyOrMove
    value: true
`
