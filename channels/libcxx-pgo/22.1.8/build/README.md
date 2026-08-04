# libcxx-pgo build

The Linux build uses the published `default@22.1.8-1` toolchain and the
clangup builder image. Its stages are:

1. build an IR-instrumented Clang;
2. train it by building Debug and Release Clang and LLD;
3. merge the raw profiles;
4. build the final PGO and ThinLTO distribution;
5. apply LBR-based BOLT optimization on x86_64.

The x86_64 BOLT stage rewrites Clang, LLD, `libclang-cpp.so`, and
`libLLVM.so`. It requires hardware branch sampling and runs on the dedicated
self-hosted GitHub runner. The aarch64 artifact uses PGO and ThinLTO.

The release pipeline is split into independently dispatched workflows. Each
downstream workflow accepts the upstream workflow run ID, downloads its named
artifact, checks the recorded commit and resolved channel plan, and continues
from that stage's output:

1. `libcxx-pgo x86 profile` produces `libcxx-pgo-x86-profile`.
2. `libcxx-pgo x86 final` consumes that profile and produces
   `libcxx-pgo-x86-final`.
3. `libcxx-pgo x86 BOLT` consumes the final-prefix artifact and produces
   `libcxx-pgo-x86-target`.
4. `libcxx-pgo aarch64 profile` produces `libcxx-pgo-aarch64-profile`.
5. `libcxx-pgo aarch64 final` consumes that profile and produces
   `libcxx-pgo-aarch64-target`.
6. `libcxx-pgo publish` consumes the x86 BOLT and aarch64 final target
   artifacts.

Profile artifacts contain the merged `clang.profdata`; the x86 final artifact
contains a compressed final prefix, the merged profile, and its CMake argument
records. The target artifacts contain the packaged toolchain, manifest, channel
plan, and build commit. The BOLT runner is reserved for trusted builds of the
protected release branch. It has host perf access and does not run untrusted
workflow code.
