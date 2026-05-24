```sh
docker build \
  -f docker/clangup-builder/Dockerfile \
  -t clangup-builder:el7-glibc2.17-v1 .
```

```sh
docker build \
  -f docker/clangup-builder/Dockerfile \
  --build-arg BASE_IMAGE=quay.io/centos/centos:8 \
  --build-arg BASE_PROFILE=el8 \
  -t clangup-builder:el8-aarch64-glibc2.28-v1 .
```
