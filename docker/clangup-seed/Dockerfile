ARG BASE_IMAGE=clangup-builder:el7-glibc2.17-v1

# -----------------------------------------------------------------------------
# Fixed seed release
# -----------------------------------------------------------------------------

ARG LLVM_VERSION=22.1.8
ARG LLVM_SHA256=922f1817a0df7b1489272d18134ee0087a8b068828f87ac63b9861b1a9965888
ARG SEED_PREFIX=/opt/clangup-seed
ARG GCC_PREFIX=/opt/clangup-seed/gcc

# -----------------------------------------------------------------------------
# GCC-hosted seed build
# -----------------------------------------------------------------------------

FROM ${BASE_IMAGE} AS seed-builder

ARG BASE_PROFILE=el7
ARG LLVM_VERSION
ARG LLVM_SHA256
ARG SEED_PREFIX
ARG GCC_PREFIX
ARG NINJA_JOBS=4
ARG LLVM_PARALLEL_LINK_JOBS=1

ENV CLANGUP_BUILDER_PREFIX=/opt/clangup-builder

SHELL ["/bin/bash", "-o", "pipefail", "-c"]

RUN set -eux; \
    package_manager="$(command -v dnf || command -v yum)"; \
    package_manager_opts=(--setopt=retries=10 --setopt=timeout=120); \
    case "${BASE_PROFILE}" in \
      el7) \
        "${package_manager}" "${package_manager_opts[@]}" install -y \
          devtoolset-11-binutils devtoolset-11-gcc devtoolset-11-gcc-c++; \
        source /opt/rh/devtoolset-11/enable; \
        gcc_root=/opt/rh/devtoolset-11/root/usr; \
        ;; \
      el8) \
        "${package_manager}" "${package_manager_opts[@]}" install -y \
          gcc gcc-c++ libstdc++-static; \
        gcc_root=/usr; \
        ;; \
      *) \
        echo "unsupported BASE_PROFILE=${BASE_PROFILE}" >&2; \
        exit 2; \
        ;; \
    esac; \
    "${package_manager}" clean all; \
    rm -rf /var/cache/yum /var/cache/dnf; \
    gcc_triple="$(gcc -dumpmachine)"; \
    gcc_install_dir="$(dirname "$(gcc -print-libgcc-file-name)")"; \
    gcc_version="$(basename "${gcc_install_dir}")"; \
    libstdcpp="$(gcc -print-file-name=libstdc++.so.6)"; \
    libgcc_s="$(gcc -print-file-name=libgcc_s.so.1)"; \
    test -d "${gcc_install_dir}"; \
    test -f "${libstdcpp}"; \
    test -f "${libgcc_s}"; \
    mkdir -p \
      "${GCC_PREFIX}/include" \
      "${GCC_PREFIX}/lib/gcc/${gcc_triple}" \
      "${GCC_PREFIX}/lib64"; \
    cp -a "${gcc_root}/include/c++" "${GCC_PREFIX}/include/"; \
    cp -a "${gcc_install_dir}" "${GCC_PREFIX}/lib/gcc/${gcc_triple}/"; \
    cp -L "${libstdcpp}" "${GCC_PREFIX}/lib64/libstdc++.so.6"; \
    cp -L "${libgcc_s}" "${GCC_PREFIX}/lib64/libgcc_s.so.1"; \
    printf '%s\n' "${gcc_triple}" > "${GCC_PREFIX}/triple"; \
    printf '%s\n' "${gcc_version}" > "${GCC_PREFIX}/version"

WORKDIR /tmp/clangup-seed-src

RUN set -eux; \
    source "${CLANGUP_BUILDER_PREFIX}/env"; \
    case "${BASE_PROFILE}" in \
      el7) source /opt/rh/devtoolset-11/enable ;; \
      el8) ;; \
    esac; \
    case "$(uname -m)" in \
      x86_64) llvm_targets=X86 ;; \
      aarch64) llvm_targets=AArch64 ;; \
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
      -DCMAKE_EXE_LINKER_FLAGS="-static-libstdc++ -static-libgcc" \
      -DCMAKE_SHARED_LINKER_FLAGS="-static-libstdc++ -static-libgcc" \
      -DLLVM_ENABLE_PROJECTS="clang;lld" \
      -DLLVM_TARGETS_TO_BUILD="${llvm_targets}" \
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
      -DCLANG_ENABLE_STATIC_ANALYZER=OFF; \
    ninja -C build -j "${NINJA_JOBS}" distribution; \
    ninja -C build install-distribution; \
    CC="${SEED_PREFIX}/bin/clang" CXX="${SEED_PREFIX}/bin/clang++" \
      cmake -G Ninja -S "${src}/runtimes" -B runtimes-build \
        -DCMAKE_BUILD_TYPE=Release \
        -DCMAKE_INSTALL_PREFIX="${SEED_PREFIX}" \
        -DLLVM_ENABLE_RUNTIMES="libcxx;libcxxabi" \
        -DLIBCXX_ENABLE_SHARED=OFF \
        -DLIBCXX_ENABLE_STATIC=ON \
        -DLIBCXX_ENABLE_STATIC_ABI_LIBRARY=ON \
        -DLIBCXX_INCLUDE_BENCHMARKS=OFF \
        -DLIBCXX_INCLUDE_TESTS=OFF \
        -DLIBCXXABI_ENABLE_SHARED=OFF \
        -DLIBCXXABI_ENABLE_STATIC=ON \
        -DLIBCXXABI_INCLUDE_TESTS=OFF \
        -DLIBCXXABI_USE_LLVM_UNWINDER=OFF; \
    ninja -C runtimes-build -j "${NINJA_JOBS}" cxx cxxabi; \
    ninja -C runtimes-build install-cxx install-cxxabi; \
    mkdir -p "${SEED_PREFIX}/etc/clang"; \
    printf '%s\n' "--gcc-toolchain=${GCC_PREFIX}" > "${SEED_PREFIX}/etc/clang/clang.cfg"; \
    printf '%s\n' "--gcc-toolchain=${GCC_PREFIX}" > "${SEED_PREFIX}/etc/clang/clang++.cfg"; \
    rm -rf /tmp/clangup-seed-src

# -----------------------------------------------------------------------------
# Compiler-free seed image
# -----------------------------------------------------------------------------

FROM ${BASE_IMAGE} AS final

ARG SEED_PREFIX
ARG GCC_PREFIX

ENV CLANGUP_BUILDER_PREFIX=/opt/clangup-builder
ENV CLANGUP_SEED_PREFIX=${SEED_PREFIX}
ENV PATH=${SEED_PREFIX}/bin:/opt/clangup-builder/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
ENV LD_LIBRARY_PATH=${GCC_PREFIX}/lib64:${SEED_PREFIX}/lib64:${SEED_PREFIX}/lib:/opt/clangup-builder/lib64:/opt/clangup-builder/lib
ENV CC=${SEED_PREFIX}/bin/clang
ENV CXX=${SEED_PREFIX}/bin/clang++
ENV AR=${SEED_PREFIX}/bin/llvm-ar
ENV NM=${SEED_PREFIX}/bin/llvm-nm
ENV RANLIB=${SEED_PREFIX}/bin/llvm-ranlib

SHELL ["/bin/bash", "-o", "pipefail", "-c"]

COPY --from=seed-builder ${SEED_PREFIX}/ ${SEED_PREFIX}/

RUN set -eux; \
    printf '%s\n' "-L${SEED_PREFIX}/lib" >> "${SEED_PREFIX}/etc/clang/clang.cfg"; \
    printf '%s\n' "-L${SEED_PREFIX}/lib" >> "${SEED_PREFIX}/etc/clang/clang++.cfg"; \
    for compiler in gcc g++ cc c++ cpp; do \
      if command -v "${compiler}"; then \
        echo "unexpected system compiler in seed image: ${compiler}" >&2; \
        exit 1; \
      fi; \
    done; \
    clang++ --version; \
    ld.lld --version; \
    llvm-ar --version | head -1; \
    test -x "${SEED_PREFIX}/bin/llvm-nm"; \
    test -x "${SEED_PREFIX}/bin/llvm-ranlib"; \
    test -f "${SEED_PREFIX}/include/c++/v1/__config"; \
    test -f "${SEED_PREFIX}/lib/libc++.a"; \
    test -f "${SEED_PREFIX}/lib/libc++abi.a"; \
    ldd "${SEED_PREFIX}/bin/clang" | tee /tmp/seed-clang.ldd; \
    if grep -E 'libstdc[+][+]|libgcc_s' /tmp/seed-clang.ldd; then \
      echo "seed clang has an external GCC runtime dependency" >&2; \
      exit 1; \
    fi; \
    printf '%s\n' '#include <string>' 'int main() { std::string value = "seed"; return value.size() != 4; }' \
      > /tmp/seed-smoke.cc; \
    clang++ -fuse-ld=lld /tmp/seed-smoke.cc -o /tmp/seed-smoke; \
    /tmp/seed-smoke; \
    printf '%s\n' '#include <stdexcept>' '#include <string>' \
      'int main() { try { throw std::runtime_error("libc++"); } catch (const std::exception& e) { return std::string(e.what()) != "libc++"; } }' \
      > /tmp/seed-libcxx-smoke.cc; \
    clang++ -stdlib=libc++ -fuse-ld=lld /tmp/seed-libcxx-smoke.cc -o /tmp/seed-libcxx-smoke; \
    if ldd /tmp/seed-libcxx-smoke | grep -E 'libc[+][+]|libc[+][+]abi'; then \
      echo "seed libc++ smoke test has a shared libc++ dependency" >&2; \
      exit 1; \
    fi; \
    /tmp/seed-libcxx-smoke; \
    rm -f \
      /tmp/seed-clang.ldd \
      /tmp/seed-smoke.cc /tmp/seed-smoke \
      /tmp/seed-libcxx-smoke.cc /tmp/seed-libcxx-smoke

WORKDIR /work
CMD ["/bin/bash", "-l"]
