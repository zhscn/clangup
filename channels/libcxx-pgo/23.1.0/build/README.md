# libcxx-pgo build

The Linux build uses the published `default@23.1.0-1` toolchain and the
clangup builder image. The macOS build uses Apple Clang and the SDK linker,
archiver, symbol, and indexing tools supplied by Xcode. Both builds use these
PGO stages:

1. build an IR-instrumented Clang;
2. train it with Debug and Release LLVM builds (Clang on macOS, Clang and LLD
   on Linux);
3. merge the raw profiles;
4. build the final PGO distribution.

Linux final builds also use ThinLTO. The x86_64 Linux build applies LBR-based
BOLT optimization after the PGO build.

The x86_64 BOLT stage rewrites Clang, LLD, `libclang-cpp.so`, and
`libLLVM.so`. It requires hardware branch sampling and runs on the dedicated
self-hosted GitHub runner. The aarch64 artifact uses PGO and ThinLTO.

The release pipeline is split into independently dispatched workflows. Each
downstream workflow accepts its required upstream workflow run IDs, downloads
their artifacts, checks the recorded commit and resolved channel plan, and
continues from those inputs:

1. Each architecture builds an instrumented compiler.
2. The Debug and Release PGO training workflows consume that compiler in
   parallel and each produce a `clang.profdata` artifact.
3. The final workflow consumes both profiles, merges them, and builds the PGO
   and ThinLTO distribution.
4. On x86_64, the Debug and Release BOLT sampling workflows consume the final
   prefix independently and produce compact `.fdata` artifacts.
5. The x86_64 BOLT reorder workflow consumes both `.fdata` artifacts and the
   final prefix to produce the x86_64 target artifact.
6. The Linux publish workflow consumes the x86_64 and aarch64 Linux target
   artifacts.

The macOS workflow contains the same PGO stage boundaries as dependent jobs
in one independently dispatched workflow. Its final job publishes the macOS
target without depending on a Linux workflow run. The release service merges
independently published targets that have the same channel release identity.

The instrumented and final-prefix artifacts are compressed tar archives to
preserve symlinks, permissions, and the installed runtime layout. BOLT sampling
converts each `perf.data` file to `.fdata` before upload and removes its local
work directory after the artifact upload. The BOLT runner is reserved for
trusted builds of the protected release branch. It has host perf access and
does not run untrusted workflow code.
