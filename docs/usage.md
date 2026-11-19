# Usage Guide

This guide walks through day-to-day workflows with `clangup` and `cmk`, from installing a Clang toolchain to building C++23 modules, running sanitizers, and managing external dependencies. For complete schema definitions, CLI flags, and environment variables, see the [Reference Manual](reference.md).

---

## 1. Installing and managing toolchains (`clangup`)

### Choosing a channel

`clangup` distributes prebuilt LLVM/Clang toolchains organized by **channel**. A selector can name a floating channel (such as `libcxx-pgo`) or an exact release (`libcxx-pgo@23.1.5-1`).

| Channel | Linux (`x86_64` / `aarch64`) | macOS (`x86_64` / `aarch64`) | Notes |
| --- | --- | --- | --- |
| `default` | Host libstdc++, system linker, libgcc | Apple SDK libc++, system `ld` / `ar` | Drop-in ABI compatibility with system libraries |
| `libcxx` | Bundled `libc++`, `lld`, `compiler-rt`, `libgcc_s` | *(Linux only)* | Self-contained modern C++ standard library & fast LLD linking |
| `libcxx-pgo` | Same driver contract as `libcxx`, built with PGO + ThinLTO (+ BOLT on `x86_64`) | System libc++/linker, built with PGO | Fastest compile times; Linux `x86_64` requires an `x86-64-v3` CPU |

### Everyday toolchain commands

```sh
# Fetch the latest channel index and inspect available channels/releases
clangup update
clangup list --remote
clangup list --remote libcxx-pgo
clangup list --remote --all

# Install a channel (or pin an exact release) and mark it as the host default
clangup install libcxx-pgo
clangup install libcxx@23.1.0-1
clangup default libcxx-pgo

# Expose the default toolchain's bin/ directory in the current shell
eval "$(clangup env)"

# Inspect installed toolchains, check host health, or clean up
clangup list
clangup doctor
clangup uninstall libcxx@23.1.0-1
clangup gc
```

> **Note:** `cmk` resolves toolchains directly from `cmk.yaml` and `cmk.lock` without requiring `eval "$(clangup env)"` in your shell. If a project declares a `clangup` selector that is not yet installed locally, `cmk config` / `cmk build` installs the pinned release automatically.

### Direct archive or URL imports

To install an artifact offline or from a custom HTTPS mirror without consulting the channel index, keep its `.manifest.json` beside the `.tar.zst` archive:

```sh
clangup install --file ./clang-libcxx-23.1.5-1.tar.zst
clangup install --url https://mirror.example.com/clang-libcxx-23.1.5-1.tar.zst
```

---

## 2. Creating or adopting a project

### Scaffolding a new C++23 project

`cmk new` creates a ready-to-run C++23 CMake project with `CMakeLists.txt`, `cmk.yaml`, `src/main.cc`, `.clang-format`, `.clang-tidy`, `.gitignore`, and a Git repository:

```sh
cmk new myapp
cd myapp
cmk run
cmk test
```

### Initializing `cmk.yaml` in an existing CMake repository

Run `cmk init` from the root of an existing project:

```sh
cmk init
```

`cmk init` inspects the host and repository automatically:
- **Toolchains** — Detects locally installed `clangup` toolchains (preferring the active default).
- **Compiler launcher** — Detects `ccache` or `sccache` on `PATH`.
- **Vendor directories** — Detects `third_party/`, `vendor/`, and `external/` and excludes them from `cmk fmt` and `cmk lint`.
- **`.gitignore`** — Checks whether `build/`, `compile_commands.json`, `CMakeUserPresets.json`, and `cmk.dev.yaml` are ignored.

On an interactive terminal, `cmk init` asks a short sequence of confirmation prompts (press `Enter` to accept each detected default). For scripts or one-liners, pass `-y` and explicit flags:

```sh
cmk init -y
cmk init --toolchain libcxx-pgo --launcher ccache --asan --gitignore
cmk init --toolchain system --force
```

### Editor autocompletion for `cmk.yaml`

Generated `cmk.yaml` files start with a `# yaml-language-server: $schema=...` header, giving automatic field completion, hover documentation, and validation in VS Code (`redhat.vscode-yaml`), Neovim/Helix (`yaml-language-server`), Zed, and JetBrains CLion.

For offline environments, export the embedded JSON Schema directly from the `cmk` binary:

```sh
cmk schema -o .cache/cmk.schema.json
```

### Working with unmanaged (foreign) CMake projects

`cmk.yaml` is optional for day-to-day build, run, test, TU, format, and lint commands. In any CMake project that already has a configured build directory (for example `cmake -B build -G Ninja`), `cmk` operates that tree without altering its generator, compiler, or cache:

```sh
cmake -B build -G "Ninja Multi-Config"
cmk build
cmk build -c Release app -- -v
cmk run app -- --help
cmk test
cmk tu src/foo.cpp
```

---

## 3. Configuring, building, running, testing, and installing

### Presets vs. configurations

`cmk` separates **presets** (`-p/--preset`) from **configurations** (`-c/--config`):
- A **preset** owns a build directory (`build/` by default) and configure-time CMake cache variables/generator settings.
- A **configuration** selects a build mode (`Debug`, `Release`, `Asan`, …) inside a multi-config build directory without reconfiguring CMake.

When `cmake.generator` is omitted, `cmk` defaults to **`Ninja Multi-Config`** with `Debug` (default) and `Release` configurations. If a preset sets `build-type` (for example `build-type: Release`), that preset defaults to single-config **`Ninja`** instead.

### Configure and automatic staleness checks

```sh
cmk config                          # configure the default preset
cmk config -p minimal               # configure a named preset
cmk config --fresh                  # wipe CMakeCache.txt and reconfigure from scratch
cmk config --if-stale               # configure only when missing or inputs changed
cmk config -- -DENABLE_BENCH=ON     # pass ad-hoc cache defines (preserved across auto-reconfigures)
```

You rarely need to run `cmk config` manually after edits: `cmk build`, `cmk run`, `cmk test`, `cmk install`, `cmk tu`, and `cmk lint` inspect the CMake File API reply (`cmakeFiles-v1` + `CONFIGURE_DEPENDS` globs), `CMakeCache.txt` (including `CMAKE_COMMAND`/`CMAKE_MAKE_PROGRAM` when `cmake.version` or `cmake.ninja` is pinned), and `.cmk/injection.json` before running, reconfiguring automatically (with `--fresh` when external `cmake` edits or injection/tool changes are detected) whenever `CMakeLists.txt`, `cmk.yaml`, `cmk.lock`, or dependency prefixes change.

- Pass **`--locked`** in CI to fail instead of writing `cmk.lock`, reconfiguring a stale tree, or accepting local `cmk.dev.yaml` overlays.
- Pass **`--no-config`** when iterating on source files and you want to skip staleness checks altogether.

### Building targets and single translation units

```sh
cmk build                           # build default target(s) in the default configuration
cmk build -c Release app core -j8   # build specific targets in Release
cmk build -i                        # pick a target interactively with fzf
cmk build --clean-first app
cmk build -- -d explain             # forward arguments after -- to ninja/build tool
```

To compile individual source files or object targets without linking libraries or executables, use `cmk build-tu` (alias `cmk tu`) on any Ninja build tree:

```sh
cmk tu src/engine.cc src/net.cc
cmk tu -c Release src/engine.cc
cmk tu                              # interactively pick an object target via fzf
```

### Running executables and using sanitizers

```sh
cmk run                             # runs the sole executable (or prompts via fzf)
cmk run server -- --config dev.toml # pass program arguments after --
cmk run -c Asan server              # build and run under a custom Asan configuration
cmk run --no-build server           # run existing binary without building
```

Custom multi-config sanitizer configurations can be added directly in `cmk.yaml` (or via `cmk init --asan`):

```yaml
cmake:
  configurations:
    - name: Debug
    - name: Release
    - name: Asan
      compile: ["-g", "-O1", "-fsanitize=address,undefined", "-fno-omit-frame-pointer"]
      link: ["-fsanitize=address,undefined"]
```

When using a `clangup` toolchain, `cmk` automatically sets **`LLVM_SYMBOLIZER_PATH`** to that toolchain's bundled `llvm-symbolizer` (unless already set in your environment, `env`, or `target-env`), so ASan/UBSan/TSan stack traces print exact source files and line numbers out of the box.

### Testing and installing

```sh
cmk test                            # build tests and run ctest --output-on-failure
cmk test parser lexer               # run tests matching regex (parser)|(lexer)
cmk test -L unit -c Release         # filter by CTest label in Release
cmk test -t unit_tests -- -VV       # build specific target(s) and pass flags to ctest

cmk install --prefix ./dist
cmk install -c Release --component runtime --strip
```

> **CMake 4.5+ fast prep targets:** On Ninja trees generated by CMake 4.5 or newer, `cmk test` automatically builds `test_prep/all` (when `-t` is omitted) and `cmk install` automatically builds `install_prep/<component>` or `install_prep/all` instead of `all`, avoiding unnecessary builds of unrelated binaries.

### Cross-worktree `ccache` reuse

Setting `cmake.launcher: ccache` in `cmk.yaml` injects `CMAKE_<LANG>_COMPILER_LAUNCHER` when `ccache` is present on `PATH` (and degrades to a warning rather than failing if a machine lacks `ccache`). `cmk` also sets `CCACHE_BASEDIR=${PROJECT_ROOT}` and `CCACHE_NOHASHDIR=true` automatically so builds across separate Git worktrees share cache hits.

---

## 4. C++20 and C++23 modules (`import std;`)

When a project selects a `clangup` toolchain (`libcxx` or `libcxx-pgo`), `cmk` automatically passes the toolchain's `clang-scan-deps` and `libc++.modules.json` paths (`CMAKE_CXX_COMPILER_CLANG_SCAN_DEPS` and `CMAKE_CXX_STDLIB_MODULES_JSON`) to CMake.

With CMake 4.0+ (including CMake 4.5's native `CMAKE_CXX_MODULE_STD` support), `import std;` and named C++20 modules work without manual toolchain plumbing:

```cmake
cmake_minimum_required(VERSION 4.0)
project(modular_app LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 23)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)
set(CMAKE_CXX_MODULE_STD ON)

add_executable(app)
target_sources(app
  PRIVATE src/main.cc
  PRIVATE FILE_SET CXX_MODULES FILES src/math.cppm
)
```

`cmk` also handles two sharp edges of C++ modules in tooling:
1. **`clangd` compatibility** — When mirroring `compile_commands.json` to the project root, `cmk` strips CMake's synthetic `__cmake_cxx23` / `__cmake_cxx26` stdlib BMI compilation entries so `clangd` does not misindex standard library sources.
2. **`cmk lint` BMI pre-warming** — Before running `clang-tidy` on files in a modular build tree, `cmk lint` automatically builds any Ninja `.pcm` / `.bmi` targets so `clang-tidy` can resolve imported modules on a clean tree.

---

## 5. External dependencies, patches, and local overlays

### When to use `FetchContent` vs. `cmk` dependencies

- **Use CMake `FetchContent` / `CPM`** for lightweight, CMake-native libraries. Because `cmk` injects `CMAKE_TOOLCHAIN_FILE` (and `CC`/`CXX`) at configure time, `FetchContent` subbuilds inherit the project's `clangup` compiler and `libc++` ABI automatically.
- **Use `cmk.yaml` `dependencies:`** for dependencies that use Autotools/Make/Meson/custom build scripts, require source patches, or are expensive to compile and benefit from being built once into `~/.local/share/cmk/store` and reused across every build directory and Git worktree.

### Adding and syncing a dependency

`cmk add` appends the dependency entry to `cmk.yaml` and creates an executable Bash recipe stub at `cmk/deps/<name>.sh` (forwarding `$CMK_TOOLCHAIN_FILE` automatically):

```sh
# Archive source (sha256 is optional in cmk.yaml and pinned automatically in cmk.lock)
cmk add zlib \
  --url https://github.com/madler/zlib/releases/download/v1.3.1/zlib-1.3.1.tar.gz \
  --cmake-name ZLIB

# Git source (ref is resolved and pinned to an exact commit in cmk.lock)
cmk add fmt \
  --git https://github.com/fmtlib/fmt.git \
  --ref 11.0.2 \
  --cmake-name fmt

# Declare dependencies between recipes with --needs
cmk add spdlog \
  --git https://github.com/gabime/spdlog.git \
  --ref v1.15.0 \
  --needs fmt \
  --cmake-name spdlog
```

Edit `cmk/deps/<name>.sh` if the library needs custom configure flags, then build into the shared store and configure your project:

```sh
cmk sync
cmk config
```

In `CMakeLists.txt`, consume the package with standard `find_package(ZLIB REQUIRED)`—`cmk` passes `-DZLIB_ROOT=<store-prefix>` automatically. Commit `cmk.yaml`, `cmk.lock`, and `cmk/deps/*.sh`.

### Updating pinned toolchains and dependencies

```sh
cmk update                  # re-resolve floating toolchains, managed cmake/ninja, and Git refs in cmk.lock
cmk update toolchain        # update only the host platform's toolchain pin
cmk update cmake ninja      # refresh managed cmake and ninja pins in cmk.lock
cmk update fmt              # update only fmt's Git commit pin
cmk sync                    # rebuild any dependencies whose identity changed
```

### Applying source patches

Place unified diff patches in the repository and list them under `patches`:

```yaml
dependencies:
  zlib:
    script: cmk/deps/zlib.sh
    cmake-name: ZLIB
    source:
      url: https://github.com/madler/zlib/releases/download/v1.3.1/zlib-1.3.1.tar.gz
    patches:
      - cmk/patches/zlib-clang.patch
```

Patch file contents are hashed into the dependency's store identity, so editing a patch and running `cmk build` or `cmk sync` automatically rebuilds the dependency.

### Developing against a local checkout (`cmk dev`)

When co-developing a dependency in a local directory, register a machine-local overlay (recorded in gitignored `cmk.dev.yaml` without touching `cmk.yaml` or `cmk.lock`):

```sh
cmk dev fmt ../fmt          # point fmt at ../fmt
cmk build                   # builds ../fmt incrementally and relinks dependents if output changed

cmk dev                     # list active local overlays
cmk dev --drop fmt          # remove overlay and switch back to the pinned store entry
```

### Inspecting and cleaning the dependency store

```sh
cmk clean                   # list store entries (* = referenced by this project's cmk.lock)
cmk clean --prune           # delete entries not referenced by this project's cmk.lock
cmk clean --all             # remove the entire store and archive download cache
```

---

## 6. Formatting, linting, and developer environment

### `cmk fmt` and `cmk lint`

Without file or scope flags, both `cmk fmt` and `cmk lint` target C/C++ files (`.c`, `.cc`, `.cpp`, `.cxx`, `.h`, `.hpp`, `.cppm`, `.ixx`, …) modified relative to `HEAD`:

```sh
# Formatting (clang-format)
cmk fmt                     # format files changed since HEAD
cmk fmt --staged            # format staged files (ideal for pre-commit hooks)
cmk fmt --all --dry-run     # check all tracked files in CI (exits non-zero if unformatted)
cmk fmt src/main.cc         # format explicit files

# Linting (clang-tidy)
cmk lint                    # lint files changed since HEAD
cmk lint --staged           # lint staged files
cmk lint --commit HEAD~1    # lint files touched by a specific commit
cmk lint --branch           # lint files changed since merge-base with origin/main (or main)
cmk lint --branch release   # lint files changed since merge-base with 'release'
cmk lint -i                 # interactively select a file from compile_commands.json via fzf
cmk lint src/main.cc --fix  # apply clang-tidy fixes serially
cmk lint --all -W           # lint all tracked files and treat warnings as errors
```

When the project selects a `clangup` toolchain, `cmk fmt` and `cmk lint` automatically use that toolchain's `clang-format` and `clang-tidy` (unless overridden via `format.command` / `lint.command` in `cmk.yaml`).

### Entering the project environment (`cmk env` & `cmk shell`)

To run ad-hoc tools (`ninja`, `ctest`, `pkg-config`, custom scripts) with the project's `clangup` toolchain, `CMAKE_PREFIX_PATH`, `PKG_CONFIG_PATH`, `LLVM_SYMBOLIZER_PATH`, `ccache` variables, and `cmk.yaml` `env` variables preconfigured:

```sh
# Spawn an interactive subshell (or run one command after --)
cmk shell
cmk shell -- pkg-config --libs fmt

# Or export into your current shell / direnv (.envrc: eval "$(cmk env)")
eval "$(cmk env)"
```

### IDE setup, shell completion, and diagnostics

- **`clangd` (VS Code, Neovim, Helix, Zed)** — With `cmake.compile-commands: default` (set by `cmk init`), `cmk` keeps `./compile_commands.json` in the project root pointed at the active configuration. Switch it to another configuration (e.g. `Asan`) via `cmake.compile-commands: Asan` or disable it with `none`.
- **CMake Presets IDEs** — `cmk config` writes `CMakeUserPresets.json` (gitignored) with full compiler, managed `cmakeExecutable`/`CMAKE_MAKE_PROGRAM` (when `cmake.version`/`cmake.ninja` are set), cache variable, dependency prefix, and environment definitions for every preset (`cmk-<preset>`). Set `cmake.user-presets: false` to disable.
- **CLion** — Install the [CLion `cmk` plugin](../integrations/clion-cmk/README.md) so CLion delegates `cmk-*` preset configuration to `cmk config --if-stale` while keeping native debugging, test runners, and code navigation.
- **Shell completion** — Enable dynamic tab-completion for presets, multi-config configurations, build directories, CMake targets, translation units, and dependency names:
  ```sh
  # Zsh (~/.zshrc)
  source <(clangup completion zsh)
  source <(cmk completion zsh)

  # Bash (~/.bashrc)
  source <(clangup completion bash)
  source <(cmk completion bash)

  # Fish (~/.config/fish/config.fish)
  clangup completion fish | source
  cmk completion fish | source
  ```
- **Diagnostics** — Run `cmk doctor` at any time to inspect the resolved toolchain, C++ modules readiness, multi-config configurations, `ccache` status, dependency store entries, and build tree staleness without modifying disk state.
