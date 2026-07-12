# clangup

`clangup` installs and manages the LLVM toolchains published at
`dl.clangup.dev`.

```sh
clangup update
clangup channel list
clangup channel show default

clangup install default
clangup install default@22.1.8-1

clangup list
clangup default default@22.1.8-1
clangup uninstall default@22.1.8-1
```

The first installed toolchain becomes the default. Add its command shims to the
current shell with:

```sh
eval "$(clangup env)"
```

Build-system integrations can resolve and install an exact toolchain without
changing the default:

```sh
clangup resolve default@22.1.8-1 --format=json
clangup ensure default@22.1.8-1 --format=json
clangup path default@22.1.8-1
```

`CLANGUP_INDEX_URL` overrides the official index URL for development and local
testing.

Local artifacts are installable with a sibling `manifest.json`:

```sh
clangup install --file ./toolchain.tar.zst
clangup install --url https://example.com/toolchain.tar.zst
```
