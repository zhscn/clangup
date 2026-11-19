# clangup & cmk

`clangup` installs reproducible, versioned LLVM/Clang toolchains on Linux and macOS. `cmk` pairs those toolchains with CMake to give C and C++ projects a cohesive CLI workflow for configuring, building, running, testing, formatting, linting, and managing heavy external dependencies—without hiding or replacing CMake itself.

## Highlights

- **Pinned Clang toolchains (`clangup`)** — Install prebuilt Clang + LLVM tools (`clang`, `clang++`, `lld`, `clang-format`, `clang-tidy`, `clang-scan-deps`, `llvm-symbolizer`, `llvm-ar`) from channels such as `default`, `libcxx`, and PGO/BOLT-optimized `libcxx-pgo`, pinned per platform in `cmk.lock`.
- **Zero-boilerplate CMake workflows (`cmk`)** — Defaults to `Ninja Multi-Config` in `./build` with `Debug` and `Release` configurations, optional pinned `cmake.version` / `cmake.ninja` downloads, automatic File API staleness checks, C++20/23 module scanner/stdlib wiring (`import std;`), cross-worktree `ccache` sharing, and automatic `LLVM_SYMBOLIZER_PATH` injection for sanitizers.
- **Native IDE & editor interop** — Mirrors sanitized `compile_commands.json` for `clangd`, generates `CMakeUserPresets.json` for IDEs, ships an embedded JSON Schema for `cmk.yaml`, and provides a native [CLion integration](integrations/clion-cmk/README.md).
- **Content-addressed dependency store** — Keep simple CMake dependencies in `FetchContent`/`CPM`, and move heavy or non-CMake third-party builds into project-owned Bash recipes cached in a shared content-addressed store with local `cmk dev` worktree overlays.
- **Works with plain CMake projects too** — Run `cmk build`, `cmk run`, `cmk test`, `cmk tu`, `cmk fmt`, and `cmk lint` against any existing CMake build directory even without `cmk.yaml`.

## Installation

Install the latest `clangup` and `cmk` binaries for Linux or macOS (`x86_64` / `aarch64`):

```sh
curl -fsSL https://dl.clangup.dev/install.sh | sh
```

By default, binaries are installed to `~/.local/bin` (override with `CLANGUP_BIN_DIR` or `--bin-dir`):

```sh
curl -fsSL https://dl.clangup.dev/install.sh | sh -s -- --bin-dir /usr/local/bin
```

Release archives are published on [GitHub Releases](https://github.com/zhscn/clangup/releases). To build from source with Go:

```sh
go install ./cmd/clangup ./cmd/cmk
```

## Quickstart

### 1. Install a Clang toolchain

```sh
clangup update
clangup install libcxx-pgo      # or: default, libcxx
clangup default libcxx-pgo
eval "$(clangup env)"           # optional: put the default toolchain on PATH
```

### 2. Create or initialize a CMake project

Create a new C++23 project from scratch:

```sh
cmk new hello
cd hello
cmk run
```

Or adopt `cmk` in an existing CMake repository (`cmk init` detects installed `clangup` toolchains, `ccache`/`sccache`, vendor directories, and `.gitignore` entries):

```sh
cmk init
cmk build                       # configures ./build automatically when missing or stale
cmk build -c Release
cmk test
```

### 3. Daily workflow at a glance

```sh
cmk build app -j8               # build specific target(s) in Debug
cmk run app -- --port 8080      # build and run an executable with arguments
cmk tu src/parser.cc            # compile a single translation unit without linking
cmk fmt --staged                # format staged C/C++ files with clang-format
cmk lint --branch               # run clang-tidy on files changed on this branch
cmk doctor                      # inspect toolchain, build trees, cache, and store
```

Enable shell tab-completion (`bash`, `zsh`, `fish`, or `powershell`):

```sh
source <(clangup completion zsh)
source <(cmk completion zsh)
```

## Command overview

### `clangup`

| Command | Description |
| --- | --- |
| `clangup update` | Refresh the cached channel index |
| `clangup list [--remote [channel] \| --remote --all]` | List installed toolchains or remote channels/releases |
| `clangup install [selector \| --file ... \| --url ...]` | Install a channel, exact release, or direct artifact |
| `clangup default <toolchain>` | Set the host default toolchain for `clangup env` |
| `clangup env` | Print `PATH` export for the default toolchain |
| `clangup uninstall <toolchain>` | Remove an installed toolchain |
| `clangup gc` | Clean up interrupted downloads and stale install records |
| `clangup doctor` | Verify host prerequisites and installed toolchains |

### `cmk`

| Command | Alias | Description |
| --- | --- | --- |
| `cmk new <name>` | `n` | Scaffold a new C++23 CMake + `cmk` project |
| `cmk init` | | Generate `cmk.yaml` with environment detection and optional prompts |
| `cmk config [-- cmake-args...]` | `c` | Configure a preset or build directory |
| `cmk build [target...] [-- tool-args...]` | `b` | Build all or selected targets (auto-reconfigures when stale) |
| `cmk run [target] [-- args...]` | `r` | Build and run an executable target |
| `cmk test [pattern...] [-- ctest-args...]` | `t` | Build tests and run `ctest` |
| `cmk install` | | Build and run `cmake --install` |
| `cmk build-tu [source-or-target...]` | `tu` | Compile individual translation units / object targets |
| `cmk fmt [file...]` | `f` | Format source files with `clang-format` |
| `cmk lint [file...]` | `l` | Lint source files with `clang-tidy` |
| `cmk add <name>` | `a` | Add an external dependency entry and recipe stub |
| `cmk sync [dep...]` | `s` | Build external dependencies into the shared store |
| `cmk update [dep\|toolchain\|cmake\|ninja...]` | | Advance floating toolchain, managed `cmake`/`ninja`, or Git dependency pins in `cmk.lock` |
| `cmk dev [dep [path] \| --drop dep]` | | Override dependencies with local working trees via `cmk.dev.yaml` |
| `cmk clean [--prune \| --all]` | | Inspect, prune, or wipe the shared dependency store |
| `cmk env` / `cmk shell [-- cmd...]` | | Export or enter the full project build environment |
| `cmk schema [-o file]` | | Print or write the embedded JSON Schema for `cmk.yaml` |
| `cmk doctor` | | Report resolved toolchain, presets, build trees, and store state |

## Documentation

- **[Usage Guide](docs/usage.md)** — End-to-end workflows: toolchain channels, `cmk init`, presets & sanitizer configurations, C++20/23 modules, external dependencies & local overlays, formatting/linting, and IDE setup.
- **[Reference Manual](docs/reference.md)** — Complete `cmk.yaml` specification, CMake injection & staleness rules, recipe environment contract, `cmk.lock` semantics, CLI flags, consumer JSON protocol, and environment variables.
- **[CLion Integration](integrations/clion-cmk/README.md)** — JetBrains CLion plugin that delegates CMake profile configuration to `cmk` while keeping CLion's native project model and debugger.
