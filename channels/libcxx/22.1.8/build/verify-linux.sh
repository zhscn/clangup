#!/usr/bin/env bash
set -euo pipefail

prefix="${1:?usage: verify-linux.sh <prefix> <profile>}"
profile="${2:?usage: verify-linux.sh <prefix> <profile>}"
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export BASE_PROFILE="${profile}"

bash "${script_dir}/../../../../docker/scripts/fix-el-repos.sh"
package_manager="$(command -v dnf || command -v yum)"
"${package_manager}" install -y gcc gcc-c++ glibc-devel binutils

"${prefix}/bin/clang" --version
"${prefix}/bin/ld.lld" --version

cat >/tmp/clangup-libcxx-smoke.cc <<'EOF'
#include <algorithm>
#include <concepts>
#include <format>
#include <ranges>
#include <span>
#include <stdexcept>
#include <string>
#include <vector>

template <std::integral T>
T sum(std::span<const T> values) {
  T result{};
  for (T value : values | std::views::filter([](T value) { return value > 1; }))
    result += value;
  return result;
}

int main() {
  std::vector<int> values{3, 1, 2};
  std::ranges::sort(values);
  try {
    std::string result = std::format(
        "{}:{}", "libcxx", sum<int>(std::span<const int>(values)));
    if (result != "libcxx:5")
      return 1;
    throw std::runtime_error(result);
  } catch (const std::runtime_error &error) {
    return std::string(error.what()) != "libcxx:5";
  }
}
EOF

"${prefix}/bin/clang++" -std=c++20 -### /tmp/clangup-libcxx-smoke.cc \
  -o /tmp/clangup-libcxx-smoke 2>/tmp/clangup-libcxx-driver.txt
grep -q -- '-lc++' /tmp/clangup-libcxx-driver.txt
grep -q -- 'ld.lld' /tmp/clangup-libcxx-driver.txt
grep -q -- 'clang_rt' /tmp/clangup-libcxx-driver.txt
grep -q -- '-lgcc_s' /tmp/clangup-libcxx-driver.txt
if grep -q -- '-lstdc++' /tmp/clangup-libcxx-driver.txt; then
  echo "libcxx driver unexpectedly selects libstdc++" >&2
  exit 1
fi

"${prefix}/bin/clang++" -std=c++20 /tmp/clangup-libcxx-smoke.cc \
  -o /tmp/clangup-libcxx-smoke
/tmp/clangup-libcxx-smoke
ldd /tmp/clangup-libcxx-smoke | tee /tmp/clangup-libcxx-smoke.ldd
grep -q 'libgcc_s[.]so' /tmp/clangup-libcxx-smoke.ldd
if grep -Eq 'libstdc[+][+][.]so|libc[+][+][.]so|libc[+][+]abi[.]so' \
    /tmp/clangup-libcxx-smoke.ldd; then
  echo "default libc++ executable has a dynamic C++ runtime dependency" >&2
  exit 1
fi

runtime_dir="$("${prefix}/bin/clang" --print-runtime-dir)"
case "${runtime_dir}" in
  "${prefix}"/*) ;;
  *) echo "Clang runtime directory escapes the prefix: ${runtime_dir}" >&2; exit 1 ;;
esac
for runtime in builtins asan profile fuzzer; do
  test -f "${runtime_dir}/libclang_rt.${runtime}.a"
done

bash "${script_dir}/verify-asan-matrix.sh" "${prefix}"

for tool in \
  clang clang++ clangd clang-tidy ld.lld \
  llvm-ar llvm-cov llvm-dwp llvm-nm llvm-objcopy llvm-profdata \
  llvm-ranlib llvm-readelf llvm-readobj llvm-strip llvm-symbolizer; do
  test -x "${prefix}/bin/${tool}"
done

case "${profile}" in
  el7) test "$(uname -m)" = x86_64 ;;
  el8) test "$(uname -m)" = aarch64 ;;
  *) echo "unknown profile: ${profile}" >&2; exit 2 ;;
esac
