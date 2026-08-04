# Using clangup and cmk

## Install and select a toolchain

Update channel metadata, install a selector, and expose its tools in the
current shell:

```sh
clangup update
clangup channel list
clangup install libcxx
clangup default libcxx
eval "$(clangup env)"
```

A selector is a channel such as `libcxx`, or an exact release such as
`libcxx@22.1.8-1`. A channel follows its current release; an exact release is
stable. `default` uses the host's standard library, linker, runtime, and
unwind library. On Linux, `libcxx` uses libc++, LLD, compiler-rt, and libgcc_s.

Useful lifecycle commands:

```sh
clangup list
clangup default libcxx@22.1.8-1
clangup resolve libcxx@22.1.8-1 --format=json
clangup path libcxx@22.1.8-1
clangup uninstall libcxx@22.1.8-1
```

`resolve`, `ensure`, and `path` can use an installed exact release without
accessing channel metadata. Import an archive with `clangup install --file
<archive>` or `clangup install --url <https-url>`; the artifact manifest must
be beside the archive.

## Configure a cmk project

Create `cmk.yaml` with a toolchain selector and one CMake preset. Then
configure its build tree:

```sh
cmk config
```

Build, run, test, and install from that tree:

```sh
cmk build
cmk build -c Release app -j8
cmk run app -- --help
cmk test -c Release
cmk install -c Release
```

Use `-p` to select a preset and `-c` to select a configuration of a
multi-config build. A single-config preset selects its configuration through
`build-type` and does not use `-c`.

`cmk.lock` records resolved toolchains, Git revisions, and dependency build
identities. Commit it with `cmk.yaml`. Refresh floating toolchain or Git pins
with `cmk update`, then build the affected dependencies with `cmk sync`.

## Work with a CMake tree not managed by cmk

For a project without `cmk.yaml`, cmk operates an existing CMake build tree:

```sh
cmk build -b build
cmk build -b build -c Release app -- -v
cmk run app -- --help
cmk test
```

cmk preserves that tree's generator, toolchain, and cache settings. `fmt` and
`lint` use `clang-format` and `clang-tidy` from `PATH` for such a tree.

## Add a custom dependency

Add an archive or Git source. The command adds the `cmk.yaml` entry and creates
a recipe stub when the script does not exist:

```sh
cmk add zlib \
  --url https://github.com/madler/zlib/releases/download/v1.3.1/zlib-1.3.1.tar.gz \
  --cmake-name ZLIB

cmk add fmt \
  --git https://github.com/fmtlib/fmt.git \
  --ref 11.0.2 \
  --cmake-name fmt
```

Edit the recipe so it installs the dependency into `$CMK_PREFIX`, then sync and
configure:

```sh
cmk sync zlib
cmk config
```

Use `--needs dep1,dep2` when the recipe consumes other cmk-managed
dependencies. Commit `cmk.yaml`, `cmk.lock`, and the recipe. The project uses
the resulting package through its ordinary `find_package()` call.

## Apply a source patch

Keep a project-owned patch beside the recipe and declare it in `cmk.yaml`:

```yaml
dependencies:
  zlib:
    patches: [cmk/patches/zlib-fix.patch]
```

Run `cmk sync zlib` after changing the patch. cmk applies it to the pinned
source before running the recipe.

## Apply a temporary local overlay

Use a dev overlay to build a dependency from a local fork or working tree
without changing the shared dependency definition:

```sh
# Add cmk.dev.yaml to this project's .gitignore.
cmk dev zlib ../zlib
cmk build

# Edit ../zlib and build again.
cmk dev                   # list active overlays
cmk dev --drop zlib       # restore the pinned source
```

cmk retains the overlay's work directory and detects changes on every build or
sync. Its dependents rebuild only when the overlay's installed output changes.
`cmk.dev.yaml` is machine-local; `cmk.yaml` and `cmk.lock` remain unchanged.
`--locked` rejects active overlays. A local overlay uses its checkout directly,
so patches declared in `cmk.yaml` are not applied again.

## Format and lint

```sh
cmk fmt --staged
cmk fmt --all --dry-run
cmk lint --commit HEAD
cmk lint --branch
cmk lint -p default -c Release src/file.cc
cmk lint src/file.cc --fix
```

`cmk lint` needs a compilation database. Run `cmk config` for a managed
project when one has not been generated yet.
