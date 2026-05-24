# final-clang

These Dockerfiles build a final clangup toolchain tarball. They do not publish a
runtime image. Use Docker BuildKit output to copy `/out` from the final scratch
stage to the host.

## Bootstrap build

Use this when `/opt/clangup-seed` is already provided by the seed clang image:

```sh
docker buildx build \
  -f docker/final-clang/Dockerfile.bootstrap \
  --build-arg BASE_IMAGE=clangup-seed-clang:llvmorg-21.1.8-el7-x86_64 \
  --build-arg BASE_PROFILE=el7 \
  --output type=local,dest=dist/final-bootstrap \
  .
```

## Release build

Use this when building from a previous final tarball. The tarball content should
be the toolchain prefix itself, so it can be extracted directly into
`/opt/clangup-seed`. `STAGE0_TARBALL` is resolved inside the Docker build
context, so copy or symlink the previous tarball under the repository before
running `docker buildx build`.

```sh
mkdir -p .artifacts
cp /path/to/clangup-final-llvmorg-21.1.8-el7-x86_64.tar.zst \
  .artifacts/clangup-stage0.tar.zst

docker buildx build \
  -f docker/final-clang/Dockerfile.release \
  --build-arg BASE_IMAGE=clangup-builder:el7-glibc2.17-v1 \
  --build-arg BASE_PROFILE=el7 \
  --build-arg STAGE0_TARBALL=.artifacts/clangup-stage0.tar.zst \
  --output type=local,dest=dist/final-release \
  .
```

The output directory contains:

```text
clang-<version>-<arch>.tar.zst
clang-<version>-<arch>.manifest.json
```

The manifest carries build metadata including `artifact.sha256` (the tarball
hash), LLVM source sha256, base profile, glibc baseline, target triple,
seed/final compiler versions, and default toolchain libs.
