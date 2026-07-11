The seed image is a fixed GCC-hosted Clang used only to bootstrap LLVM builds.
Its final stage contains no GCC driver. Clang defaults to the bundled libstdc++
headers and runtime under `/opt/clangup-seed/gcc`; static libc++ and libc++abi
development files are also installed so final LLVM builds can explicitly use
`-stdlib=libc++`.

```sh
docker build \
  --network host \
  -f docker/clangup-seed/Dockerfile \
  --build-arg BASE_IMAGE=clangup-builder:el7-glibc2.17-v1 \
  --build-arg BASE_PROFILE=el7 \
  -t clangup-seed:llvm22-el7-glibc2.17-v1 .
```

```sh
docker build \
  --network host \
  --platform linux/arm64 \
  -f docker/clangup-seed/Dockerfile \
  --build-arg BASE_IMAGE=clangup-builder:el8-aarch64-glibc2.28-v1 \
  --build-arg BASE_PROFILE=el8 \
  -t clangup-seed:llvm22-el8-aarch64-glibc2.28-v1 .
```
