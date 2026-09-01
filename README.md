# clangup

`clangup` installs versioned LLVM toolchains. `cmk` uses those toolchains to
configure and operate CMake projects.

Install the latest `clangup` and `cmk` binaries for the current platform:

```sh
curl -fsSL https://dl.clangup.dev/install.sh | sh
```

The installer writes to `~/.local/bin` by default. Pass another destination
when that directory is not suitable:

```sh
curl -fsSL https://dl.clangup.dev/install.sh | sh -s -- --bin-dir /usr/local/bin
```

The binaries are downloaded from
[GitHub Releases](https://github.com/zhscn/clangup/releases).

## Start here

Install a toolchain and make it available to the current shell:

```sh
clangup update
clangup install default
eval "$(clangup env)"
```

Add `cmk.yaml` to a CMake project:

```yaml
version: 1

toolchain:
  default: default

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

Configure and build:

```sh
cmk config
cmk build
cmk test
```

## Documentation

- [Usage guide](docs/usage.md) covers toolchain installation, project work,
  custom dependencies, patches, and local dependency overlays.
- [Project reference](docs/reference.md) defines `cmk.yaml`, `cmk.lock`, and
  the dependency recipe environment.
- [CLion integration](integrations/clion-cmk/README.md) lets cmk configure a
  shared build tree while CLion retains its native CMake project workflow.

Run `clangup --help` or `cmk --help` for the complete command list. Use
`clangup doctor` and `cmk doctor` to inspect toolchain and project resolution.
