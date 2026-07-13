#!/usr/bin/env bash
set -euo pipefail

script_dir="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"

: "${CLANGUP_SOURCE:?}"
: "${CLANGUP_BUILD:?}"
: "${CLANGUP_PREFIX:?}"
: "${CLANGUP_TARGET_TRIPLE:?}"
: "${CLANGUP_ARCH:?}"
: "${CLANGUP_PROJECTS:?}"
: "${CLANGUP_RUNTIMES:?}"
: "${CLANGUP_JOBS:?}"
: "${CLANGUP_LINK_JOBS:?}"

export CLANGUP_BOOTSTRAP_PREFIX="${CLANGUP_BOOTSTRAP_PREFIX:-/opt/clangup-bootstrap}"
export CLANGUP_BUILDER_PREFIX="${CLANGUP_BUILDER_PREFIX:-/opt/clangup-builder}"

case "${CLANGUP_ARCH}" in
  x86_64) expected_machine=x86_64; export CLANGUP_LLVM_TARGETS=X86 ;;
  aarch64) expected_machine=aarch64; export CLANGUP_LLVM_TARGETS=AArch64 ;;
  *) echo "unsupported Linux architecture: ${CLANGUP_ARCH}" >&2; exit 1 ;;
esac
if [[ "$(uname -s)" != Linux || "$(uname -m)" != "${expected_machine}" ]]; then
  echo "target ${CLANGUP_TARGET_TRIPLE} requires Linux/${expected_machine}" >&2
  exit 1
fi
export CLANGUP_BUILD_CONFIG_DIR="${script_dir}"

cmake_args=(
  cmake -G Ninja
  -S "${CLANGUP_SOURCE}/llvm"
  -B "${CLANGUP_BUILD}"
  -C "${script_dir}/common.cmake"
  -C "${script_dir}/linux.cmake"
)
printf '%s\n' "${cmake_args[@]}" > "$(dirname -- "${CLANGUP_BUILD}")/cmake-arguments.txt"
"${cmake_args[@]}"

ninja -C "${CLANGUP_BUILD}" -j "${CLANGUP_JOBS}"
ninja -C "${CLANGUP_BUILD}" install
ninja -C "${CLANGUP_BUILD}" install-builtins install-runtimes

mkdir -p "${CLANGUP_PREFIX}/etc/clang"
printf '%s\n' '-L<CFGDIR>/../../lib' >"${CLANGUP_PREFIX}/etc/clang/clang.cfg"
printf '%s\n' '-L<CFGDIR>/../../lib' >"${CLANGUP_PREFIX}/etc/clang/clang++.cfg"

export CLANGUP_RESOURCE_DIR="$("${CLANGUP_PREFIX}/bin/clang" --print-resource-dir)"

compiler_rt_build="$(dirname -- "${CLANGUP_BUILD}")/compiler-rt"
cmake \
  -G Ninja \
  -S "${CLANGUP_SOURCE}/compiler-rt" \
  -B "${compiler_rt_build}" \
  -C "${script_dir}/compiler-rt.cmake"
ninja -C "${compiler_rt_build}" -j "${CLANGUP_JOBS}"
ninja -C "${compiler_rt_build}" install
