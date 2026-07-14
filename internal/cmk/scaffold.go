package cmk

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func cmdInit(force bool) error {
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
	if err := os.WriteFile(path, []byte(cmkYAMLTemplate), 0o644); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "cmk: wrote", path)
	return nil
}

func cmdNew(name string) error {
	if _, err := os.Stat(name); err == nil {
		return fmt.Errorf("%s already exists", name)
	}

	files := map[string]string{
		"CMakeLists.txt": strings.ReplaceAll(cmakeListsTemplate, "{name}", name),
		"cmk.yaml":       cmkYAMLTemplate,
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

const cmkYAMLTemplate = `version: 1

# Toolchain selectors are resolved in exact-platform, OS, default order.
# toolchain:
#   default: default@22.1.8-1
#   linux: libcxx@22.1.8-1
#   linux-aarch64: libcxx@22.1.8-1

cmake:
  generator: Ninja Multi-Config
  default-preset: default
  default-configuration: Debug
  compile-commands: default
  # launcher: ccache

  presets:
    default:
      build-dir: build

  configurations:
    - name: Debug
    - name: Release

  # A preset is a separate configure/build tree. A configuration is selected
  # inside every multi-config tree with cmk build -p <preset> -c <config>.
  # presets:
  #   default:
  #     build-dir: build/default
  #   minimal:
  #     inherits: default
  #     build-dir: build/minimal
  #     variables:
  #       ENABLE_OPTIONAL_FEATURES: false
  #   release:
  #     build-dir: build/release
  #     generator: Ninja
  #     build-type: Release

  # A configuration's compile/link flags become CMAKE_<LANG>_FLAGS_<CONFIG>
  # cache variables. A custom configuration lists its full flag set.
  # configurations:
  #   - name: Debug
  #   - name: Release
  #   - name: Asan
  #     compile: [-g, -O1, -fsanitize=address, -fno-omit-frame-pointer]
  #     link: [-fsanitize=address]

# External dependencies use immutable sources and project-owned recipes.
# dependencies:
#   zlib:
#     script: cmk/deps/zlib.sh
#     cmake-name: ZLIB
#     source:
#       url: https://github.com/madler/zlib/releases/download/v1.3.1/zlib-1.3.1.tar.gz
#       sha256: 9a93b2b7dfdac77ceba5a558a580e74667dd6fede4585b91eefb60f03b72df23

# env:
#   MY_VAR: ${PROJECT_ROOT}/config

# target-env:
#   my_server:
#     ASAN_OPTIONS: detect_leaks=1

# format:
#   ignore: [third_party/**]

# lint:
#   ignore: [third_party/**]
#   header-filter: ^(src|include)/
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
