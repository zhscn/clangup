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
: "${CLANGUP_CPU_ISA:?}"
: "${CLANGUP_OPTIMIZATION_PGO:?}"
: "${CLANGUP_OPTIMIZATION_BOLT:?}"

start_at="${CLANGUP_START_AT:-instrumented}"
stop_after="${CLANGUP_STOP_AFTER:-bolt}"

if [[ "${CLANGUP_OPTIMIZATION_PGO}" != 1 ]]; then
  echo "libcxx-pgo requires PGO for every target" >&2
  exit 1
fi

export CLANGUP_BOOTSTRAP_PREFIX="${CLANGUP_BOOTSTRAP_PREFIX:-/opt/clangup-bootstrap}"
export CLANGUP_BUILDER_PREFIX="${CLANGUP_BUILDER_PREFIX:-/opt/clangup-builder}"
export CLANGUP_BUILD_CONFIG_DIR="${script_dir}"

case "${CLANGUP_ARCH}" in
  x86_64)
    expected_machine=x86_64
    export CLANGUP_LLVM_TARGETS=X86
    ;;
  aarch64)
    expected_machine=aarch64
    export CLANGUP_LLVM_TARGETS=AArch64
    if [[ "${CLANGUP_OPTIMIZATION_BOLT}" == 1 ]]; then
      echo "BOLT is not enabled for the aarch64 channel target" >&2
      exit 1
    fi
    ;;
  *)
    echo "unsupported Linux architecture: ${CLANGUP_ARCH}" >&2
    exit 1
    ;;
esac
export CLANGUP_OPTIMIZATION_CFLAGS="-march=${CLANGUP_CPU_ISA}"
if [[ "$(uname -s)" != Linux || "$(uname -m)" != "${expected_machine}" ]]; then
  echo "target ${CLANGUP_TARGET_TRIPLE} requires Linux/${expected_machine}" >&2
  exit 1
fi

work_root="$(dirname -- "${CLANGUP_BUILD}")"
instrumented_build="${work_root}/instrumented-build"
export CLANGUP_INSTRUMENTED_PREFIX="${work_root}/instrumented-prefix"
export CLANGUP_PGO_DIR="${work_root}/pgo"
export CLANGUP_PROFDATA="${CLANGUP_PGO_DIR}/clang.profdata"
bolt_dir="${work_root}/bolt"
bolt_profile_dir="${work_root}/bolt-profiles"
stamp_dir="${work_root}/stamps"
mkdir -p "${CLANGUP_PGO_DIR}" "${bolt_dir}" "${bolt_profile_dir}" "${stamp_dir}"

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
train_targets=(clang lld)

record_command() {
  local path="$1"
  shift
  printf '%s\n' "$@" > "${path}"
}

write_driver_config() {
  local prefix="$1"
  mkdir -p "${prefix}/etc/clang"
  printf '%s\n' '-L<CFGDIR>/../../lib' > "${prefix}/etc/clang/clang.cfg"
  printf '%s\n' '-L<CFGDIR>/../../lib' > "${prefix}/etc/clang/clang++.cfg"
}

configure_training_tree() {
  local build_dir="$1"
  local cc="$2"
  local cxx="$3"
  local build_type="$4"
  local record="$5"
  rm -rf "${build_dir}"
  export CLANGUP_TRAIN_CC="${cc}"
  export CLANGUP_TRAIN_CXX="${cxx}"
  export CLANGUP_TRAIN_AR="${CLANGUP_BOOTSTRAP_PREFIX}/bin/llvm-ar"
  export CLANGUP_TRAIN_NM="${CLANGUP_BOOTSTRAP_PREFIX}/bin/llvm-nm"
  export CLANGUP_TRAIN_RANLIB="${CLANGUP_BOOTSTRAP_PREFIX}/bin/llvm-ranlib"
  export CLANGUP_TRAIN_BUILD_TYPE="${build_type}"
  local cmake_args=(
    cmake -G Ninja
    -S "${CLANGUP_SOURCE}/llvm"
    -B "${build_dir}"
    -C "${script_dir}/training.cmake"
  )
  record_command "${record}" "${cmake_args[@]}"
  CFLAGS= CXXFLAGS= "${cmake_args[@]}"
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
    -C "${script_dir}/linux.cmake"
    -C "${script_dir}/instrumented.cmake"
  )
  record_command "${work_root}/cmake-arguments.instrumented.txt" "${cmake_args[@]}"
  "${cmake_args[@]}"
  ninja -C "${instrumented_build}" -j "${CLANGUP_JOBS}" install

  mkdir -p \
    "${CLANGUP_INSTRUMENTED_PREFIX}/include/c++" \
    "${CLANGUP_INSTRUMENTED_PREFIX}/lib/clang"
  cp -a "${CLANGUP_BOOTSTRAP_PREFIX}/include/c++/." \
    "${CLANGUP_INSTRUMENTED_PREFIX}/include/c++/"
  cp -a "${CLANGUP_BOOTSTRAP_PREFIX}/lib/clang/." \
    "${CLANGUP_INSTRUMENTED_PREFIX}/lib/clang/"
  cp -a "${CLANGUP_BOOTSTRAP_PREFIX}/lib/libc++.a" \
    "${CLANGUP_BOOTSTRAP_PREFIX}/lib/libc++abi.a" \
    "${CLANGUP_INSTRUMENTED_PREFIX}/lib/"
  write_driver_config "${CLANGUP_INSTRUMENTED_PREFIX}"

  test -x "${CLANGUP_INSTRUMENTED_PREFIX}/bin/clang++"
  runtime_dir="$("${CLANGUP_INSTRUMENTED_PREFIX}/bin/clang" --print-runtime-dir)"
  find "${runtime_dir}" -maxdepth 1 -name 'libclang_rt.profile*.a' -print -quit \
    | grep -q .
}

stage_train() {
  find "${CLANGUP_PGO_DIR}" -name '*.profraw' -delete
  local build_type
  for build_type in "${train_types[@]}"; do
    local build_dir="${work_root}/pgo-training-${build_type}"
    configure_training_tree \
      "${build_dir}" \
      "${CLANGUP_INSTRUMENTED_PREFIX}/bin/clang" \
      "${CLANGUP_INSTRUMENTED_PREFIX}/bin/clang++" \
      "${build_type}" \
      "${work_root}/cmake-arguments.pgo-${build_type}.txt"
    LLVM_PROFILE_FILE="${CLANGUP_PGO_DIR}/${build_type}-%16m.profraw" \
      ninja -C "${build_dir}" -j "${CLANGUP_JOBS}" "${train_targets[@]}"
  done
  find "${CLANGUP_PGO_DIR}" -name '*.profraw' -print -quit | grep -q .
}

stage_merge() {
  local -a profiles
  mapfile -d '' profiles < <(find "${CLANGUP_PGO_DIR}" -name '*.profraw' -print0)
  if [[ "${#profiles[@]}" -eq 0 ]]; then
    echo "PGO training produced no raw profiles" >&2
    exit 1
  fi
  "${CLANGUP_BOOTSTRAP_PREFIX}/bin/llvm-profdata" merge \
    -o "${CLANGUP_PROFDATA}" "${profiles[@]}"
  test -s "${CLANGUP_PROFDATA}"
}

stage_final() {
  rm -rf "${CLANGUP_BUILD}" "${CLANGUP_PREFIX}"
  rm -rf "${bolt_dir}"
  mkdir -p "${bolt_dir}"
  mkdir -p "${CLANGUP_PREFIX}"
  local cmake_args=(
    cmake -G Ninja
    -S "${CLANGUP_SOURCE}/llvm"
    -B "${CLANGUP_BUILD}"
    -C "${script_dir}/common.cmake"
    -C "${script_dir}/linux.cmake"
    -C "${script_dir}/final.cmake"
  )
  record_command "${work_root}/cmake-arguments.final.txt" "${cmake_args[@]}"
  "${cmake_args[@]}"
  ninja -C "${CLANGUP_BUILD}" -j "${CLANGUP_JOBS}"
  ninja -C "${CLANGUP_BUILD}" install
  ninja -C "${CLANGUP_BUILD}" install-builtins install-runtimes
  write_driver_config "${CLANGUP_PREFIX}"

  export CLANGUP_RESOURCE_DIR="$("${CLANGUP_PREFIX}/bin/clang" --print-resource-dir)"
  compiler_rt_build="${work_root}/compiler-rt"
  rm -rf "${compiler_rt_build}"
  local compiler_rt_args=(
    cmake -G Ninja
    -S "${CLANGUP_SOURCE}/compiler-rt"
    -B "${compiler_rt_build}"
    -C "${script_dir}/compiler-rt.cmake"
  )
  record_command "${work_root}/cmake-arguments.compiler-rt.txt" \
    "${compiler_rt_args[@]}"
  "${compiler_rt_args[@]}"
  ninja -C "${compiler_rt_build}" -j "${CLANGUP_JOBS}"
  ninja -C "${compiler_rt_build}" install

  test -x "${CLANGUP_PREFIX}/bin/clang-22"
  test -x "${CLANGUP_PREFIX}/bin/llvm-bolt"
  test -x "${CLANGUP_PREFIX}/bin/perf2bolt"
  test -x "${CLANGUP_PREFIX}/bin/merge-fdata"
}

bolt_input_paths() {
  readlink -f "${CLANGUP_PREFIX}/bin/clang"
  readlink -f "${CLANGUP_PREFIX}/lib/libclang-cpp.so"
  readlink -f "${CLANGUP_PREFIX}/lib/libLLVM.so"
  readlink -f "${CLANGUP_PREFIX}/bin/lld"
}

stage_bolt_profile() {
  local -a inputs
  if [[ "${CLANGUP_OPTIMIZATION_BOLT}" != 1 ]]; then
    return
  fi
  command -v perf >/dev/null
  perf record -e cycles:u -j any,u -o "${bolt_dir}/preflight.perf.data" \
    -- "${CLANGUP_PREFIX}/bin/clang" --version
  rm -f "${bolt_dir}/preflight.perf.data"

  mapfile -t inputs < <(bolt_input_paths)
  local input relative stem build_type
  for input in "${inputs[@]}"; do
    [[ "${input}" == "${CLANGUP_PREFIX}/"* ]] || {
      echo "BOLT input escapes toolchain prefix: ${input}" >&2
      exit 1
    }
    test -f "${input}"
  done

  for build_type in "${train_types[@]}"; do
    local build_dir="${work_root}/bolt-training-${build_type}"
    configure_training_tree \
      "${build_dir}" \
      "${CLANGUP_PREFIX}/bin/clang" \
      "${CLANGUP_PREFIX}/bin/clang++" \
      "${build_type}" \
      "${work_root}/cmake-arguments.bolt-${build_type}.txt"
    perf record -e cycles:u -j any,u \
      -o "${bolt_dir}/${build_type}.perf.data" \
      -- ninja -C "${build_dir}" -j "${CLANGUP_JOBS}" "${train_targets[@]}"
    for input in "${inputs[@]}"; do
      relative="${input#${CLANGUP_PREFIX}/}"
      stem="$(printf '%s' "${relative}" | tr '/.' '__')"
      "${CLANGUP_PREFIX}/bin/perf2bolt" \
        -p "${bolt_dir}/${build_type}.perf.data" \
        -o "${bolt_profile_dir}/${stem}-${build_type}.fdata" "${input}"
      test -s "${bolt_profile_dir}/${stem}-${build_type}.fdata"
    done
    rm -f "${bolt_dir}/${build_type}.perf.data"
  done
}

stage_bolt() {
  local -a inputs profiles relatives
  : > "${bolt_dir}/inputs.list"
  if [[ "${CLANGUP_OPTIMIZATION_BOLT}" != 1 ]]; then
    return
  fi

  local snapshot_dir="${bolt_dir}/original"
  local snapshot_list="${snapshot_dir}/inputs.list"
  local input relative
  if [[ -s "${snapshot_list}" ]]; then
    mapfile -t relatives < "${snapshot_list}"
    inputs=()
    for relative in "${relatives[@]}"; do
      input="${CLANGUP_PREFIX}/${relative}"
      test -f "${snapshot_dir}/${relative}"
      cp -p "${snapshot_dir}/${relative}" "${input}"
      inputs+=("${input}")
    done
  else
    rm -rf "${snapshot_dir}"
    mkdir -p "${snapshot_dir}"
    mapfile -t inputs < <(bolt_input_paths)
    for input in "${inputs[@]}"; do
      [[ "${input}" == "${CLANGUP_PREFIX}/"* ]] || {
        echo "BOLT input escapes toolchain prefix: ${input}" >&2
        exit 1
      }
      test -f "${input}"
      relative="${input#${CLANGUP_PREFIX}/}"
      mkdir -p "${snapshot_dir}/$(dirname -- "${relative}")"
      cp -p "${input}" "${snapshot_dir}/${relative}"
      printf '%s\n' "${relative}" >> "${snapshot_list}.tmp"
    done
    mv "${snapshot_list}.tmp" "${snapshot_list}"
  fi

  local stem profile merged output build_type
  for input in "${inputs[@]}"; do
    relative="${input#${CLANGUP_PREFIX}/}"
    stem="$(printf '%s' "${relative}" | tr '/.' '__')"
    profiles=()
    for build_type in Debug Release; do
      profile="${bolt_profile_dir}/${stem}-${build_type}.fdata"
      test -s "${profile}"
      profiles+=("${profile}")
    done
    merged="${bolt_dir}/${stem}.fdata"
    "${CLANGUP_PREFIX}/bin/merge-fdata" -o "${merged}" "${profiles[@]}"
    test -s "${merged}"
    output="${input}.bolt"
    "${CLANGUP_PREFIX}/bin/llvm-bolt" \
      "${input}" \
      -o "${output}" \
      -data="${merged}" \
      -reorder-blocks=ext-tsp \
      -reorder-functions=cdsort \
      -split-functions \
      -split-all-cold \
      -dyno-stats \
      -icf=all \
      -use-gnu-stack
    mv "${output}" "${input}"
    chmod 0755 "${input}"
    printf '%s\n' "${relative}" >> "${bolt_dir}/inputs.list"
  done
}

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
  if [[ "${stages[index]}" == "${start_at}" ]]; then
    start_index="${index}"
  fi
  if [[ "${stages[index]}" == "${stop_after}" ]]; then
    stop_index="${index}"
  fi
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
  for tool in clang clang++; do
    test -x "${CLANGUP_INSTRUMENTED_PREFIX}/bin/${tool}" || {
      echo "training stage requires ${CLANGUP_INSTRUMENTED_PREFIX}/bin/${tool}" >&2
      exit 1
    }
  done
fi
if [[ "${start_at}" == bolt || "${start_at}" == bolt-profile ]]; then
  for tool in clang clang++ llvm-bolt perf2bolt merge-fdata; do
    test -x "${CLANGUP_PREFIX}/bin/${tool}" || {
      echo "BOLT stage requires ${CLANGUP_PREFIX}/bin/${tool}" >&2
      exit 1
    }
  done
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

export CLANGUP_OPTIMIZATION_INPUTS="${bolt_dir}/inputs.list"
python3 - "${work_root}/optimization.json" <<'PY'
import hashlib
import json
import os
from pathlib import Path
import sys


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as file:
        for chunk in iter(lambda: file.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


profile = Path(os.environ["CLANGUP_PROFDATA"])
inputs_path = Path(os.environ["CLANGUP_OPTIMIZATION_INPUTS"])
bolt_enabled = os.environ["CLANGUP_OPTIMIZATION_BOLT"] == "1"
inputs = inputs_path.read_text(encoding="utf-8").splitlines() if bolt_enabled else []
record = {
    "schema": "clangup.optimization-build/v1",
    "pgo": {
        "enabled": True,
        "instrumentation": "ir",
        "profile_sha256": sha256(profile),
        "training": ["Debug", "Release"],
    },
    "lto": "thin",
    "bolt": {
        "enabled": bolt_enabled,
        "method": "lbr" if bolt_enabled else "none",
        "inputs": inputs,
    },
}
Path(sys.argv[1]).write_text(
    json.dumps(record, sort_keys=True, separators=(",", ":")) + "\n",
    encoding="utf-8",
)
PY
