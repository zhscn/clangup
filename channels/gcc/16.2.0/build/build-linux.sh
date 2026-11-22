#!/usr/bin/env bash
set -euo pipefail

if [[ $# != 5 && $# != 6 ]]; then
    echo "usage: $0 SOURCE BUILD PREFIX SEED_PREFIX JOBS [STAGE]" >&2
    exit 2
fi
source=$(realpath "$1")
build=$2
prefix=$3
seed=$(realpath "$4")
jobs=$5
stage=${6:-bootstrap}
cc=clang
cxx=clang++
if [[ $stage != bootstrap ]]; then
    cc=gcc
    cxx=g++
fi

case "$(uname -m):$(getconf GNU_LIBC_VERSION)" in
    "x86_64:glibc 2.17"|"aarch64:glibc 2.28") ;;
    *) echo 'GCC requires x86-64 EL7 or ARM64 EL8' >&2; exit 1 ;;
esac

mkdir -p "$build" "$prefix"
prefix=$(realpath "$prefix")
cd "$build"
if [[ ! -f Makefile ]]; then
    CC="$seed/bin/$cc" CXX="$seed/bin/$cxx" \
        "$source/configure" --prefix="$prefix" \
        --enable-languages=c,c++ --enable-bootstrap --disable-multilib \
        --disable-nls
fi
bash "$(dirname "$0")/bootstrap.sh" make "$jobs" "$stage"
