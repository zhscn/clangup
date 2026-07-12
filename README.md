# clangup

`clangup` installs and manages relocatable Clang toolchains.

## Install

Download the binary for the host from
[GitHub Releases](https://github.com/zhscn/clangup/releases):

- `clangup-linux-amd64`
- `clangup-linux-arm64`
- `clangup-darwin-arm64`

Install it on `PATH`, for example:

```sh
mkdir -p ~/.local/bin
install -m 755 clangup-linux-amd64 ~/.local/bin/clangup
```

## Usage

Add a toolchain source and inspect its channels:

```sh
clangup repo add https://dl.clangup.dev/catalog-v1.json
clangup channel list
clangup channel show dl.clangup.dev/llvm/default
```

Install the current channel release or an exact release:

```sh
clangup install dl.clangup.dev/llvm/default
clangup install dl.clangup.dev/llvm/default@22.1.8-1
```

When exactly one source is configured, `clangup install` selects its default
channel. The host target is selected automatically; `--target` selects an
explicit target triple.

Manage installed toolchains:

```sh
clangup list
clangup default dl.clangup.dev/llvm/default
clangup uninstall dl.clangup.dev/llvm/default
clangup gc
```

The first installed toolchain becomes the default. Add its command shims to the
current shell with:

```sh
eval "$(clangup env)"
```

Check the host, external driver requirements, installed files and compiler
runtime behavior:

```sh
clangup doctor
clangup doctor --full
```

Build-system integrations can resolve exact compiler paths without using the
default toolchain:

```sh
clangup resolve dl.clangup.dev/llvm/default --install --format=json
```

Local or externally hosted artifacts can be installed with their sibling
manifest:

```sh
clangup install --file ./clang-22.1.8-1-x86_64-unknown-linux-gnu.tar.zst
clangup install --url https://example.com/clang.tar.zst
```
