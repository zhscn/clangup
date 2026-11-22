# GCC 16.2

The GCC channel builds C and C++ toolchains on x86-64 EL7, ARM64 EL8 and
Apple Silicon macOS 14 or later. The three-stage bootstrap starts with Clang.
The GCC it builds takes over the following stages and builds the paired libgcc
and libstdc++ runtimes.

Use the matching clangup Linux builder with `gmp-devel`, `mpfr-devel`,
`libmpc-devel` and `flex` installed. Set `CLANGUP_BOOTSTRAP_PREFIX` to the
installed Clang prefix. The installed GCC toolchain uses the platform binutils,
glibc development files and GMP, MPFR and MPC runtime packages.

On Apple Silicon, install Xcode and the Homebrew packages `make`, `gmp`,
`mpfr`, `libmpc`, `zstd` and `gnu-tar`. Point `CLANGUP_BOOTSTRAP_PREFIX` at
the Xcode toolchain containing Apple Clang. The macOS target applies the
[GCC 16.2 Darwin patch] used by Homebrew. It links the math libraries
statically and finds the active SDK via `xcrun` when `SDKROOT` is unset.

Generate the plan and download the GCC source archive listed in it. Run the
build in the environment for the target you select.

```sh
go run ./cmd/channel-plan channels/gcc/16.2.0/release.yaml plan.json
python3 channels/build/run.py --plan plan.json \
  --source gcc-16.2.0.tar.xz --target x86_64-unknown-linux-gnu \
  --channel-root channels/gcc/16.2.0 --config-dir channels/gcc/16.2.0/build \
  --work /work/gcc --output /work/artifacts
```

The builder verifies the source archive, bootstraps GCC and tests a C++20
program before packaging. The manifest and target descriptor accompany the
archive for installation and release assembly. Install a local artifact with
`clangup install --file /work/artifacts/toolchain.tar.zst`.

For single-file deployment, link the application with
`-static-libstdc++ -static-libgcc`. Check the remaining system dependencies and
run the application on the deployment platform.

[GCC 16.2 Darwin patch]: https://github.com/Homebrew/homebrew-core/blob/af6b5d310c29ce89ac70b7b9094022f8ebb5c005/Patches/gcc/gcc-16.2.0.diff
