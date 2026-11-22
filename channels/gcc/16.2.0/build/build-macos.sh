#!/usr/bin/env bash
set -euo pipefail

source=$(realpath "$1")
build=$2
prefix=$3
seed=$4
jobs=$5
stage=${6:-bootstrap}
cc=clang
cxx=clang++
if [[ $stage != bootstrap ]]; then
    cc=gcc
    cxx=g++
fi

if [[ $(uname -sm) != 'Darwin arm64' ]]; then
    echo 'GCC macOS builds require Apple Silicon' >&2
    exit 1
fi

mkdir -p "$build" "$prefix"
prefix=$(realpath "$prefix")
cd "$build"

export SDKROOT=$(xcrun --sdk macosx --show-sdk-path)
export MACOSX_DEPLOYMENT_TARGET=14.0
if [[ ! -f Makefile ]]; then
    # Use the static math archives so the compiler can move to a host without Homebrew.
    mkdir -p host-math
    math_libs=$(realpath host-math)
    gmp=$(brew --prefix gmp)
    mpfr=$(brew --prefix mpfr)
    mpc=$(brew --prefix libmpc)
    ln -s "$gmp/lib/libgmp.a" "$math_libs/"
    ln -s "$mpfr/lib/libmpfr.a" "$math_libs/"
    ln -s "$mpc/lib/libmpc.a" "$math_libs/"

    CC="$seed/bin/$cc" CXX="$seed/bin/$cxx" \
        "$source/configure" --prefix="$prefix" --with-sysroot="$SDKROOT" \
        --build="aarch64-apple-darwin$(uname -r)" \
        --enable-languages=c,c++ --enable-bootstrap --enable-checking=release \
        --enable-darwin-at-rpath --disable-multilib --disable-nls \
        --with-gmp-include="$gmp/include" --with-gmp-lib="$math_libs" \
        --with-mpfr-include="$mpfr/include" --with-mpfr-lib="$math_libs" \
        --with-mpc-include="$mpc/include" --with-mpc-lib="$math_libs"
fi
bash "$(dirname "$0")/bootstrap.sh" gmake "$jobs" "$stage"
if [[ $stage == profile || $stage == train ]]; then
    exit 0
fi

for driver in gcc g++; do
    mv "$prefix/bin/$driver" "$prefix/bin/$driver.real"
    cat > "$prefix/bin/$driver" <<'DRIVER'
#!/bin/bash
set -euo pipefail
if [[ -z ${SDKROOT:-} ]]; then
    export SDKROOT=$(xcrun --sdk macosx --show-sdk-path)
fi
exec "$(realpath "$0").real" "$@"
DRIVER
    chmod +x "$prefix/bin/$driver"
done
