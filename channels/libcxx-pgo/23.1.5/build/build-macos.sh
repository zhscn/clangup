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
: "${CLANGUP_OPTIMIZATION_PGO:?}"
: "${CLANGUP_OPTIMIZATION_BOLT:?}"

if [[ "$(uname -s)" != Darwin || "$(uname -m)" != arm64 ]]; then
  echo "arm64-apple-darwin requires Apple Silicon macOS" >&2
  exit 1
fi
if [[ "${CLANGUP_OPTIMIZATION_PGO}" != 1 || "${CLANGUP_OPTIMIZATION_BOLT}" != 0 ]]; then
  echo "the macOS libcxx-pgo target requires PGO without BOLT" >&2
  exit 1
fi

export CLANGUP_MACOS_SDK="$(xcrun --sdk macosx --show-sdk-path)"
export CLANGUP_APPLE_CLANG="$(xcrun -f clang)"
export CLANGUP_APPLE_CLANGXX="$(xcrun -f clang++)"
export CLANGUP_APPLE_AR="$(xcrun -f ar)"
export CLANGUP_APPLE_LD="$(xcrun -f ld)"
export CLANGUP_APPLE_NM="$(xcrun -f nm)"
export CLANGUP_APPLE_RANLIB="$(xcrun -f ranlib)"
export CLANGUP_LLVM_PROFDATA="$(xcrun -f llvm-profdata)"
export CLANGUP_LLVM_TARGETS=AArch64

start_at="${CLANGUP_START_AT:-instrumented}"
stop_after="${CLANGUP_STOP_AFTER:-bolt}"
work_root="$(dirname -- "${CLANGUP_BUILD}")"
instrumented_build="${work_root}/instrumented-build"
export CLANGUP_INSTRUMENTED_PREFIX="${work_root}/instrumented-prefix"
export CLANGUP_PGO_DIR="${work_root}/pgo"
export CLANGUP_PROFDATA="${CLANGUP_PGO_DIR}/clang.profdata"
stamp_dir="${work_root}/stamps"
mkdir -p "${CLANGUP_PGO_DIR}" "${stamp_dir}"

read -r -a train_types <<< "${CLANGUP_TRAIN_TYPES:-Debug Release}"
if [[ "${#train_types[@]}" -eq 0 ]]; then
  echo "at least one training build type is required" >&2
  exit 1
fi
for train_type in "${train_types[@]}"; do
  case "${train_type}" in
    Debug|Release) ;;
    *) echo "unsupported training build type: ${train_type}" >&2; exit 1 ;;
  esac
done

record_command() {
  local path="$1"
  shift
  printf '%s\n' "$@" > "${path}"
}

stage_instrumented() {
  rm -rf "${instrumented_build}" "${CLANGUP_INSTRUMENTED_PREFIX}"
  find "${CLANGUP_PGO_DIR}" -name '*.profraw' -delete
  mkdir -p "${CLANGUP_INSTRUMENTED_PREFIX}"
  local cmake_args=(
    cmake -G Ninja
    -S "${CLANGUP_SOURCE}/llvm"
    -B "${instrumented_build}"
    -C "${script_dir}/common.cmake"
    -C "${script_dir}/macos.cmake"
    -C "${script_dir}/instrumented-macos.cmake"
  )
  record_command "${work_root}/cmake-arguments.instrumented.txt" "${cmake_args[@]}"
  "${cmake_args[@]}"
  ninja -C "${instrumented_build}" -j "${CLANGUP_JOBS}" install
  test -x "${CLANGUP_INSTRUMENTED_PREFIX}/bin/clang++"
}

stage_train() {
  find "${CLANGUP_PGO_DIR}" -name '*.profraw' -delete
  local build_type build_dir
  for build_type in "${train_types[@]}"; do
    build_dir="${work_root}/pgo-training-${build_type}"
    rm -rf "${build_dir}"
    export CLANGUP_TRAIN_CC="${CLANGUP_INSTRUMENTED_PREFIX}/bin/clang"
    export CLANGUP_TRAIN_CXX="${CLANGUP_INSTRUMENTED_PREFIX}/bin/clang++"
    export CLANGUP_TRAIN_BUILD_TYPE="${build_type}"
    local cmake_args=(
      cmake -G Ninja
      -S "${CLANGUP_SOURCE}/llvm"
      -B "${build_dir}"
      -C "${script_dir}/training-macos.cmake"
    )
    record_command "${work_root}/cmake-arguments.pgo-${build_type}.txt" "${cmake_args[@]}"
    "${cmake_args[@]}"
    LLVM_PROFILE_FILE="${CLANGUP_PGO_DIR}/${build_type}-%16m.profraw" \
      ninja -C "${build_dir}" -j "${CLANGUP_JOBS}" clang
  done
  find "${CLANGUP_PGO_DIR}" -name '*.profraw' -print -quit | grep -q .
}

stage_merge() {
  local -a profiles=()
  local profile
  while IFS= read -r -d '' profile; do
    profiles+=("${profile}")
  done < <(find "${CLANGUP_PGO_DIR}" -name '*.profraw' -print0)
  if [[ "${#profiles[@]}" -eq 0 ]]; then
    echo "PGO training produced no raw profiles" >&2
    exit 1
  fi
  "${CLANGUP_LLVM_PROFDATA}" merge -o "${CLANGUP_PROFDATA}" "${profiles[@]}"
  test -s "${CLANGUP_PROFDATA}"
}

stage_final() {
  rm -rf "${CLANGUP_BUILD}" "${CLANGUP_PREFIX}"
  mkdir -p "${CLANGUP_PREFIX}"
  local cmake_args=(
    cmake -G Ninja
    -S "${CLANGUP_SOURCE}/llvm"
    -B "${CLANGUP_BUILD}"
    -C "${script_dir}/common.cmake"
    -C "${script_dir}/macos.cmake"
    -C "${script_dir}/final-macos.cmake"
  )
  record_command "${work_root}/cmake-arguments.final.txt" "${cmake_args[@]}"
  "${cmake_args[@]}"
  ninja -C "${CLANGUP_BUILD}" -j "${CLANGUP_JOBS}"
  ninja -C "${CLANGUP_BUILD}" install
  ninja -C "${CLANGUP_BUILD}" install-builtins install-runtimes
  test -x "${CLANGUP_PREFIX}/bin/clang"
}

stage_bolt_profile() { :; }
stage_bolt() { :; }

run_stage() {
  local name="$1"
  local stamp="${stamp_dir}/${name}"
  local function_name="stage_${name//-/_}"
  if [[ -f "${stamp}" ]]; then
    echo "skip ${name}: checkpoint exists"
    return
  fi
  "${function_name}"
  touch "${stamp}"
}

stages=(instrumented train merge final bolt-profile bolt)
start_index=-1
stop_index=-1
for index in "${!stages[@]}"; do
  [[ "${stages[index]}" == "${start_at}" ]] && start_index="${index}"
  [[ "${stages[index]}" == "${stop_after}" ]] && stop_index="${index}"
done
if [[ "${start_index}" -lt 0 || "${stop_index}" -lt 0 || "${start_index}" -gt "${stop_index}" ]]; then
  echo "invalid stage range: ${start_at}..${stop_after}" >&2
  exit 1
fi
if [[ "${start_at}" == final && ! -s "${CLANGUP_PROFDATA}" ]]; then
  echo "final stage requires a merged PGO profile" >&2
  exit 1
fi
if [[ "${start_at}" == train ]]; then
  test -x "${CLANGUP_INSTRUMENTED_PREFIX}/bin/clang"
  test -x "${CLANGUP_INSTRUMENTED_PREFIX}/bin/clang++"
fi

for ((index = start_index; index <= stop_index; index++)); do
  run_stage "${stages[index]}"
done

{
  for file in "${work_root}"/cmake-arguments.*.txt; do
    printf '[%s]\n' "$(basename -- "${file}")"
    cat "${file}"
  done
} > "${work_root}/cmake-arguments.txt"

if [[ "${stop_after}" != bolt ]]; then
  exit 0
fi

python3 - "${CLANGUP_PROFDATA}" "${work_root}/optimization.json" <<'PY'
import hashlib
import json
from pathlib import Path
import sys

profile = Path(sys.argv[1])
record = {
    "schema": "clangup.optimization-build/v1",
    "pgo": {
        "enabled": True,
        "instrumentation": "ir",
        "profile_sha256": hashlib.sha256(profile.read_bytes()).hexdigest(),
        "training": ["Debug", "Release"],
    },
    "lto": "none",
    "bolt": {"enabled": False, "method": "none", "inputs": []},
}
Path(sys.argv[2]).write_text(
    json.dumps(record, sort_keys=True, separators=(",", ":")) + "\n",
    encoding="utf-8",
)
PY
