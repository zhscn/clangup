The seed image is a fixed GCC-hosted Clang used only to bootstrap LLVM builds.
Its final stage contains no GCC driver. It inherits `glibc-devel` from the
builder image, providing the libc headers and startup objects required to
compile and link programs. Clang defaults to the bundled libstdc++ headers and
runtime under `/opt/clangup-seed/gcc`; static libc++ and libc++abi development
files and compiler-rt builtins are also installed so final LLVM builds can use
the seed as a complete bootstrap runtime.

```sh
docker build \
  --network host \
  -f docker/clangup-seed/Dockerfile \
  --build-arg BASE_IMAGE=clangup-builder:1 \
  --build-arg BASE_PROFILE=el7 \
  -t clangup-seed:22.1.8-1 .
```

```sh
docker build \
  --network host \
  --platform linux/arm64 \
  -f docker/clangup-seed/Dockerfile \
  --build-arg BASE_IMAGE=clangup-builder:1 \
  --build-arg BASE_PROFILE=el8 \
  -t clangup-seed:22.1.8-1 .
```

The published `clangup-seed:22.1.8-1` tag is a multi-architecture image.
Docker selects its `linux/amd64` or `linux/arm64` manifest automatically. The
moving `clangup-seed:latest` tag points to the most recently published release.
