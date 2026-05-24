ARG BASE_IMAGE=clangup-builder:el7-glibc2.17-v1

FROM ${BASE_IMAGE}

ARG BASE_PROFILE=el7
ARG NINJA_JOBS=4
ARG LLVM_PARALLEL_LINK_JOBS=1

ENV CLANGUP_BUILDER_PREFIX=/opt/clangup-builder
ENV CLANGUP_SEED_PREFIX=/opt/clangup-seed
ENV PATH=/opt/clangup-seed/bin:/opt/clangup-builder/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
ENV LD_LIBRARY_PATH=/opt/clangup-seed/lib64:/opt/clangup-seed/lib:/opt/clangup-builder/lib64:/opt/clangup-builder/lib:/opt/rh/devtoolset-11/root/usr/lib64
ENV CC=/opt/clangup-seed/bin/clang
ENV CXX=/opt/clangup-seed/bin/clang++
ENV AR=/opt/clangup-seed/bin/llvm-ar
ENV NM=/opt/clangup-seed/bin/llvm-nm
ENV RANLIB=/opt/clangup-seed/bin/llvm-ranlib

SHELL ["/bin/bash", "-o", "pipefail", "-c"]

WORKDIR /tmp/clangup-seed-src

RUN set -eux; \
    package_manager="$(command -v dnf || command -v yum)"; \
    package_manager_opts=(--setopt=retries=10 --setopt=timeout=120); \
    source "${CLANGUP_BUILDER_PREFIX}/env"; \
    case "${BASE_PROFILE}" in \
      el7) \
        "${package_manager}" "${package_manager_opts[@]}" install -y devtoolset-11-gcc devtoolset-11-gcc-c++ devtoolset-11-binutils; \
        source /opt/rh/devtoolset-11/enable; \
        clang_config_dir="${CLANGUP_SEED_PREFIX}/etc/clang"; \
        gcc_install_dir="$(dirname "$(gcc -print-libgcc-file-name)")"; \
        ;; \
      el8) \
        "${package_manager}" "${package_manager_opts[@]}" install -y libstdc++-static; \
        clang_config_dir=""; \
        gcc_install_dir=""; \
        ;; \
      *) \
        echo "unsupported BASE_PROFILE=${BASE_PROFILE}" >&2; \
        exit 2; \
        ;; \
    esac; \
    case "$(uname -m)" in \
      x86_64) llvm_targets=X86 ;; \
      aarch64) llvm_targets=AArch64 ;; \
      *) echo "unsupported seed clang arch: $(uname -m)" >&2; exit 2 ;; \
    esac; \
    curl -fsSL -o llvm-project-21.1.8.src.tar.xz https://github.com/llvm/llvm-project/releases/download/llvmorg-21.1.8/llvm-project-21.1.8.src.tar.xz; \
    echo '4633a23617fa31a3ea51242586ea7fb1da7140e426bd62fc164261fe036aa142  llvm-project-21.1.8.src.tar.xz' | sha256sum -c -; \
    tar -xf llvm-project-21.1.8.src.tar.xz; \
    cd llvm-project-21.1.8.src; \
    export CC=gcc; \
    export CXX=g++; \
    cmake -G Ninja -S llvm -B build-seed \
      -DCMAKE_BUILD_TYPE=Release \
      -DCMAKE_INSTALL_PREFIX="${CLANGUP_SEED_PREFIX}" \
      -DCMAKE_PREFIX_PATH="${CLANGUP_BUILDER_PREFIX}" \
      -DLLVM_ENABLE_PROJECTS="clang;lld" \
      -DLLVM_TARGETS_TO_BUILD="${llvm_targets}" \
      -DCLANG_CONFIG_FILE_SYSTEM_DIR="${CLANGUP_SEED_PREFIX}/etc/clang" \
      -DLLVM_ENABLE_ASSERTIONS=OFF \
      -DLLVM_ENABLE_LIBEDIT=OFF \
      -DLLVM_ENABLE_LIBXML2=OFF \
      -DLLVM_ENABLE_TERMINFO=OFF \
      -DLLVM_ENABLE_ZLIB=OFF \
      -DLLVM_ENABLE_ZSTD=OFF \
      -DLLVM_INCLUDE_BENCHMARKS=OFF \
      -DLLVM_INCLUDE_DOCS=OFF \
      -DLLVM_INCLUDE_EXAMPLES=OFF \
      -DLLVM_INCLUDE_TESTS=OFF \
      -DLLVM_PARALLEL_LINK_JOBS="${LLVM_PARALLEL_LINK_JOBS}" \
      -DCLANG_ENABLE_ARCMT=OFF \
      -DCLANG_ENABLE_STATIC_ANALYZER=OFF; \
    ninja -C build-seed -j "${NINJA_JOBS}"; \
    ninja -C build-seed install; \
    if [ -n "${clang_config_dir}" ]; then \
      mkdir -p "${clang_config_dir}"; \
      printf '%s\n' "--gcc-install-dir=${gcc_install_dir}" > "${clang_config_dir}/clang.cfg"; \
      printf '%s\n' "--gcc-install-dir=${gcc_install_dir}" > "${clang_config_dir}/clang++.cfg"; \
    fi; \
    cd /tmp; \
    rm -rf /tmp/clangup-seed-src; \
    hash -r; \
    clang++ --version; \
    ld.lld --version; \
    llvm-ar --version > /tmp/llvm-ar.version; \
    sed -n '1p' /tmp/llvm-ar.version; \
    printf '%s\n' 'int main() { return 0; }' > /tmp/seed-smoke.cc; \
    clang++ /tmp/seed-smoke.cc -o /tmp/seed-smoke; \
    /tmp/seed-smoke; \
    ldd "${CLANGUP_SEED_PREFIX}/bin/clang" | tee /tmp/seed-clang.ldd; \
    test -x "${CLANGUP_SEED_PREFIX}/bin/clang"; \
    test -x "${CLANGUP_SEED_PREFIX}/bin/clang++"; \
    test -x "${CLANGUP_SEED_PREFIX}/bin/ld.lld"; \
    test -x "${CLANGUP_SEED_PREFIX}/bin/llvm-ar"; \
    test -x "${CLANGUP_SEED_PREFIX}/bin/llvm-nm"; \
    test -x "${CLANGUP_SEED_PREFIX}/bin/llvm-ranlib"; \
    rm -f /tmp/seed-smoke /tmp/seed-smoke.cc /tmp/seed-clang.ldd /tmp/llvm-ar.version

WORKDIR /work
CMD ["/bin/bash", "-l"]
