```sh
docker build \
  -f docker/seed-clang/Dockerfile \
  --build-arg BASE_IMAGE=clangup-builder:el7-glibc2.17-v1 \
  --build-arg BASE_PROFILE=el7 \
  -t clangup-seed-clang:llvmorg-21.1.8-el7-x86_64 .
```

```sh
docker build \
  -f docker/seed-clang/Dockerfile \
  --build-arg BASE_IMAGE=clangup-builder:el8-aarch64-glibc2.28-v1 \
  --build-arg BASE_PROFILE=el8 \
  -t clangup-seed-clang:llvmorg-21.1.8-el8-aarch64 .
```
