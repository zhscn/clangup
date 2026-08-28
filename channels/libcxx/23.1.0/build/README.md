# Libcxx channel build

`release.yaml` defines the source, patchset, target compatibility, delivered
runtimes and driver contract.

The channel workflow runs Linux builds in the pinned builder image and extracts
the matching pinned `default` channel artifact at `/opt/clangup-bootstrap`.
The CMake cache files build Clang and its tools against static libc++ while
configuring the installed driver to select libc++, lld, compiler-rt and libgcc
by default.

Each target build produces `toolchain.tar.zst`, `manifest.json` and
`target.json`. The shared channel release scripts assemble and publish these
outputs.
