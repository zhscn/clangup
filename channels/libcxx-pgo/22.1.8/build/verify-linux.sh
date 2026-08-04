#!/usr/bin/env bash
set -euo pipefail

prefix="${1:?usage: verify-linux.sh <prefix> <profile>}"
profile="${2:?usage: verify-linux.sh <prefix> <profile>}"
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

bash "${script_dir}/../../../libcxx/22.1.8/build/verify-linux.sh" \
  "${prefix}" "${profile}"

for tool in llvm-bolt merge-fdata perf2bolt; do
  test -x "${prefix}/bin/${tool}"
done

if [[ "$(uname -m)" == x86_64 ]]; then
  for input in \
    "$(readlink -f "${prefix}/bin/clang")" \
    "$(readlink -f "${prefix}/lib/libclang-cpp.so")" \
    "$(readlink -f "${prefix}/lib/libLLVM.so")" \
    "$(readlink -f "${prefix}/bin/lld")"; do
    "${prefix}/bin/llvm-readelf" -S --wide "${input}" | grep -Fq .bolt.org.text
  done
fi
