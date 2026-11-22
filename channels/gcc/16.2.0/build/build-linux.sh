#!/usr/bin/env bash
set -euo pipefail

if [[ $# != 5 ]]; then
    echo "usage: $0 SOURCE BUILD PREFIX CLANG_PREFIX JOBS" >&2
    exit 2
fi
source=$(realpath "$1")
build=$2
prefix=$3
seed=$(realpath "$4")
jobs=$5

if [[ $(getconf GNU_LIBC_VERSION) != 'glibc 2.17' || $(uname -m) != x86_64 ]]; then
    echo 'GCC channel requires an x86-64 glibc 2.17 build environment' >&2
    exit 1
fi

mkdir -p "$build" "$prefix"
prefix=$(realpath "$prefix")
cd "$build"
if [[ ! -f Makefile ]]; then
    CC="$seed/bin/clang" CXX="$seed/bin/clang++" \
        "$source/configure" --prefix="$prefix" \
        --enable-languages=c,c++ --enable-bootstrap --disable-multilib \
        --disable-nls
fi
make -j"$jobs" bootstrap
make install
