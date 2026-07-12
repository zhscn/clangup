#!/usr/bin/env bash
set -euo pipefail

script_dir="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"

: "${CLANGUP_SOURCE:?}"
: "${CLANGUP_BUILD:?}"
: "${CLANGUP_PREFIX:?}"
: "${CLANGUP_TARGET_TRIPLE:?}"
: "${CLANGUP_PROJECTS:?}"
: "${CLANGUP_RUNTIMES:?}"
: "${CLANGUP_JOBS:?}"
: "${CLANGUP_MIN_MACOS_VERSION:?}"

if [[ "$(uname -s)" != Darwin || "$(uname -m)" != arm64 ]]; then
  echo "arm64-apple-darwin requires Apple Silicon macOS" >&2
  exit 1
fi

export CLANGUP_MACOS_SDK="$(xcrun --sdk macosx --show-sdk-path)"
export CLANGUP_APPLE_CLANG="$(xcrun -f clang)"
export CLANGUP_APPLE_CLANGXX="$(xcrun -f clang++)"
export CLANGUP_LLVM_TARGETS=AArch64

cmake_args=(
  cmake -G Ninja
  -S "${CLANGUP_SOURCE}/llvm"
  -B "${CLANGUP_BUILD}"
  -C "${script_dir}/common.cmake"
  -C "${script_dir}/macos.cmake"
)
printf '%s\n' "${cmake_args[@]}" > "$(dirname -- "${CLANGUP_BUILD}")/cmake-arguments.txt"
"${cmake_args[@]}"

ninja -C "${CLANGUP_BUILD}" -j "${CLANGUP_JOBS}"
ninja -C "${CLANGUP_BUILD}" install
ninja -C "${CLANGUP_BUILD}" install-builtins install-runtimes
