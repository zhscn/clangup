#!/usr/bin/env bash
set -euo pipefail

LLVM_VERSION="${LLVM_VERSION:-21.1.8}"
LLVM_SHA256="${LLVM_SHA256:-4633a23617fa31a3ea51242586ea7fb1da7140e426bd62fc164261fe036aa142}"
BASE_PROFILE="${BASE_PROFILE:-el7}"
SEED_PREFIX="${STAGE0_PREFIX:-/opt/clangup-seed}"
FINAL_PREFIX="${FINAL_PREFIX:-/opt/clangup-final}"
BUILDER_PREFIX="${CLANGUP_BUILDER_PREFIX:-/opt/clangup-builder}"
WORK_DIR="${WORK_DIR:-/tmp/clangup-final-src}"
OUT_DIR="${OUT_DIR:-/out}"
NINJA_JOBS="${NINJA_JOBS:-4}"
LLVM_PARALLEL_LINK_JOBS="${LLVM_PARALLEL_LINK_JOBS:-1}"
ZSTD_CLEVEL="${ZSTD_CLEVEL:-19}"

case "$(uname -m)" in
  x86_64)
    LLVM_TARGETS="${LLVM_TARGETS:-X86}"
    TARGET_TRIPLE="${TARGET_TRIPLE:-x86_64-unknown-linux-gnu}"
    ARTIFACT_ARCH="${ARTIFACT_ARCH:-x86_64}"
    ;;
  aarch64)
    LLVM_TARGETS="${LLVM_TARGETS:-AArch64}"
    TARGET_TRIPLE="${TARGET_TRIPLE:-aarch64-unknown-linux-gnu}"
    ARTIFACT_ARCH="${ARTIFACT_ARCH:-aarch64}"
    ;;
  *)
    echo "unsupported final clang arch: $(uname -m)" >&2
    exit 2
    ;;
esac

ARTIFACT_NAME="${ARTIFACT_NAME:-clang-${LLVM_VERSION}-${ARTIFACT_ARCH}.tar.zst}"
ARTIFACT_STEM="${ARTIFACT_NAME%.tar.zst}"

if [[ -f "${BUILDER_PREFIX}/env" ]]; then
  source "${BUILDER_PREFIX}/env"
fi

export PATH="${SEED_PREFIX}/bin:${BUILDER_PREFIX}/bin:${PATH}"
export LD_LIBRARY_PATH="${SEED_PREFIX}/lib64:${SEED_PREFIX}/lib:${BUILDER_PREFIX}/lib64:${BUILDER_PREFIX}/lib${LD_LIBRARY_PATH:+:${LD_LIBRARY_PATH}}"
export CMAKE_PREFIX_PATH="${BUILDER_PREFIX};${SEED_PREFIX}${CMAKE_PREFIX_PATH:+;${CMAKE_PREFIX_PATH}}"
export CC="${SEED_PREFIX}/bin/clang"
export CXX="${SEED_PREFIX}/bin/clang++"
export AR="${SEED_PREFIX}/bin/llvm-ar"
export NM="${SEED_PREFIX}/bin/llvm-nm"
export RANLIB="${SEED_PREFIX}/bin/llvm-ranlib"
export CFLAGS="${CFLAGS:-} -O2 -fPIC"
export CXXFLAGS="${CXXFLAGS:-} -O2 -fPIC"
export LDFLAGS="${LDFLAGS:-} -Wl,--build-id"

rm -rf "${WORK_DIR}" "${FINAL_PREFIX}" "${OUT_DIR}"
mkdir -p "${WORK_DIR}" "${FINAL_PREFIX}" "${OUT_DIR}"
cd "${WORK_DIR}"

archive="llvm-project-${LLVM_VERSION}.src.tar.xz"
curl -fsSL -o "${archive}" \
  "https://github.com/llvm/llvm-project/releases/download/llvmorg-${LLVM_VERSION}/${archive}"

if [[ -n "${LLVM_SHA256}" ]]; then
  echo "${LLVM_SHA256}  ${archive}" | sha256sum -c -
fi

tar -xf "${archive}"
src_dir="${WORK_DIR}/llvm-project-${LLVM_VERSION}.src"
main_build="${src_dir}/build-final"
rt_build="${src_dir}/build-rt"

cmake -G Ninja -S "${src_dir}/llvm" -B "${main_build}" \
  -DCMAKE_BUILD_TYPE=Release \
  -DCMAKE_INSTALL_PREFIX="${FINAL_PREFIX}" \
  -DCMAKE_PREFIX_PATH="${CMAKE_PREFIX_PATH}" \
  -DCMAKE_C_COMPILER="${CC}" \
  -DCMAKE_CXX_COMPILER="${CXX}" \
  -DCMAKE_AR="${AR}" \
  -DCMAKE_NM="${NM}" \
  -DCMAKE_RANLIB="${RANLIB}" \
  -DLLVM_ENABLE_PROJECTS="clang;lld;clang-tools-extra" \
  -DLLVM_ENABLE_RUNTIMES="compiler-rt;libcxx;libcxxabi" \
  -DLLVM_TARGETS_TO_BUILD="${LLVM_TARGETS}" \
  -DLLVM_DEFAULT_TARGET_TRIPLE="${TARGET_TRIPLE}" \
  -DLLVM_ENABLE_LIBEDIT=OFF \
  -DLLVM_ENABLE_LIBXML2=OFF \
  -DLLVM_ENABLE_TERMINFO=OFF \
  -DLLVM_ENABLE_ZLIB=ON \
  -DLLVM_ENABLE_ZSTD=ON \
  -DLLVM_ENABLE_ASSERTIONS=OFF \
  -DLLVM_INCLUDE_BENCHMARKS=OFF \
  -DLLVM_INCLUDE_DOCS=OFF \
  -DLLVM_INCLUDE_EXAMPLES=OFF \
  -DLLVM_INCLUDE_TESTS=OFF \
  -DLLVM_ENABLE_LLD=ON \
  -DLLVM_STATIC_LINK_CXX_STDLIB=ON \
  -DLLVM_PARALLEL_LINK_JOBS="${LLVM_PARALLEL_LINK_JOBS}" \
  -DCLANG_DEFAULT_CXX_STDLIB=libc++ \
  -DCLANG_DEFAULT_RTLIB=compiler-rt \
  -DCLANG_DEFAULT_UNWINDLIB=libgcc \
  -DCLANG_DEFAULT_LINKER=lld \
  -DCOMPILER_RT_BUILD_CRT=ON \
  -DCOMPILER_RT_BUILD_SANITIZERS=OFF \
  -DCOMPILER_RT_INCLUDE_TESTS=OFF \
  -DLIBCXX_ENABLE_SHARED=OFF \
  -DLIBCXX_ENABLE_STATIC=ON \
  -DLIBCXX_ENABLE_STATIC_ABI_LIBRARY=ON \
  -DLIBCXX_INCLUDE_TESTS=OFF \
  -DLIBCXXABI_ENABLE_SHARED=OFF \
  -DLIBCXXABI_ENABLE_STATIC=ON \
  -DLIBCXXABI_INCLUDE_TESTS=OFF \
  -DLIBCXXABI_USE_LLVM_UNWINDER=OFF

ninja -C "${main_build}" -j "${NINJA_JOBS}"
ninja -C "${main_build}" install

cmake -G Ninja -S "${src_dir}/compiler-rt" -B "${rt_build}" \
  -DCMAKE_BUILD_TYPE=Release \
  -DCMAKE_INSTALL_PREFIX="${FINAL_PREFIX}" \
  -DCMAKE_C_COMPILER="${main_build}/bin/clang" \
  -DCMAKE_CXX_COMPILER="${main_build}/bin/clang++" \
  -DCMAKE_C_COMPILER_TARGET="${TARGET_TRIPLE}" \
  -DCMAKE_CXX_COMPILER_TARGET="${TARGET_TRIPLE}" \
  -DCOMPILER_RT_BUILD_BUILTINS=OFF \
  -DCOMPILER_RT_BUILD_CRT=OFF \
  -DCOMPILER_RT_BUILD_SANITIZERS=ON \
  -DCOMPILER_RT_DEFAULT_TARGET_ONLY=ON \
  -DCOMPILER_RT_INCLUDE_TESTS=OFF \
  -DCOMPILER_RT_INSTALL_PATH="${FINAL_PREFIX}/lib/clang/${LLVM_VERSION%%.*}" \
  -DLLVM_ENABLE_PER_TARGET_RUNTIME_DIR=ON \
  -DLLVM_CONFIG_PATH="${main_build}/bin/llvm-config"

ninja -C "${rt_build}" -j "${NINJA_JOBS}"
ninja -C "${rt_build}" install

echo '=== stripping debug info ==='
strip_tool="${main_build}/bin/llvm-strip"
if [[ -x "${strip_tool}" ]]; then
  while IFS= read -r -d '' file; do
    "${strip_tool}" --strip-debug "${file}" 2>/dev/null || true
  done < <(find "${FINAL_PREFIX}" -type f -executable -print0)

  while IFS= read -r -d '' file; do
    "${strip_tool}" --strip-debug "${file}" 2>/dev/null || true
  done < <(find "${FINAL_PREFIX}" -type f \( -name '*.a' -o -name '*.o' -o -name '*.so' -o -name '*.so.*' \) -print0)
fi

echo '=== fixing libc++ __config_site symlink ==='
config_site="${FINAL_PREFIX}/include/${TARGET_TRIPLE}/c++/v1/__config_site"
default_config_site="${FINAL_PREFIX}/include/c++/v1/__config_site"
if [[ -e "${config_site}" && ! -e "${default_config_site}" ]]; then
  mkdir -p "$(dirname "${default_config_site}")"
  ln -s "../../${TARGET_TRIPLE}/c++/v1/__config_site" "${default_config_site}"
fi

echo '=== rewriting python shebangs ==='
while IFS= read -r -d '' file; do
  first_line=""
  IFS= read -r first_line < "${file}" || true
  case "${first_line}" in
    '#!/usr/bin/env python' | '#!/usr/libexec/platform-python')
      sed -i \
        -e '1s|^#!/usr/bin/env python$|#!/usr/bin/env python3|' \
        -e '1s|^#!/usr/libexec/platform-python$|#!/usr/bin/env python3|' \
        "${file}"
      ;;
  esac
done < <(find "${FINAL_PREFIX}" -type f \( -perm -111 -o -name '*.py' \) -print0)

echo '=== writing enable script ==='
cat > "${FINAL_PREFIX}/enable" <<'EOF'
_clangup_prefix="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
export PATH="${_clangup_prefix}/bin${PATH:+:${PATH}}"
export CC="${_clangup_prefix}/bin/clang"
export CXX="${_clangup_prefix}/bin/clang++"
export AR="${_clangup_prefix}/bin/llvm-ar"
export NM="${_clangup_prefix}/bin/llvm-nm"
export RANLIB="${_clangup_prefix}/bin/llvm-ranlib"
unset _clangup_prefix
EOF
chmod 0755 "${FINAL_PREFIX}/enable"

smoke_dir="${WORK_DIR}/smoke"
mkdir -p "${smoke_dir}"
cat > "${smoke_dir}/smoke.cc" <<'EOF'
#include <stdexcept>
#include <string>
#include <format>

int main() {
  try {
    throw std::runtime_error(std::format("{}", 42));
  } catch (const std::runtime_error&) {
    return 0;
  }
}
EOF

echo '=== smoke: driver dump ==='
"${FINAL_PREFIX}/bin/clang++" -### -std=c++20 "${smoke_dir}/smoke.cc" -o "${smoke_dir}/smoke" \
  2> "${smoke_dir}/final-driver.txt"
if ! grep -q -- '-lc++' "${smoke_dir}/final-driver.txt" \
   || ! grep -q 'clang_rt' "${smoke_dir}/final-driver.txt"; then
  echo '=== smoke driver dump (missing -lc++ or clang_rt) ===' >&2
  cat "${smoke_dir}/final-driver.txt" >&2
  exit 1
fi

echo '=== smoke: compile + run ==='
"${FINAL_PREFIX}/bin/clang++" -std=c++20 "${smoke_dir}/smoke.cc" -o "${smoke_dir}/smoke"
"${smoke_dir}/smoke"

echo '=== ldd checks ==='
ldd "${FINAL_PREFIX}/bin/clang" | tee "${smoke_dir}/final-clang.ldd.txt"
ldd "${smoke_dir}/smoke" | tee "${smoke_dir}/final-smoke.ldd.txt"

forbidden_dynamic='lib(stdc[+][+]|c[+][+]|c[+][+]abi|z|zstd|xml2|tinfo|ncurses|edit)[.]so'
if grep -E "${forbidden_dynamic}" "${smoke_dir}/final-clang.ldd.txt" "${smoke_dir}/final-smoke.ldd.txt"; then
  echo "final artifact has forbidden dynamic dependency" >&2
  exit 1
fi

tar \
  --sort=name \
  --owner=0 \
  --group=0 \
  --numeric-owner \
  --mtime="@${SOURCE_DATE_EPOCH:-0}" \
  --use-compress-program="zstd -T0 -${ZSTD_CLEVEL}" \
  -C "${FINAL_PREFIX}" \
  -cf "${OUT_DIR}/${ARTIFACT_NAME}" \
  .

(cd "${OUT_DIR}" && sha256sum "${ARTIFACT_NAME}" > "${ARTIFACT_NAME}.sha256")
artifact_sha256=$(awk '{print $1}' "${OUT_DIR}/${ARTIFACT_NAME}.sha256")
artifact_size=$(stat -c '%s' "${OUT_DIR}/${ARTIFACT_NAME}")
build_date=$(date -u -d "@${SOURCE_DATE_EPOCH:-$(date -u +%s)}" -Iseconds)

case "${BASE_PROFILE}" in
  el7) glibc_baseline=2.17 ;;
  el8) glibc_baseline=2.28 ;;
  *)   glibc_baseline=unknown ;;
esac

seed_clang_version=$("${SEED_PREFIX}/bin/clang" --version | head -n1)
final_clang_version=$("${FINAL_PREFIX}/bin/clang" --version | head -n1)

cat > "${OUT_DIR}/${ARTIFACT_STEM}.manifest.json" <<EOF
{
  "artifact": {
    "name": "${ARTIFACT_NAME}",
    "size_bytes": ${artifact_size},
    "sha256": "${artifact_sha256}"
  },
  "llvm": {
    "version": "${LLVM_VERSION}",
    "source_sha256": "${LLVM_SHA256}"
  },
  "target": {
    "arch": "${ARTIFACT_ARCH}",
    "triple": "${TARGET_TRIPLE}",
    "base_profile": "${BASE_PROFILE}",
    "glibc_baseline": "${glibc_baseline}"
  },
  "build": {
    "date": "${build_date}",
    "seed_clang": "${seed_clang_version}",
    "final_clang": "${final_clang_version}"
  },
  "defaults": {
    "cxx_stdlib": "libc++",
    "rtlib": "compiler-rt",
    "unwindlib": "libgcc",
    "linker": "lld"
  }
}
EOF

ls -lh "${OUT_DIR}"
