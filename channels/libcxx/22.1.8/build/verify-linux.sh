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
case "$(uname -m)" in
  x86_64) compatible_triple=x86_64-pc-linux ;;
  aarch64) compatible_triple=aarch64-pc-linux-gnu ;;
  *) echo "unsupported verification architecture: $(uname -m)" >&2; exit 2 ;;
esac
test "$("${prefix}/bin/clang" --target="${compatible_triple}" --print-runtime-dir)" = "${runtime_dir}"

"${prefix}/bin/clang++" --target="${compatible_triple}" -std=c++20 \
  /tmp/clangup-libcxx-smoke.cc -o /tmp/clangup-libcxx-compatible-triple
/tmp/clangup-libcxx-compatible-triple

"${prefix}/bin/clang++" -std=c++20 -flto -ffat-lto-objects -fuse-ld=lld \
  /tmp/clangup-libcxx-smoke.cc -o /tmp/clangup-libcxx-lto
/tmp/clangup-libcxx-lto
for archive in libc++.a libc++abi.a; do
  "${prefix}/bin/llvm-readelf" -S "${prefix}/lib/${archive}" \
    >"/tmp/clangup-libcxx-${archive}.sections"
  grep -Fq '.llvm.lto' "/tmp/clangup-libcxx-${archive}.sections"
done

clang_ldd="$(ldd "${prefix}/bin/clang")"
grep -q 'libclang-cpp[.]so' <<<"${clang_ldd}"
grep -q 'libLLVM[.]so' <<<"$(ldd "${prefix}/bin/llvm-ar")"
if grep -Eq 'libstdc[+][+][.]so|libc[+][+][.]so|libc[+][+]abi[.]so' <<<"${clang_ldd}"; then
  echo "Clang has a dynamic C++ runtime dependency" >&2
  exit 1
fi

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
