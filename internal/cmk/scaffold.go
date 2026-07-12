package cmk

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func cmdInit(args []string) error {
	var force bool
	a := newArgSpec()
	a.boolFlag(&force, "-f", "--force")
	if err := a.parse(args); err != nil {
		return err
	}

	root, err := findProjectRoot()
	if err != nil {
		// not in a project yet: scaffold in the PWD
		root, err = os.Getwd()
		if err != nil {
			return err
		}
	}
	path := filepath.Join(root, configFileName)
	if _, err := os.Stat(path); err == nil && !force {
		return fmt.Errorf("%s already exists (use --force to overwrite)", path)
	}
	if err := os.WriteFile(path, []byte(cmkTomlTemplate), 0o644); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "cmk: wrote", path)
	return nil
}

func cmdNew(args []string) error {
	a := newArgSpec()
	if err := a.parse(args); err != nil {
		return err
	}
	if len(a.Pos) != 1 {
		return fmt.Errorf("usage: cmk new <name>")
	}
	name := a.Pos[0]
	if _, err := os.Stat(name); err == nil {
		return fmt.Errorf("%s already exists", name)
	}

	files := map[string]string{
		"CMakeLists.txt": strings.ReplaceAll(cmakeListsTemplate, "{name}", name),
		"cmk.toml":       cmkTomlTemplate,
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

const cmkTomlTemplate = `# cmk project configuration. See https://github.com/zhscn/clangup

# Uncomment to build with a clangup channel. cmk installs it on the first
# configure and records the exact channel release in cmk.lock.
# [toolchain]
# selector = "libcxx@22.1.8-1"

# External deps built by bash recipes outside CMake. Standard CMake deps
# belong in your CMakeLists (FetchContent); this is for the awkward ones.
# The script runs in $CMK_WORK with $CMK_SRC (unpacked source),
# $CMK_PREFIX (install here), $CMK_JOBS, $CC/$CXX, and
# $CMK_DEP_<NAME>_PREFIX for each entry in needs. cmk config injects
# -D<name>_ROOT=<prefix> unless the script writes $CMK_PREFIX/.cmk-exports.
#
# [deps.zlib]
# script = "cmk/deps/zlib.sh"
# cmake_name = "ZLIB"
# source = { url = "https://github.com/madler/zlib/releases/download/v1.3.1/zlib-1.3.1.tar.gz", sha256 = "9a93b2b7dfdac77ceba5a558a580e74667dd6fede4585b91eefb60f03b72df23" }
#
# [deps.fdb]
# script = "cmk/deps/fdb.sh"
# needs = ["zlib"]
# source = { git = "https://github.com/apple/foundationdb.git", ref = "release-7.4" }
# patches = ["cmk/patches/fdb-*.patch"]   # applied to $CMK_SRC, hashed into the stamp
# env = { FDB_BUILD_TYPE = "Release" }    # recipes see a sanitized env; knobs go here

# One build dir holding every configuration (Ninja Multi-Config),
# Cargo-style. Pick one at build time: cmk build -c <config>.
[config]
generator = "Ninja Multi-Config"
configurations = ["Debug", "Release"]
default = "Debug"               # default -c for build/run/test/tu
# compile_commands = "default"   # mirror just one config's compile_commands.json
                                 # to the project root (else clangd sees every
                                 # config); "default" = the default above
# compiler_launcher = "ccache"   # wrap compiles via ccache/sccache;
                                 # for ccache, cmk also wires CCACHE_BASEDIR
                                 # so builds in different worktrees share cache

# A custom configuration is a flag bundle. Uncomment for an Asan build
# (then: cmk build -c Asan).
# [config.configuration.Asan]
# inherits = "Debug"             # seed from ${CMAKE_*_FLAGS_DEBUG}
# flags = "-fsanitize=address -fno-omit-frame-pointer"
# link_flags = "-fsanitize=address"

# Existing configurations can be tweaked without replacing CMake's defaults.
# [config.configuration.RelWithDebInfo]
# append_flags = "-fno-omit-frame-pointer"

# Alternative model — one build DIR per type, each a separate CMake cache.
# Use this *instead of* the [config] above when the variants differ at
# configure time (different -D options, deps, or toolchain file), which
# multi-config can't express. The two models don't mix. Select a preset by
# name the same way: cmk config release / cmk build -c release.
#
# [config]
# default = "debug"
#
# [config.preset.debug]
# dir = "build/debug"
# args = ["-DCMAKE_BUILD_TYPE=Debug"]
#
# [config.preset.release]
# dir = "build/release"
# args = ["-DCMAKE_BUILD_TYPE=Release"]

# [env]                   # extra environment for cmake/ninja/run
# MY_VAR = "${PROJECT_ROOT}/config"

# [target-env.my_server]  # per-target environment for cmk run
# ASAN_OPTIONS = "detect_leaks=1"

# [fmt]
# ignore = ["third_party/**"]

# [lint]
# ignore = ["third_party/**"]
# header_filter = "^(src|include)/"
`

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
