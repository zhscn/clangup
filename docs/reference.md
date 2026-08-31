# cmk project reference

## `cmk.yaml`

`cmk.yaml` has `version: 1` and defines a managed CMake project.

| Section | Purpose |
| --- | --- |
| `toolchain` | clangup selector for each host platform |
| `cmake` | presets, configurations, cache variables, and configure arguments |
| `dependencies` | externally built packages and their recipes |
| `install` | installation prefix, component, and stripping |
| `env` | environment variables for project commands |
| `target-env` | environment variables for a specific `cmk run` target |
| `format` | clang-format command and files excluded from `cmk fmt` |
| `lint` | clang-tidy command, exclusions, and diagnostic options |

## Toolchains

`toolchain` resolves an exact `os-architecture` key first, then an OS key, then
`default`.

```yaml
toolchain:
  default: default
  linux: libcxx
  linux-aarch64: libcxx@23.1.0-1
  macos: default
```

When no selector matches, cmk uses `CC` and `CXX` or discovers a system
compiler pair. Selected clangup toolchains provide `clang-format` and
`clang-tidy`; system tools come from `PATH`.

## Format and lint tools

`format.command` and `lint.command` select project-specific executables. An
absolute path is used directly; a relative path is resolved from the project
root. When omitted, the selected clangup toolchain provides the tool, or cmk
finds it on `PATH` when the project has no toolchain selector.

```yaml
format:
  command: tools/clang-format
  ignore: [third_party/**]

lint:
  command: /opt/llvm/bin/clang-tidy
  ignore: [third_party/**]
  header-filter: ^(src|include)/
```

## CMake configuration

```yaml
cmake:
  generator: Ninja Multi-Config
  default-preset: default
  default-configuration: Debug
  compile-commands: default
  launcher: ccache
  variables:
    CMAKE_COLOR_DIAGNOSTICS: true
  args: []
  presets:
    default:
      build-dir: build/default
      variables:
        ENABLE_FEATURE: true
    minimal:
      inherits: default
      build-dir: build/minimal
      variables:
        ENABLE_FEATURE: false
    release:
      build-dir: build/release
      generator: Ninja
      build-type: Release
  configurations:
    - name: Debug
    - name: Release
    - name: Asan
      compile: [-g, -O1, -fsanitize=address, -fno-omit-frame-pointer]
      link: [-fsanitize=address]
```

Every preset owns its `build-dir`; an omitted directory is `build/<preset>`.
`inherits` merges variables and arguments from another preset. A
multi-config preset exposes every entry in `configurations`; a single-config
preset uses `build-type`.

`compile` supplies common C and C++ flags. `c`, `cxx`, and `link` supply
language- or linker-specific flags. cmk passes them through
`CMAKE_<LANG>_FLAGS_<CONFIG>` and `CMAKE_<KIND>_LINKER_FLAGS_<CONFIG>`.
For a custom configuration, provide its complete flag set. Flags on standard
configurations replace CMake's initialized defaults.

## Dependencies

```yaml
dependencies:
  zlib:
    script: cmk/deps/zlib.sh
    cmake-name: ZLIB
    needs: [openssl]
    source:
      url: https://example.com/zlib.tar.gz
      sha256: <sha256>
    env:
      BUILD_SHARED_LIBS: "OFF"
    patches: [cmk/patches/zlib-fix.patch]
    extra-inputs: [cmk/deps/zlib-options.cmake]
```

`source` accepts either `url` with `sha256`, or `git` with `ref`. `cmk.lock`
pins Git refs to commits. `patches` and `extra-inputs` are project-relative
globs. Their contents participate in the dependency identity.

Each dependency has an immutable store entry after a successful pinned build.
Its identity includes the recipe, source, selected toolchain, declared
environment, patches, extra inputs, and installed outputs of `needs`. A
dependent rebuilds only when a need's installed prefix changes.

## Recipe environment

A recipe runs as Bash in `$CMK_WORK` with a sanitized environment and the
selected compiler tools.

| Variable | Meaning |
| --- | --- |
| `CMK_SRC` | materialized archive or pinned Git checkout |
| `CMK_PREFIX` | required installation destination |
| `CMK_WORK` | recipe working directory |
| `CMK_JOBS` | parallel job count resolved by cmk |
| `CMK_PROJECT_ROOT` | project root |
| `CMK_TOOLCHAIN_FILE` | toolchain CMake file |
| `CMK_DEP_<NAME>_PREFIX` | prefix of a declared direct need |
| `CMAKE_PREFIX_PATH`, `PKG_CONFIG_PATH` | prefixes of transitive needs |

The recipe may use the standard compiler variables (`CC`, `CXX`, `AR`,
`RANLIB`, and `NM`). Dependency `env` entries are expanded, exported, and
included in the dependency identity. Shell `CFLAGS` and similar ambient build
settings are not passed to recipes.

cmk normally configures the project with `-D<cmake-name>_ROOT=<prefix>`. A
recipe can write one CMake argument per line to `$CMK_PREFIX/.cmk-exports` to
replace that default export.

## Lock and local state

Commit `cmk.lock`; it pins the resolved toolchain, Git dependency revisions,
and dependency identities for each platform. `cmk.dev.yaml` is machine-local
state for `cmk dev` overlays and must be ignored by Git. A `--locked` command
requires the committed lock and rejects local overlays.
