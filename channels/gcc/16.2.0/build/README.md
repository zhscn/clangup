# GCC 16.2 for EL7

Build this channel in an x86-64 EL7 environment with glibc 2.17. GCC's
three-stage bootstrap starts with an installed clangup Clang and uses the GCC
it builds for the following stages. The final compiler builds the paired
libgcc and libstdc++ runtimes against the EL7 development headers and libraries.

To run the toolchain on EL7, install the system binutils, glibc development
files, and the GMP, MPFR and MPC runtime packages used by the compiler.

Use the EL7 clangup builder with `gmp-devel`, `mpfr-devel`, `libmpc-devel` and
`flex` installed. Put the builder's Python, GNU tar and zstd on `PATH`. Mount or
install the seed toolchain in the container and point `CLANGUP_BOOTSTRAP_PREFIX`
at its prefix.

Generate a plan with `go run ./cmd/channel-plan channels/gcc/16.2.0/release.yaml
plan.json`. Download the source archive named by the plan, then run the build
from the repository root inside the builder:

```sh
python3 channels/build/run.py --plan plan.json \
  --source gcc-16.2.0.tar.xz --target x86_64-unknown-linux-gnu \
  --channel-root channels/gcc/16.2.0 --config-dir channels/gcc/16.2.0/build \
  --work /work/gcc --output /work/artifacts
```

The builder checks the archive hash, bootstraps GCC and tests a C++20 program
before packaging the toolchain. The manifest and target descriptor accompany
the archive for installation and release assembly. Install the local artifact
with `clangup install --file /work/artifacts/toolchain.tar.zst`.

For a single-file deployment on EL7, link the application with
`-static-libstdc++ -static-libgcc` and use the system glibc dynamically. Check
the application's remaining dynamic dependencies and run it on EL7.
