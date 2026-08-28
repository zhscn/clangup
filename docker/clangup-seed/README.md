# clangup seed image

The seed image provides Clang 22.1.8 for bootstrapping LLVM toolchain builds.
It contains:

- Clang, LLD and LLVM archive tools;
- the native GCC toolchain used by Clang's default driver;
- static libc++, libc++abi and compiler-rt builtins;
- the build tools inherited from `clangup-builder`.

Clang and the default driver use the native GCC runtime. Programs may select
the packaged static libc++ with `-stdlib=libc++`.

The image build verifies the default driver, C++20 libc++, compiler-rt builtins,
LTO and LLVM archive tools.

## Build

```sh
docker build \
  --network host \
  --build-arg BASE_IMAGE=clangup-builder:1 \
  --build-arg BASE_PROFILE=el7 \
  -f docker/clangup-seed/Dockerfile \
  -t clangup-seed:22.1.8-1 .
```

```sh
docker build \
  --network host \
  --platform linux/arm64 \
  --build-arg BASE_IMAGE=clangup-builder:1 \
  --build-arg BASE_PROFILE=el8 \
  -f docker/clangup-seed/Dockerfile \
  -t clangup-seed:22.1.8-1 .
```

Published tags are `22.1.8-1` and `latest`.
