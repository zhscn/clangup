# Default channel build

The runner consumes only a canonical locked spec and verified source inputs.
It does not parse the authoring YAML or publish repository metadata.

Python handles source and patch verification, payload validation, packaging,
and structured build metadata. The LLVM build itself is defined by
`build-linux.sh` or `build-macos.sh` together with the platform CMake cache.
Those scripts expose the configure, build, and install stages directly.
CMake cache entries are defined by `common.cmake` and one platform cache.
Build scripts pass dynamic values through the environment and invoke CMake
with `-C`; a cache variable has one owner within each configure invocation.

```sh
python3 specs/default/build/run.py \
  --spec-lock out/spec.lock.json \
  --target x86_64-unknown-linux-gnu \
  --source .cache/sources/llvm-project-22.1.8.src.tar.xz \
  --bundle specs/default/22.1.8 \
  --work .cache/build/default-x86_64 \
  --output out/default/x86_64
```

Linux runs inside a bootstrap environment supplied through
`CLANGUP_BOOTSTRAP_PREFIX`; this may be the seed toolchain or an exact published
toolchain. macOS runs natively with Xcode.

Each target emits an artifact, detached manifest, build record, and
`release-fragment.json`. `stage.py` uploads these files as immutable,
content-addressed objects through the repository presign service and emits a
small `staged-target.json` reference.

```sh
python3 specs/default/build/stage.py \
  --endpoint "$CLANGUP_UPLOAD_ENDPOINT" \
  --output out/default/x86_64/staged-target.json \
  target \
  --fragment out/default/x86_64/release-fragment.json
```

The source archive, patch series, and locked spec use the same upload path and
produce `staged-inputs.json`. Once every required target reference exists,
`release.py` validates the matrix and writes the immutable release descriptor:

```sh
python3 specs/default/build/release.py \
  --spec-lock out/spec.lock.json \
  --inputs out/staged-inputs.json \
  --targets out/default \
  --output out/release.json
```
