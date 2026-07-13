# clangup

`clangup` installs versioned LLVM toolchains from `dl.clangup.dev`. `cmk`
selects those toolchains for CMake projects and provides configure, build, test,
run, format, lint, install, and dependency commands.

Download the `clangup` and `cmk` binaries for your platform from
[GitHub Releases](https://github.com/zhscn/clangup/releases), make them
executable, and place them on `PATH`.

## clangup

```sh
clangup update
clangup channel list
clangup install default
clangup install libcxx@22.1.8-1

clangup list
clangup default libcxx@22.1.8-1
eval "$(clangup env)"
clangup doctor --full
```

`default` follows the host C++ standard library, linker, runtime, and unwind
library. On Linux, `libcxx` defaults to static libc++, LLD, compiler-rt, and
libgcc_s.

A selector is either a channel such as `libcxx` or an exact release such as
`libcxx@22.1.8-1`. Channel selectors track the current release.

```sh
clangup ensure libcxx@22.1.8-1
clangup path libcxx@22.1.8-1
clangup uninstall libcxx@22.1.8-1
```

## cmk

Add a `cmk.yaml` file to a CMake project:

```yaml
version: 1

toolchain:
  linux: libcxx
  macos: default

cmake:
  generator: Ninja Multi-Config
  default-preset: default
  default-configuration: Debug

  presets:
    default:
      build-dir: build

  configurations:
    - name: Debug
    - name: Release
```

Configure and build the project:

```sh
cmk config
cmk build
cmk build -c Release app -j8
cmk run app -- --help
cmk test -c Release
cmk install -c Release
```

Use `-p` to select a preset and `-c` to select a multi-config configuration.
Presets may override the generator, CMake variables, configurations, and their
default configuration. Single-config presets use `build-type` instead of `-c`.

`inherits` reuses another preset's settings. Each preset has its own
`build-dir`, which defaults to `build/<preset>`.

Toolchains resolve by platform, then OS, then `default`; for example,
`linux-aarch64` takes precedence over `linux`. `cmk.lock` pins resolved
toolchains and dependencies.

### Configuration example

```yaml
version: 1

toolchain:
  default: default
  linux: libcxx
  linux-aarch64: libcxx@22.1.8-1
  macos: default

cmake:
  generator: Ninja Multi-Config
  default-preset: default
  default-configuration: Debug
  compile-commands: default
  launcher: ccache

  variables:
    CMAKE_TOOLCHAIN_FILE: ${VCPKG_ROOT}/scripts/buildsystems/vcpkg.cmake
    VCPKG_OVERLAY_TRIPLETS: ${PROJECT_ROOT}/triplets
    VCPKG_OVERLAY_PORTS: ${PROJECT_ROOT}/overlay-ports
    CMAKE_COLOR_DIAGNOSTICS: true

  presets:
    default:
      build-dir: build/default
      variables:
        ENABLE_OPTIONAL_FEATURES: true
    minimal:
      inherits: default
      build-dir: build/minimal
      configurations: [Debug, Release]
      default-configuration: Release
      variables:
        ENABLE_OPTIONAL_FEATURES: false
    release:
      build-dir: build/release
      generator: Ninja
      build-type: Release
      variables:
        ENABLE_LTO: true

  configurations:
    - name: Debug
    - name: Release
    - name: RelWithDebInfo
      compile: [-fno-omit-frame-pointer]
    - name: Asan
      inherits: Debug
      compile: [-fsanitize=address, -fno-omit-frame-pointer]
      link: [-fsanitize=address]

install:
  prefix: ${PROJECT_ROOT}/dist
  strip: false

env:
  APP_CONFIG: ${PROJECT_ROOT}/config

target-env:
  app:
    ASAN_OPTIONS: detect_leaks=1

format:
  ignore: [third_party/**, build/**]

lint:
  ignore: [third_party/**, build/**]
  header-filter: ^(src|include)/
  warnings-as-errors: true
  extra-args: [--checks=bugprone-*,performance-*,modernize-*]

dependencies:
  zlib:
    script: cmk/deps/zlib.sh
    cmake-name: ZLIB
    source:
      url: https://github.com/madler/zlib/releases/download/v1.3.1/zlib-1.3.1.tar.gz
      sha256: 9a93b2b7dfdac77ceba5a558a580e74667dd6fede4585b91eefb60f03b72df23
```

Common project commands:

```sh
cmk config minimal
cmk build -p minimal -c Release
cmk build -p release
cmk update toolchain
cmk sync

cmk fmt --staged
cmk lint --branch
cmk lint src/file.cc --fix
```

### Existing CMake build trees

`cmk build` can also invoke CMake for a project without `cmk.yaml`:

```sh
cmk build -b build
cmk build -b build -c Release app -- -v
```

Targets, configuration, parallelism, and arguments after `--` are forwarded to
`cmake --build`.

Run `clangup --help`, `cmk --help`, `clangup doctor`, or `cmk doctor` for the
complete command reference and diagnostics.
