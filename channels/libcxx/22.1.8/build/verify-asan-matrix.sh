#!/usr/bin/env bash
set -euo pipefail

prefix="${1:?usage: verify-asan-matrix.sh <prefix>}"
work=/tmp/clangup-libcxx-asan-matrix
rm -rf "${work}"
mkdir -p "${work}"

cat >"${work}/safe.cc" <<'EOF'
#include <atomic>
#include <stdexcept>
#include <string>
#include <thread>
#include <vector>

int main() {
  std::atomic<int> value{0};
  std::thread worker([&] { value.store(42); });
  worker.join();
  try {
    throw std::runtime_error(std::string("asan-matrix"));
  } catch (const std::runtime_error &error) {
    std::vector<int> values{value.load()};
    return values.front() != 42 || std::string(error.what()) != "asan-matrix";
  }
}
EOF

cat >"${work}/bugs.cc" <<'EOF'
#include <cstring>

__attribute__((noinline)) int heap_overflow() {
  volatile int index = 1;
  int *values = new int[1];
  values[index] = 42;
  delete[] values;
  return 0;
}

__attribute__((noinline)) int heap_use_after_free() {
  int *value = new int(42);
  delete value;
  return *value;
}

__attribute__((noinline)) int stack_overflow() {
  volatile int index = 1;
  int values[1] = {0};
  values[index] = 42;
  return values[0];
}

int main(int argc, char **argv) {
  if (argc != 2)
    return 2;
  if (std::strcmp(argv[1], "heap") == 0)
    return heap_overflow();
  if (std::strcmp(argv[1], "uaf") == 0)
    return heap_use_after_free();
  if (std::strcmp(argv[1], "stack") == 0)
    return stack_overflow();
  return 2;
}
EOF

cat >"${work}/dso.c" <<'EOF'
#include <stdlib.h>

__attribute__((visibility("default"), noinline)) void dso_overflow(void) {
  volatile int index = 1;
  char *value = malloc(1);
  value[index] = 42;
  free(value);
}
EOF

cat >"${work}/dso-main.cc" <<'EOF'
extern "C" void dso_overflow();

int main() {
  dso_overflow();
  return 0;
}
EOF

common_flags=(-O0 -g -fno-omit-frame-pointer -fsanitize=address)
asan_options="detect_leaks=0:abort_on_error=1:symbolize=1"
export ASAN_SYMBOLIZER_PATH="${prefix}/bin/llvm-symbolizer"

expect_asan_failure() {
  local name="$1"
  local diagnostic="$2"
  shift 2
  local stdout="${work}/${name}.stdout"
  local stderr="${work}/${name}.stderr"
  set +e
  ASAN_OPTIONS="${asan_options}" "$@" >"${stdout}" 2>"${stderr}"
  local status=$?
  set -e
  if [[ ${status} -eq 0 ]]; then
    echo "${name}: ASan did not terminate the faulty program" >&2
    exit 1
  fi
  if ! grep -q "AddressSanitizer: ${diagnostic}" "${stderr}"; then
    echo "${name}: expected AddressSanitizer diagnostic ${diagnostic}" >&2
    cat "${stderr}" >&2
    exit 1
  fi
}

"${prefix}/bin/clang" "${common_flags[@]}" -fPIC -shared \
  "${work}/dso.c" -o "${work}/libasan-matrix.so"

for stdlib in libstdc++ libc++; do
  stdlib_flags=(-stdlib="${stdlib}")

  "${prefix}/bin/clang++" -std=c++11 -pthread "${stdlib_flags[@]}" \
    "${common_flags[@]}" "${work}/safe.cc" -o "${work}/safe-${stdlib}"
  ASAN_OPTIONS="${asan_options}" "${work}/safe-${stdlib}"

  ldd "${work}/safe-${stdlib}" >"${work}/safe-${stdlib}.ldd"
  if grep -q 'libasan[.]so' "${work}/safe-${stdlib}.ldd"; then
    echo "${stdlib}: ASan unexpectedly depends on a system shared runtime" >&2
    exit 1
  fi
  if [[ "${stdlib}" == libstdc++ ]]; then
    grep -q 'libstdc[+][+][.]so' "${work}/safe-${stdlib}.ldd"
  elif grep -Eq 'libstdc[+][+][.]so|libc[+][+][.]so|libc[+][+]abi[.]so' \
      "${work}/safe-${stdlib}.ldd"; then
    echo "libc++: expected the bundled static C++ runtime" >&2
    cat "${work}/safe-${stdlib}.ldd" >&2
    exit 1
  fi

  "${prefix}/bin/clang++" -std=c++11 "${stdlib_flags[@]}" \
    "${common_flags[@]}" "${work}/bugs.cc" -o "${work}/bugs-${stdlib}"
  expect_asan_failure "${stdlib}-heap" heap-buffer-overflow \
    "${work}/bugs-${stdlib}" heap
  expect_asan_failure "${stdlib}-uaf" heap-use-after-free \
    "${work}/bugs-${stdlib}" uaf
  expect_asan_failure "${stdlib}-stack" stack-buffer-overflow \
    "${work}/bugs-${stdlib}" stack

  "${prefix}/bin/clang++" -std=c++11 "${stdlib_flags[@]}" \
    "${common_flags[@]}" "${work}/dso-main.cc" \
    -L"${work}" -lasan-matrix -Wl,-rpath,"${work}" \
    -o "${work}/dso-${stdlib}"
  expect_asan_failure "${stdlib}-dso" heap-buffer-overflow \
    "${work}/dso-${stdlib}"
done
