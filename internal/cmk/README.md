# cmk

`cmk` is an independent CMake project command distributed from the clangup
repository. Its implementation lives in `internal/cmk`; `cmd/cmk` contains the
binary entry point.

A project selects clangup toolchains with the same selectors accepted by the
clangup CLI. Platform selectors let one project use channels with different
driver contracts:

```toml
[toolchain]
linux = "libcxx@22.1.8-1"
macos = "default@22.1.8-1"
linux-aarch64 = "libcxx-pgo@22.1.8-1"
```

Floating channel selectors such as `libcxx` resolve through `clangup ensure`.
An exact OS-architecture entry overrides its OS selector. The optional
`selector` key supplies a fallback. `cmk.lock` records each platform's exact
channel release, target, manifest digest and artifact digest. Configure and
dependency commands consume the installed prefix and `toolchain.cmake`
returned by clangup's JSON interface.

When a project has an existing CMake build directory and no toolchain selector,
`cmk build` invokes `cmake --build` against that tree with its existing compiler
and regeneration rules. A build directory carrying a cmk injection stamp keeps
the managed configure and staleness workflow.
