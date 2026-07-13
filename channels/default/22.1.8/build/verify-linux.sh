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

cat >/tmp/clangup-default-smoke.cc <<'EOF'
#include <stdexcept>
#include <string>

int main() {
  try {
    throw std::runtime_error(std::string("default"));
  } catch (const std::runtime_error &) {
    return 0;
  }
}
EOF

"${prefix}/bin/clang++" -### /tmp/clangup-default-smoke.cc -o /tmp/clangup-default-smoke \
  2>/tmp/clangup-default-driver.txt
grep -q -- '-lstdc++' /tmp/clangup-default-driver.txt
if grep -q -- '-lc++' /tmp/clangup-default-driver.txt; then
  echo "default driver unexpectedly selects libc++" >&2
  exit 1
fi
if grep -q -- 'ld.lld' /tmp/clangup-default-driver.txt; then
  echo "default driver unexpectedly selects lld" >&2
  exit 1
fi

"${prefix}/bin/clang++" /tmp/clangup-default-smoke.cc -o /tmp/clangup-default-smoke
/tmp/clangup-default-smoke
ldd /tmp/clangup-default-smoke | tee /tmp/clangup-default-smoke.ldd
grep -q 'libstdc[+][+][.]so' /tmp/clangup-default-smoke.ldd

runtime_dir="$("${prefix}/bin/clang" --print-runtime-dir)"
case "$(uname -m)" in
  x86_64) compatible_triple=x86_64-pc-linux ;;
  aarch64) compatible_triple=aarch64-pc-linux-gnu ;;
  *) echo "unsupported verification architecture: $(uname -m)" >&2; exit 2 ;;
esac
test "$("${prefix}/bin/clang" --target="${compatible_triple}" --print-runtime-dir)" = "${runtime_dir}"

cat >/tmp/clangup-default-libcxx-cxx20.cc <<'EOF'
#include <algorithm>
#include <concepts>
#include <format>
#include <ranges>
#include <span>
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
  std::string result = std::format(
      "{}:{}", "default", sum<int>(std::span<const int>(values)));
  return result != "default:5";
}
EOF

"${prefix}/bin/clang++" -std=c++20 -stdlib=libc++ \
  /tmp/clangup-default-libcxx-cxx20.cc \
  -o /tmp/clangup-default-libcxx-cxx20
/tmp/clangup-default-libcxx-cxx20
if ldd /tmp/clangup-default-libcxx-cxx20 |
    grep -Eq 'libstdc[+][+][.]so|libc[+][+][.]so|libc[+][+]abi[.]so'; then
  echo "explicit libc++ C++20 smoke has a dynamic C++ runtime dependency" >&2
  exit 1
fi

"${prefix}/bin/clang++" --target="${compatible_triple}" -std=c++20 \
  -stdlib=libc++ --rtlib=compiler-rt --unwindlib=libgcc \
  /tmp/clangup-default-libcxx-cxx20.cc \
  -o /tmp/clangup-default-compatible-triple
/tmp/clangup-default-compatible-triple

"${prefix}/bin/clang++" -std=c++20 -stdlib=libc++ -flto \
  -ffat-lto-objects -fuse-ld=lld \
  /tmp/clangup-default-libcxx-cxx20.cc \
  -o /tmp/clangup-default-libcxx-lto
/tmp/clangup-default-libcxx-lto
for archive in libc++.a libc++abi.a; do
  "${prefix}/bin/llvm-readelf" -S "${prefix}/lib/${archive}" \
    >"/tmp/clangup-default-${archive}.sections"
  grep -Fq '.llvm.lto' "/tmp/clangup-default-${archive}.sections"
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
  llvm-ar llvm-bolt llvm-cov llvm-dwp llvm-nm llvm-objcopy llvm-profdata \
  llvm-ranlib llvm-readelf llvm-readobj llvm-strip llvm-symbolizer \
  merge-fdata perf2bolt; do
  test -x "${prefix}/bin/${tool}"
done

case "${profile}" in
  el7) expected=2.17 ;;
  el8) expected=2.28 ;;
  *) echo "unknown profile: ${profile}" >&2; exit 2 ;;
esac
echo "verified default toolchain on ${profile} (glibc baseline ${expected})"
