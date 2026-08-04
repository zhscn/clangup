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

The BOLT runner is reserved for trusted builds of the protected release branch.
It has host perf access and does not run untrusted workflow code.

Build work is retained under the self-hosted runner's cache and keyed by the
repository commit. Re-running the same commit resumes from completed stage
checkpoints whose plan, source, build configuration, and runner inputs match.
