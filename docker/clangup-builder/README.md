# clangup builder image

The builder image provides the native build environment shared by LLVM
toolchain builds:

- GCC and the platform C/C++ development files;
- CMake, Ninja, Python, Perl, Git and archive tools;
- static compression, TLS, HTTP and XML dependencies under
  `/opt/clangup-builder`.

The multi-architecture image uses an EL7 base for `linux/amd64` and an EL8 base
for `linux/arm64`.

## Build

```sh
docker build \
  --network host \
  -f docker/clangup-builder/Dockerfile \
  -t clangup-builder:1 .
```

```sh
docker build \
  --network host \
  --platform linux/arm64 \
  --build-arg BASE_IMAGE=quay.io/centos/centos:8 \
  --build-arg BASE_PROFILE=el8 \
  -f docker/clangup-builder/Dockerfile \
  -t clangup-builder:1 .
```

Published tags are `1` and `latest`.
