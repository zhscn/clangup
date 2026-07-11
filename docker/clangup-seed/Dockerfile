ARG BASE_IMAGE=clangup-builder:1

# -----------------------------------------------------------------------------
# Fixed seed release
# -----------------------------------------------------------------------------

ARG LLVM_VERSION=22.1.8
ARG LLVM_SHA256=922f1817a0df7b1489272d18134ee0087a8b068828f87ac63b9861b1a9965888
ARG SEED_PREFIX=/opt/clangup-seed

# -----------------------------------------------------------------------------
# Seed platform toolchain
# -----------------------------------------------------------------------------

FROM ${BASE_IMAGE} AS seed-base

ARG BASE_PROFILE=el7

SHELL ["/bin/bash", "-o", "pipefail", "-c"]

RUN set -eux; \
    package_manager="$(command -v dnf || command -v yum)"; \
    package_manager_opts=(--setopt=retries=10 --setopt=timeout=120); \
    case "${BASE_PROFILE}" in \
      el7) \
        "${package_manager}" "${package_manager_opts[@]}" install -y \
          devtoolset-11-binutils devtoolset-11-gcc devtoolset-11-gcc-c++; \
        source /opt/rh/devtoolset-11/enable; \
        ;; \
      el8) \
        "${package_manager}" "${package_manager_opts[@]}" install -y \
          gcc gcc-c++ libstdc++-static; \
        ;; \
      *) \
        echo "unsupported BASE_PROFILE=${BASE_PROFILE}" >&2; \
        exit 2; \
        ;; \
    esac; \
    "${package_manager}" clean all; \
    rm -rf /var/cache/yum /var/cache/dnf

# -----------------------------------------------------------------------------
# GCC-hosted seed build
# -----------------------------------------------------------------------------

FROM seed-base AS seed-builder

ARG BASE_PROFILE=el7
ARG LLVM_VERSION
ARG LLVM_SHA256
ARG SEED_PREFIX
ARG NINJA_JOBS=4
ARG LLVM_PARALLEL_LINK_JOBS=1

ENV CLANGUP_BUILDER_PREFIX=/opt/clangup-builder

SHELL ["/bin/bash", "-o", "pipefail", "-c"]

WORKDIR /tmp/clangup-seed-src

RUN set -eux; \
    source "${CLANGUP_BUILDER_PREFIX}/env"; \
    case "${BASE_PROFILE}" in \
      el7) source /opt/rh/devtoolset-11/enable; gcc_toolchain=/opt/rh/devtoolset-11/root/usr ;; \
      el8) gcc_toolchain=/usr ;; \
    esac; \
    case "$(uname -m)" in \
      x86_64) llvm_targets=X86; seed_triple=x86_64-unknown-linux-gnu ;; \
      aarch64) llvm_targets=AArch64; seed_triple=aarch64-unknown-linux-gnu ;; \
      *) echo "unsupported seed clang arch: $(uname -m)" >&2; exit 2 ;; \
    esac; \
    archive="llvm-project-${LLVM_VERSION}.src.tar.xz"; \
    curl --retry 5 --connect-timeout 30 -fsSL -o "${archive}" \
      "https://github.com/llvm/llvm-project/releases/download/llvmorg-${LLVM_VERSION}/${archive}"; \
    echo "${LLVM_SHA256}  ${archive}" | sha256sum -c -; \
    tar -xf "${archive}"; \
    src="llvm-project-${LLVM_VERSION}.src"; \
    CC=gcc CXX=g++ cmake -G Ninja -S "${src}/llvm" -B build \
      -DCMAKE_BUILD_TYPE=Release \
      -DCMAKE_INSTALL_PREFIX="${SEED_PREFIX}" \
      -DCMAKE_PREFIX_PATH="${CLANGUP_BUILDER_PREFIX}" \
      -DLLVM_ENABLE_PROJECTS="clang;lld" \
      -DLLVM_ENABLE_RUNTIMES="compiler-rt;libcxx;libcxxabi" \
      -DLLVM_TARGETS_TO_BUILD="${llvm_targets}" \
      -DLLVM_DEFAULT_TARGET_TRIPLE="${seed_triple}" \
      -DLLVM_ENABLE_PER_TARGET_RUNTIME_DIR=ON \
      -DLLVM_INSTALL_TOOLCHAIN_ONLY=ON \
      -DLLVM_DISTRIBUTION_COMPONENTS="clang;clang-resource-headers;lld;llvm-ar;llvm-nm;llvm-ranlib" \
      -DCLANG_CONFIG_FILE_SYSTEM_DIR="${SEED_PREFIX}/etc/clang" \
      -DLLVM_ENABLE_ASSERTIONS=OFF \
      -DLLVM_ENABLE_LIBEDIT=OFF \
      -DLLVM_ENABLE_LIBXML2=OFF \
      -DLLVM_ENABLE_ZLIB=OFF \
      -DLLVM_ENABLE_ZSTD=OFF \
      -DLLVM_INCLUDE_BENCHMARKS=OFF \
      -DLLVM_INCLUDE_DOCS=OFF \
      -DLLVM_INCLUDE_EXAMPLES=OFF \
      -DLLVM_INCLUDE_TESTS=OFF \
      -DLLVM_PARALLEL_LINK_JOBS="${LLVM_PARALLEL_LINK_JOBS}" \
      -DCLANG_ENABLE_OBJC_REWRITER=OFF \
      -DCLANG_ENABLE_STATIC_ANALYZER=OFF \
      -DRUNTIMES_CMAKE_ARGS="-DCOMPILER_RT_BUILD_CRT=OFF;-DCOMPILER_RT_BUILD_SANITIZERS=OFF;-DCOMPILER_RT_BUILD_LIBFUZZER=OFF;-DCOMPILER_RT_BUILD_PROFILE=OFF;-DCOMPILER_RT_BUILD_MEMPROF=OFF;-DCOMPILER_RT_BUILD_ORC=OFF;-DCOMPILER_RT_INCLUDE_TESTS=OFF;-DLIBCXX_ENABLE_SHARED=OFF;-DLIBCXX_ENABLE_STATIC=ON;-DLIBCXX_ENABLE_STATIC_ABI_LIBRARY=ON;-DLIBCXX_INCLUDE_BENCHMARKS=OFF;-DLIBCXX_INCLUDE_TESTS=OFF;-DLIBCXXABI_ENABLE_SHARED=OFF;-DLIBCXXABI_ENABLE_STATIC=ON;-DLIBCXXABI_INCLUDE_TESTS=OFF;-DLIBCXXABI_USE_LLVM_UNWINDER=OFF"; \
    ninja -C build -j "${NINJA_JOBS}" distribution runtimes; \
    ninja -C build install-distribution install-builtins install-runtimes; \
    mkdir -p "${SEED_PREFIX}/etc/clang"; \
    printf '%s\n' "--gcc-toolchain=${gcc_toolchain}" > "${SEED_PREFIX}/etc/clang/clang.cfg"; \
    printf '%s\n' "--gcc-toolchain=${gcc_toolchain}" > "${SEED_PREFIX}/etc/clang/clang++.cfg"; \
    rm -rf /tmp/clangup-seed-src

# -----------------------------------------------------------------------------
# Seed image
# -----------------------------------------------------------------------------

FROM seed-base AS final

ARG SEED_PREFIX
ARG BASE_PROFILE=el7

ENV CLANGUP_BUILDER_PREFIX=/opt/clangup-builder
ENV CLANGUP_SEED_PREFIX=${SEED_PREFIX}
ENV PATH=${SEED_PREFIX}/bin:/opt/clangup-builder/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
ENV LD_LIBRARY_PATH=${SEED_PREFIX}/lib64:${SEED_PREFIX}/lib:/opt/clangup-builder/lib64:/opt/clangup-builder/lib:/opt/rh/devtoolset-11/root/usr/lib64
ENV CC=${SEED_PREFIX}/bin/clang
ENV CXX=${SEED_PREFIX}/bin/clang++
ENV AR=${SEED_PREFIX}/bin/llvm-ar
ENV NM=${SEED_PREFIX}/bin/llvm-nm
ENV RANLIB=${SEED_PREFIX}/bin/llvm-ranlib

SHELL ["/bin/bash", "-o", "pipefail", "-c"]

COPY --from=seed-builder ${SEED_PREFIX}/ ${SEED_PREFIX}/

RUN set -eux; \
    seed_triple="$(clang -dumpmachine)"; \
    test "${seed_triple}" = "$(uname -m)-unknown-linux-gnu"; \
    case "${BASE_PROFILE}" in \
      el7) gcc_toolchain=/opt/rh/devtoolset-11/root/usr ;; \
      el8) gcc_toolchain=/usr ;; \
      *) echo "unsupported BASE_PROFILE=${BASE_PROFILE}" >&2; exit 2 ;; \
    esac; \
    clang++ -### -x c++ /dev/null 2> /tmp/seed-default-driver.txt; \
    grep -Fq "${gcc_toolchain}/" /tmp/seed-default-driver.txt; \
    printf '%s\n' \
      '#include <stdexcept>' '#include <string>' '#include <thread>' '#include <vector>' \
      'int main() {' \
      '  std::vector<int> values(1);' \
      '  std::thread worker([&] { values[0] = 42; });' \
      '  worker.join();' \
      '  try { throw std::runtime_error("seed"); }' \
      '  catch (const std::exception& error) {' \
      '    return values[0] != 42 || std::string(error.what()) != "seed";' \
      '  }' \
      '}' > /tmp/seed-cxx-smoke.cc; \
    clang++ -std=c++11 -pthread -fuse-ld=lld \
      /tmp/seed-cxx-smoke.cc -o /tmp/seed-libstdcxx-smoke; \
    ldd /tmp/seed-libstdcxx-smoke | tee /tmp/seed-libstdcxx-smoke.ldd; \
    grep -q 'libstdc[+][+][.]so' /tmp/seed-libstdcxx-smoke.ldd; \
    /tmp/seed-libstdcxx-smoke; \
    printf '%s\n' \
      '#include <algorithm>' '#include <array>' '#include <atomic>' \
      '#include <concepts>' '#include <format>' '#include <ranges>' \
      '#include <span>' '#include <string>' '#include <thread>' '#include <vector>' \
      'template <std::integral T>' \
      'T sum(std::span<const T> values) {' \
      '  T result{};' \
      '  for (T value : values | std::views::filter([](T value) { return value > 1; }))' \
      '    result += value;' \
      '  return result;' \
      '}' \
      'int main() {' \
      '  std::vector<int> values{3, 1, 2};' \
      '  std::ranges::sort(values);' \
      '  std::atomic<int> thread_value{0};' \
      '  std::jthread worker([&](std::stop_token token) {' \
      '    if (!token.stop_requested()) thread_value.store(42);' \
      '  });' \
      '  worker.join();' \
      '  std::span<const int> view(values);' \
      '  std::string text = std::format("{}:{}", "seed", sum(view));' \
      '  return text != "seed:5" || thread_value.load() != 42;' \
      '}' > /tmp/seed-libcxx-cxx20-smoke.cc; \
    clang++ -std=c++20 -pthread -stdlib=libc++ -fuse-ld=lld \
      /tmp/seed-libcxx-cxx20-smoke.cc -o /tmp/seed-libcxx-cxx20-smoke; \
    if ldd /tmp/seed-libcxx-cxx20-smoke | grep -E 'libstdc[+][+]|libc[+][+][.]so|libc[+][+]abi[.]so'; then \
      echo "seed libc++ C++20 smoke test has a shared C++ runtime dependency" >&2; \
      exit 1; \
    fi; \
    /tmp/seed-libcxx-cxx20-smoke; \
    printf '%s\n' \
      'volatile unsigned __int128 seed_input = ((unsigned __int128)1 << 100) + 17;' \
      'volatile unsigned __int128 seed_divisor = 3;' \
      'int main(void) {' \
      '  unsigned __int128 quotient = seed_input / seed_divisor;' \
      '  unsigned __int128 remainder = seed_input % seed_divisor;' \
      '  return quotient * seed_divisor + remainder != seed_input;' \
      '}' > /tmp/seed-builtins-smoke.c; \
    clang --rtlib=compiler-rt --unwindlib=none -fuse-ld=lld \
      /tmp/seed-builtins-smoke.c -o /tmp/seed-builtins-smoke; \
    /tmp/seed-builtins-smoke; \
    printf '%s\n' 'int main(void) { return 0; }' > /tmp/seed-lto-smoke.c; \
    clang -flto -fuse-ld=lld /tmp/seed-lto-smoke.c -o /tmp/seed-lto-smoke; \
    /tmp/seed-lto-smoke; \
    printf '%s\n' 'int seed_archive_value(void) { return 42; }' \
      > /tmp/seed-archive-member.c; \
    printf '%s\n' \
      'int seed_archive_value(void);' \
      'int main(void) { return seed_archive_value() != 42; }' \
      > /tmp/seed-archive-main.c; \
    clang -c /tmp/seed-archive-member.c -o /tmp/seed-archive-member.o; \
    llvm-ar rc /tmp/libseed-smoke.a /tmp/seed-archive-member.o; \
    llvm-ranlib /tmp/libseed-smoke.a; \
    llvm-nm /tmp/libseed-smoke.a | grep -q ' T seed_archive_value'; \
    clang -fuse-ld=lld /tmp/seed-archive-main.c /tmp/libseed-smoke.a \
      -o /tmp/seed-archive-smoke; \
    /tmp/seed-archive-smoke; \
    rm -f \
      /tmp/seed-default-driver.txt \
      /tmp/seed-cxx-smoke.cc \
      /tmp/seed-libstdcxx-smoke /tmp/seed-libstdcxx-smoke.ldd \
      /tmp/seed-libcxx-cxx20-smoke.cc /tmp/seed-libcxx-cxx20-smoke \
      /tmp/seed-builtins-smoke.c /tmp/seed-builtins-smoke \
      /tmp/seed-lto-smoke.c /tmp/seed-lto-smoke \
      /tmp/seed-archive-member.c /tmp/seed-archive-member.o \
      /tmp/seed-archive-main.c /tmp/libseed-smoke.a /tmp/seed-archive-smoke

WORKDIR /work
CMD ["/bin/bash", "-l"]
