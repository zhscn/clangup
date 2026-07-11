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
  -f docker/clangup-builder/Dockerfile \
  --build-arg BASE_IMAGE=quay.io/centos/centos:8 \
  --build-arg BASE_PROFILE=el8 \
  -t clangup-builder:1 .
```

The builder contains the glibc development headers, startup objects, and the
`libgcc_s` linker name needed by Clang, but no system compiler. The published
`clangup-builder:1` tag is a multi-architecture image. Its `linux/amd64`
manifest uses the EL7 profile, while `linux/arm64` uses EL8.
Docker selects the matching manifest for the host architecture. The moving
`clangup-builder:latest` tag points to the most recently published version.
