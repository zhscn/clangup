# Reference Manual

This document specifies `cmk.yaml`, CMake configure injection and staleness rules, the external dependency store and recipe contract, lock/overlay files, CLI flags for `cmk` and `clangup`, and environment variables.

---

## 1. `cmk.yaml` specification

`cmk.yaml` resides at the project root, declares `version: 1`, and rejects unknown fields (`KnownFields` strict decoding).

### Editor JSON Schema

`cmk init` and `cmk new` emit a `yaml-language-server` modeline on line 1:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/zhscn/clangup/main/internal/cmk/schema/cmk.schema.json
```

`cmk schema [-o <file>]` prints or writes the embedded Draft-07 JSON Schema for offline setups.

### Complete annotated example

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/zhscn/clangup/main/internal/cmk/schema/cmk.schema.json
version: 1

toolchain:
  default: default
  linux: libcxx-pgo
  linux-aarch64: libcxx
  macos: default

cmake:
  version: "4.5.0"
  ninja: "1.13.2"
  generator: Ninja Multi-Config
  default-preset: default
  default-configuration: Debug
  compile-commands: default
  user-presets: true
  launcher: ccache
  variables:
    CMAKE_COLOR_DIAGNOSTICS: true
  args:
    - -Wno-dev
  presets:
    default:
      build-dir: build/default
      variables:
        ENABLE_TESTS: true
    minimal:
      inherits: default
      build-dir: build/minimal
      variables:
        ENABLE_TESTS: false
    release-single:
      build-dir: build/release-single
      generator: Ninja
      build-type: Release
  configurations:
    - name: Debug
    - name: Release
    - name: Asan
      compile: ["-g", "-O1", "-fsanitize=address,undefined", "-fno-omit-frame-pointer"]
      c: []
      cxx: ["-fno-sanitize-recover=all"]
      link: ["-fsanitize=address,undefined"]

dependencies:
  zlib:
    script: cmk/deps/zlib.sh
    cmake-name: ZLIB
    source:
      url: https://github.com/madler/zlib/releases/download/v1.3.1/zlib-1.3.1.tar.gz
    patches:
      - cmk/patches/zlib-fix.patch
    extra-inputs:
      - cmk/deps/zlib-options.cmake
    env:
      CFLAGS: "-fPIC"
  fmt:
    script: cmk/deps/fmt.sh
    cmake-name: fmt
    needs: [zlib]
    source:
      git: https://github.com/fmtlib/fmt.git
      ref: 11.0.2

rust:
  rust_cli:
    manifest: rust_cli/Cargo.toml
    package: rust_cli
    cmake-name: rust_cli
    cxxbridge: true
    features: ["derive"]
    env:
      CARGO_NET_OFFLINE: "false"

install:
  prefix: ${PROJECT_ROOT}/dist
  component: runtime
  strip: false

env:
  APP_DATA_DIR: ${PROJECT_ROOT}/testdata

target-env:
  my_server:
    ASAN_OPTIONS: detect_leaks=1:halt_on_error=1

format:
  command: tools/clang-format
  ignore: ["third_party/**"]

lint:
  command: tools/clang-tidy
  ignore: ["third_party/**"]
  warnings-as-errors: false
  header-filter: "^(src|include)/"
  extra-args: ["-Wno-unknown-warning-option"]
```

---

### Top-level sections

#### `toolchain`

Maps host platforms to `clangup` selectors (`<channel>` or `<channel>@<version>-<release>`).

- **Allowed keys**: `linux-x86_64`, `linux-aarch64`, `macos-x86_64`, `macos-aarch64`, `linux`, `macos`, `default`.
- **Resolution order**: exact `<os>-<arch>` -> `<os>` -> `default`.
- **Fallback**: when `toolchain` is omitted or no key matches the host, `cmk` uses `CC`/`CXX` from the environment or discovers `clang`/`clang++`, `gcc`/`g++`, or `cc`/`c++` on `PATH`.

#### `cmake`

| Field | Type | Default | Description |
| --- | --- | --- | --- |
| `version` | `string` | `""` | Managed CMake version (dotted numbers, e.g. `"4.5.0"`) downloaded from upstream Kitware releases and pinned per platform in `cmk.lock`. When omitted, `cmk` uses `cmake`/`ctest` from `PATH`. |
| `ninja` | `string` | `""` | Managed Ninja version (dotted numbers, e.g. `"1.13.2"`) downloaded from `clangup` R2 tool releases (`tools/ninja/`) and pinned per platform in `cmk.lock`. Automatically injected as `CMAKE_MAKE_PROGRAM` for Ninja generators unless overridden. When omitted, `cmk` uses `ninja` from `PATH`. |
| `generator` | `string` | `"Ninja Multi-Config"` | Default CMake generator across presets (unless a preset sets `build-type` without an explicit top-level `generator`, in which case that preset defaults to `"Ninja"`). |
| `default-preset` | `string` | `"default"` (or sole preset) | Preset selected when `-p/--preset` and `-b/--build` are omitted. Required when multiple presets exist without one named `default`. |
| `default-configuration` | `string` | First entry in `configurations` (`"Debug"`) | Active configuration for multi-config presets when `-c/--config` is omitted. Disallowed when all presets are single-config. |
| `compile-commands` | `string` | `""` (`"default"` in `cmk init`) | Controls `./compile_commands.json` mirroring at the project root: `"default"` (uses `default-configuration`), `"none"`/`""` (disabled), or any configured configuration name (e.g. `"Asan"`). |
| `user-presets` | `bool` | `true` | Emit `CMakeUserPresets.json` (`cmk-<preset>`) in the project root during configure. |
| `launcher` | `string` | `""` | Compiler launcher (`ccache` or `sccache`) injected as `CMAKE_<LANG>_COMPILER_LAUNCHER` if found on `PATH`. |
| `variables` | `map<string, scalar>` | `{}` | Cache variables passed as `-D<NAME>=<VALUE>` (`bool` maps to `ON`/`OFF`). Supports `${VAR}` expansion. |
| `args` | `string[]` | `[]` | Extra `cmake` configure flags. Supports `${VAR}` expansion. |
| `presets` | `map<string, Preset>` | `{default: {build-dir: build}}` | Named configure/build trees (`^[A-Za-z][A-Za-z0-9_.-]*$`). |
| `configurations` | `Configuration[]` | `[{name: Debug}, {name: Release}]` | Configurations exposed by multi-config presets (`^[A-Za-z][A-Za-z0-9_]*$`, case-insensitively unique). |

**Reserved CMake variables & flags:** `cmk` owns configure topology and rejects the following in `variables` or `args`:
- Cache variables: `CMAKE_CONFIGURATION_TYPES`, `CMAKE_DEFAULT_BUILD_TYPE`, `CMAKE_EXPORT_COMPILE_COMMANDS`, `CMAKE_PROJECT_INCLUDE`, `CMAKE_SUPPRESS_REGENERATION`, `CMAKE_C_COMPILER_LAUNCHER`, `CMAKE_CXX_COMPILER_LAUNCHER`.
- CLI flags: `-S`, `-B`, `-G`, `--preset`, `--fresh`.

#### `cmake.presets.<name>`

| Field | Type | Default | Description |
| --- | --- | --- | --- |
| `build-dir` | `string` | `build/<name>` (`build` for implicit `default`) | Unique build directory for this preset (never inherited from a parent preset). Supports `${PROJECT_ROOT}` / `${VAR}` expansion. |
| `inherits` | `string` | `""` | Parent preset name. Inherits `generator`, `build-type`, merged `variables` (child wins), and concatenated `args` (cycles rejected). |
| `generator` | `string` | Inherited or `cmake.generator` | Generator override for this preset. |
| `build-type` | `string` | `""` | Sets `CMAKE_BUILD_TYPE` for single-config generators. Conflicts with multi-config generators and explicit `CMAKE_BUILD_TYPE` defines. |
| `variables` | `map<string, scalar>` | `{}` | Per-preset cache variable overrides. |
| `args` | `string[]` | `[]` | Per-preset configure arguments appended after `cmake.args`. |

#### `cmake.configurations[]`

| Field | Type | Description |
| --- | --- | --- |
| `name` | `string` (required) | Configuration identifier (`Debug`, `Release`, `RelWithDebInfo`, `MinSizeRel`, `Asan`, …). |
| `compile` | `string[]` | Common flags prepended to both `CMAKE_C_FLAGS_<NAME>` and `CMAKE_CXX_FLAGS_<NAME>`. |
| `c` | `string[]` | C-only flags appended to `CMAKE_C_FLAGS_<NAME>`. |
| `cxx` | `string[]` | C++-only flags appended to `CMAKE_CXX_FLAGS_<NAME>`. |
| `link` | `string[]` | Linker flags injected as `CMAKE_{EXE,SHARED,MODULE}_LINKER_FLAGS_<NAME>`. |

For custom configurations (such as `Asan`), supply the complete optimization/debug/sanitizer flag set. Setting flags on standard configurations (`Debug`, `Release`, …) replaces CMake's default flag string for that configuration.

#### `dependencies.<name>`

Dependency names match `^[A-Za-z0-9_.-]+$`.

| Field | Type | Description |
| --- | --- | --- |
| `script` | `string` (required) | Project-relative path to the Bash build recipe. |
| `cmake-name` | `string` | Package prefix name exported to CMake as `-D<cmake-name>_ROOT=<store-prefix>` (defaults to `<name>`). |
| `needs` | `string[]` | Direct `cmk` dependencies consumed by this recipe (cycles rejected; built in topological order). |
| `source.url` | `string` | Archive URL (`.tar.gz`, `.tgz`, `.tar.bz2`, `.tar.xz`, `.tar.zst`, `.zip`). Mutually exclusive with `source.git`. |
| `source.sha256` | `string` | Optional 64-hex lowercase SHA-256 digest for `source.url`. Verified on download; whether set or omitted, `cmk.lock` pins the resolved archive digest. |
| `source.git` | `string` | Git repository URL. Requires `source.ref`; mutually exclusive with `source.url`. |
| `source.ref` | `string` | Git tag, branch, or commit SHA. Resolved to an exact commit hash in `cmk.lock`. |
| `patches` | `string[]` | Project-relative globs of unified diffs applied (`git apply` / `patch -p1`) to `source` before running `script`. Requires `source`. |
| `extra-inputs` | `string[]` | Project-relative file globs hashed into the dependency's store identity. |
| `env` | `map<string, string>` | Environment variables expanded, exported to `script`, and hashed into the store identity. |

#### `rust.<name>`

In-tree Rust `staticlib` crates managed directly by `cmk`. Crate keys match `^[A-Za-z0-9_.-]+$`.

| Field | Type | Description |
| --- | --- | --- |
| `manifest` | `string` (required) | Path to `Cargo.toml` (project-relative or absolute). |
| `package` | `string` | Cargo package name passed to `--package` (defaults to `<name>`). |
| `lib-name` | `string` | Staticlib base name without `lib` prefix or `.a`/`.lib` suffix (defaults to `package` with `-` replaced by `_`). |
| `cmake-name` | `string` | CMake package and target name exported as `<cmake-name>::<cmake-name>` (defaults to `<name>`). |
| `features` | `string[]` | Cargo features passed to `--features`. |
| `no-default-features` | `bool` | Pass `--no-default-features` to `cargo build` (default `false`). |
| `target` | `string` | Explicit Rust target triple passed to `--target`. |
| `cxxbridge` | `bool` | Set `CXXBRIDGE_OUT_DIR=<build-dir>/cmk-rust/<name>/cxxbridge` when invoking Cargo and export generated headers/sources via `<cmake-name>Config.cmake` (default `false`). |
| `lto` | `string` | LTO mode for release-profile builds (`Release`, `RelWithDebInfo`, `MinSizeRel`): `off` (default), `thin`, `fat`, or `linker-plugin` (cross-language ThinLTO via `-Clinker-plugin-lto` + `CFLAGS`/`CXXFLAGS` `-flto=thin` for `lld`). Ignored in `Debug`/custom configs. |
| `env` | `map<string, string>` | Extra environment variables passed to `cargo build` and folded into the configure stamp. |

**Build & CMake export contract:**
- **Out-of-source layout**: All crates in a build tree share `<build-dir>/cmk-rust/cargo-target`. Package configs are written to `<build-dir>/cmk-rust/cmake/<cmake-name>/<cmake-name>Config.cmake` and injected as `-D<cmake-name>_DIR=...` (mirrored into `CMakeUserPresets.json`).
- **Lifecycle**: `cmk config` builds each crate once for the default configuration, probes required native system libraries via `rustc --print native-static-libs` (filtering out `-lstdc++`/`-lc++` so the CMake C++ toolchain stays authoritative), and generates `<cmake-name>Config.cmake`. Subsequent `cmk build` / `run` / `test` runs invoke `cargo build --lib` before `cmake --build`.
- **Configuration mapping**: `Release`, `RelWithDebInfo` (with `CARGO_PROFILE_RELEASE_DEBUG=true`), and `MinSizeRel` build with `--release`; `Debug` and custom configurations build with the `dev` profile.
- **Toolchain environment**: `cargo` runs inside `cmk`'s toolchain environment with `CC`, `CXX`, `AR`, `CC_<target>`, and `CXX_<target>` pointing at the resolved toolchain.
- **Exported CMake interface**: `find_package(<cmake-name> REQUIRED)` provides the `IMPORTED STATIC` target `<cmake-name>::<cmake-name>` (with per-config `IMPORTED_LOCATION_<CONFIG>` and probed `INTERFACE_LINK_LIBRARIES`). When `cxxbridge: true`, it also sets `INTERFACE_INCLUDE_DIRECTORIES`, `<cmake-name>_CXXBRIDGE_INCLUDE_DIR`, and `<cmake-name>_CXXBRIDGE_SOURCE`. Because `<cmake-name>::<cmake-name>` is a real `IMPORTED STATIC` target (not an `INTERFACE` wrapper), it works both in acyclic graphs and inside CMake 3.24+ `$<LINK_GROUP:RESCAN,...>` for bidirectional static calls between Rust and C++.

#### `install`, `env`, `target-env`, `format`, `lint`

- **`install`**: `prefix` (`string`, root-relative if not absolute), `component` (`string`), `strip` (`bool`). Overridden by CLI flags on `cmk install`.
- **`env`**: `map<string, string>` exported to `config`, `build`, `run`, `test`, `install`, `env`, and `shell`, and included in the configure injection stamp.
- **`target-env`**: `map<target, map<string, string>>` overlaid on top of `env` when running `cmk run <target>`.
- **`format`**: `command` (`string`, custom `clang-format` path), `ignore` (`string[]`, project-relative globs; patterns without `/` match basenames at any depth, `**` matches across directories).
- **`lint`**: `command` (`string`, custom `clang-tidy` path), `ignore` (`string[]`), `warnings-as-errors` (`bool`), `header-filter` (`string`), `extra-args` (`string[]`).

#### Variable expansion (`${NAME}`)

Strings in `variables`, `args`, `build-dir`, `configurations` flags, `install.prefix`, `env`, `target-env`, `dependencies.<name>.env`, and `format.command`/`lint.command` expand **`${NAME}`** up to 10 passes:
1. Built-in `${PROJECT_ROOT}` (absolute path to the project root).
2. Process environment variables (`os.LookupEnv`).
3. Unknown `${NAME}` tokens remain literal.

Bare `$NAME` and CMake generator expressions (`$<$<CONFIG:Debug>:...>`) are **never** expanded or mangled by `cmk`.

---

## 2. Toolchain injection and staleness model

### Compiler & C++ modules injection

During `cmk config`, `cmk` injects the resolved toolchain as follows:
1. **No user `CMAKE_TOOLCHAIN_FILE`** — Passes `-DCMAKE_TOOLCHAIN_FILE=<clangup-prefix>/toolchain.cmake` (or `-DCMAKE_C_COMPILER=... -DCMAKE_CXX_COMPILER=...` for system compilers).
2. **User `vcpkg` toolchain file** — Preserves the user's `-DCMAKE_TOOLCHAIN_FILE=.../vcpkg.cmake` and injects `-DVCPKG_CHAINLOAD_TOOLCHAIN_FILE=<clangup-prefix>/toolchain.cmake`.
3. **Other custom `CMAKE_TOOLCHAIN_FILE`** — Preserves the user's toolchain file and injects `-DCMAKE_C_COMPILER` and `-DCMAKE_CXX_COMPILER`.
4. **C++20/23 modules** — When present in the `clangup` toolchain (and not already defined by the user), injects `-DCMAKE_CXX_COMPILER_CLANG_SCAN_DEPS=<prefix>/bin/clang-scan-deps` and (for `libc++` toolchains) `-DCMAKE_CXX_STDLIB_MODULES_JSON=<prefix>/lib/libc++.modules.json`.

### Runtime sanitizer & `ccache` environment

- **`LLVM_SYMBOLIZER_PATH`** — Automatically set to `<clangup-prefix>/bin/llvm-symbolizer` in command environments, `cmk env`/`cmk shell`, and `CMakeUserPresets.json` when unset in the ambient environment, `env`, and `target-env`.
- **`CCACHE_BASEDIR` & `CCACHE_NOHASHDIR`** — When `cmake.launcher` resolves to `ccache`, `cmk` sets `CCACHE_BASEDIR=${PROJECT_ROOT}` and `CCACHE_NOHASHDIR=true` unless already present in the environment.

### File API queries & automatic reconfiguration

`cmk` plants stateless CMake File API v1 queries (`codemodel-v2`, `cache-v2`, `cmakeFiles-v1`, `toolchains-v1`) in `<build-dir>/.cmake/api/v1/query/` and configures managed trees with `-DCMAKE_SUPPRESS_REGENERATION=ON` so `ninja` never re-runs `cmake` with an stripped environment behind `cmk`'s back.

Before building, testing, running, installing, compiling TUs, or linting, `cmk` checks whether the managed tree is stale:
1. Missing `CMakeCache.txt`, missing File API replies, or missing/mismatched `<build-dir>/.cmk/injection.json` hash (covering generator, toolchain, launcher, dependency exports, multi-config flags, `variables`, `args`, `env`, and ad-hoc `cmk config -- ...` flags).
2. Any non-generated input recorded in `cmakeFiles-v1` is missing or newer than the File API index timestamp.
3. Any `file(GLOB ... CONFIGURE_DEPENDS)` expression recorded in `cmakeFiles-v1` matches a different file list than at configure time.

If injection inputs changed, `cmk` reconfigures with `--fresh` (re-applying any recorded ad-hoc CLI args); if only `CMakeLists.txt` / `.cmake` files changed, `cmk` re-runs `cmake` incrementally.

---

## 3. Dependency store and recipe contract

### Store identity

Pinned dependencies build into `<store-dir>/<name>-<stamp16>` (locked per entry via `flock` for safe concurrent builds across processes/worktrees). The 64-bit stamp is derived from:
- Recipe script contents (`script`),
- Source identity (archive URL + SHA-256, or Git URL + resolved commit SHA),
- Patch paths and contents (`patches`),
- Extra input paths and contents (`extra-inputs`),
- Toolchain identity (`tc.ID`, including artifact SHA-256),
- Expanded `dependencies.<name>.env`,
- Output tree hashes of direct `needs` (dependents only rebuild when a `need`'s installed files actually change).

### Recipe execution environment

Recipes execute via `bash <script>` with `PWD=$CMK_WORK` in a **sanitized environment** (ambient `CFLAGS`, `CXXFLAGS`, `LDFLAGS`, `CMAKE_*`, etc. are stripped; only `PATH`, `HOME`, `USER`, `LOGNAME`, `TMPDIR`, `TERM`, `LANG`, `LC_*`, `SSL_*`, `CURL_CA_BUNDLE`, `HTTP(S)_PROXY`, `NO_PROXY`, `SSH_AUTH_SOCK`, and `GIT_SSH_COMMAND` are inherited).

| Variable | Value |
| --- | --- |
| `CMK_SRC` | Extracted source root or checked-out Git tree (empty if `source` is omitted; points at local checkout under `cmk dev`). |
| `CMK_PREFIX` | Staging installation prefix; must be non-empty when the script exits 0. |
| `CMK_WORK` | Scratch build directory (`$PWD`). |
| `CMK_JOBS` | Parallel job count. |
| `CMK_PROJECT_ROOT` | Absolute path of the project root. |
| `CMK_TOOLCHAIN_FILE` | Path to `clangup`'s `toolchain.cmake` (when using a `clangup` toolchain with one). |
| `CMK_DEP_<NAME>_PREFIX` | Installed store prefix for each direct `needs` entry (`<NAME>` uppercased with non-alphanumerics mapped to `_`). |
| `CMAKE_PREFIX_PATH` | Colon-separated prefixes of all transitive `needs`. |
| `PKG_CONFIG_PATH` | Colon-separated `lib/pkgconfig`, `lib64/pkgconfig`, and `share/pkgconfig` directories of transitive `needs`. |
| `CC`, `CXX` | Resolved C and C++ compiler paths (`clangup` `bin/` is also prepended to `PATH`). |
| `AR`, `RANLIB`, `NM` | Set to bundled `llvm-ar`, `llvm-ranlib`, `llvm-nm` on Linux `clangup` toolchains (macOS retains Apple system archive tools). |

### Custom CMake exports (`.cmk-exports`)

By default, `cmk` exposes each dependency to the top-level CMake configure as `-D<cmake-name>_ROOT=<prefix>`. If a recipe writes lines to `$CMK_PREFIX/.cmk-exports` (ignoring blank lines and `#` comments, expanding `${CMK_PREFIX}`), those arguments replace the default `-D<cmake-name>_ROOT` injection.

---

## 4. Project state files

| File | Git status | Purpose |
| --- | --- | --- |
| `cmk.yaml` | **Commit** | Project configuration, presets, configurations, and dependency declarations. |
| `cmk.lock` | **Commit** | Per-platform resolved `clangup` toolchain releases/digests, managed `cmake`/`ninja` tool URLs and `sha256` digests, dependency archive `sha256` digests, Git commits, and per-platform dependency store stamps. |
| `cmk/deps/*` | **Commit** | Project-owned dependency build scripts and patches. |
| `cmk.dev.yaml` | **Ignore** | Machine-local `cmk dev` dependency path overrides. Rejected by `--locked`. |
| `CMakeUserPresets.json` | **Ignore** | Machine-local CMake presets generated by `cmk config` (contains host-absolute toolchain and store paths). |
| `compile_commands.json` | **Ignore** | Root compilation database mirrored from the active build tree (with synthetic C++ module stdlib BMI commands stripped). |

---

## 5. CLI reference

### Shared variant flags (`cmk build`, `run`, `test`, `install`, `build-tu`)

| Flag | Description |
| --- | --- |
| `-b, --build <dir>` | Explicit CMake build directory |
| `-p, --preset <name>` | Managed configure preset from `cmk.yaml` |
| `-c, --config <cfg>` | Multi-config build configuration (`Debug`, `Release`, `Asan`, …) |
| `-j, --jobs <n>` | Parallel job count (default: `CMK_DEFAULT_JOBS` or `max(NumCPU-1, 1)`) |
| `-v, --verbose` | Verbose build or CTest output |
| `--locked` | Require up-to-date `cmk.lock` and build tree; reject `cmk.dev.yaml` overlays |
| `--no-config` | Skip configure staleness checks (mutually exclusive with `--locked`) |

### `cmk` commands

- **`cmk new <name>`** (`n`) — Create a new C++23 project directory.
- **`cmk init [-f|--force] [-y|--yes] [--toolchain <sel|system>] [--launcher <ccache|sccache|none>] [--asan] [--gitignore]`** — Detect host/project defaults and generate `cmk.yaml`.
- **`cmk config [-p <preset> | -b <dir>] [--fresh | --if-stale] [-- <cmake-args...>]`** (`c`) — Configure a managed build tree.
- **`cmk build [target...] [-t|--target <t>]... [--clean-first] [-i|--interactive] [-- <tool-args...>]`** (`b`) — Build targets.
- **`cmk run [target] [-t|--target <t>] [--no-build] [-- <program-args...>]`** (`r`) — Build and run an executable target.
- **`cmk test [regex...] [-t|--target <t>]... [-L|--label <lbl>]... [--no-build] [-- <ctest-args...>]`** (`t`) — Build tests and run `ctest`.
- **`cmk install [--prefix <dir>] [--component <name>] [--strip] [--no-build]`** — Build and install via `cmake --install`.
- **`cmk build-tu [source-or-target...]`** (`tu`) — Build Ninja object targets resolved from source paths, exact object target names, or unambiguous substrings.
- **`cmk fmt [file...] [-a|--all | -s|--staged | -u|--unstaged] [-d|--dry-run] [-v|--verbose]`** (`f`) — Run `clang-format`.
- **`cmk lint [file...] [-b|-p|-c] [-i|--interactive | -a|--all | -s|--staged | -u|--unstaged | --commit <rev> | --branch[=<base>]] [--fix] [-W|--warnings-as-errors] [-v|--verbose]`** (`l`) — Run `clang-tidy`.
- **`cmk add <name> [--url <url> [--sha256 <hex>] | --git <url> --ref <ref>] [--cmake-name <pkg>] [--needs <a,b>] [--script <path>]`** (`a`) — Add a dependency and recipe stub.
- **`cmk sync [dep...] [-f|--force]`** (`s`) — Build dependencies into the shared store.
- **`cmk update [dep|toolchain|cmake|ninja...]`** — Re-resolve floating toolchain channels, managed `cmake`/`ninja` pins, and Git refs in `cmk.lock`.
- **`cmk dev [dep [path]] [--drop <dep>...]`** — List, set, or drop local dependency overlays in `cmk.dev.yaml`.
- **`cmk clean [--prune | --all]`** — List, prune unreferenced entries, or wipe the dependency store.
- **`cmk env`** — Print `export` statements for the project environment.
- **`cmk shell [-- <cmd...>]`** — Run `$SHELL` or `<cmd...>` inside the project environment.
- **`cmk schema [-o|--output <file>]`** — Print or write the JSON Schema for `cmk.yaml`.
- **`cmk doctor`** — Inspect project, toolchain, build tools, launcher, dependencies, and build directories (read-only).
- **`cmk completion <bash|zsh|fish|powershell>`** — Generate shell completion script.
- **`cmk version`** (`-V, --version`) — Print the `cmk` version.

### `clangup` commands and consumer JSON interface

- **`clangup update [--format text|json]`** — Refresh channel index (`clangup.update/v1`).
- **`clangup install [channel[@ver-rel]] [--file <tar.zst> | --url <https-url>] [--prefix <dir>] [--target <triple>] [--force] [--format text|json]`** — Install a toolchain (`clangup.install/v1`).
- **`clangup list [channel] [--remote [--all]] [--format text|json]`** — List installed toolchains (`clangup.toolchain-list/v1`) or remote channels/releases (`clangup.channel-list/v1`, `clangup.channel-show/v1`, `clangup.remote-list/v1`).
- **`clangup default <installed-toolchain>`** — Set active default toolchain symlink (`<data-root>/bin`).
- **`clangup env`** — Print `export PATH='<data-root>/bin':"$PATH"`.
- **`clangup uninstall <installed-toolchain>`** (`remove`) — Delete an installed toolchain and unpin default if needed.
- **`clangup gc`** — Remove `.partial` downloads, `.clangup-install-*` staging directories, and dangling install records.
- **`clangup doctor [--format text|json]`** — Check host tools, glibc/macOS runtime compatibility, index cache, and installed toolchains.
- **Build-system consumer commands** (serve installed exact selectors offline without channel index access):
  - `clangup resolve <selector> [--target <triple>] [--format text|json]` (`clangup.resolve/v1`)
  - `clangup ensure <selector> [--prefix <dir>] [--target <triple>] [--format text|json]` (`clangup.resolve/v1` with `install` populated)
  - `clangup path <selector> [--target <triple>] [--format text|json]` (`clangup.path/v1`)
- **`clangup` exit codes**: `0` success, `1` internal error, `2` invalid request/selector, `3` repository/index error, `5` install/resolution failure (JSON errors emit `clangup.error/v1`).

---

## 6. Environment variables and filesystem layout

### `clangup`

| Variable | Default | Purpose |
| --- | --- | --- |
| `CLANGUP_HOME` | `$XDG_DATA_HOME/clangup` or `~/.local/share/clangup` | Installed toolchains (`toolchains/<channel>/<ver-rel>/<triple>`), records (`installed/`), and default `bin/` symlinks |
| `CLANGUP_CONFIG_HOME` | `$XDG_CONFIG_HOME/clangup` (OS config dir + `/clangup`) | Cached channel index (`index.json`) |
| `CLANGUP_CACHE_HOME` | `$XDG_CACHE_HOME/clangup` (OS cache dir + `/clangup`) | Content-addressed downloaded objects (`objects/sha256/`) |
| `CLANGUP_INDEX_URL` | `https://dl.clangup.dev/index.json` | Channel index endpoint |

### `cmk`

| Variable | Default | Purpose |
| --- | --- | --- |
| `CMK_STORE_DIR` | `$XDG_DATA_HOME/cmk/store` or `~/.local/share/cmk/store` | Content-addressed dependency store (`<name>-<stamp16>`) and mutable `dev/` worktrees |
| `CMK_TOOLS_DIR` | `$CLANGUP_HOME/tools` (`~/.local/share/clangup/tools`) | Shared extracted cache for managed `cmake` and `ninja` binaries |
| `CMK_CMAKE_DOWNLOAD_BASE` | `https://github.com/Kitware/CMake/releases/download` | Base URL override for upstream Kitware CMake archives and `SHA-256.txt` |
| `CMK_TOOLS_BASE_URL` | Base of `CLANGUP_INDEX_URL` (`https://dl.clangup.dev`) | Base URL override for `clangup` R2 tool releases (`tools/ninja/<ver>/release.json`) |
| *(download cache)* | `$XDG_CACHE_HOME/cmk/downloads` or `~/.cache/cmk/downloads` | Cached dependency source archives (`<sha256>-<filename>`) |
| `CMK_DEFAULT_JOBS` | `max(NumCPU - 1, 1)` | Default parallelism when `-j/--jobs` is omitted |
| `CMK_MAX_DEPTH` | `2` | Directory scan depth when discovering unmanaged CMake build trees |
| `CC`, `CXX` | Discovered on `PATH` | Fallback compilers when no `clangup` selector applies |
| `LLVM_SYMBOLIZER_PATH` | `<clangup-prefix>/bin/llvm-symbolizer` | Respected when already set; otherwise auto-injected from the selected `clangup` toolchain |
| `CCACHE_BASEDIR`, `CCACHE_NOHASHDIR` | `${PROJECT_ROOT}`, `true` | Respected when already set; otherwise auto-injected when `cmake.launcher` is `ccache` |
