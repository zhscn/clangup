#!/usr/bin/env bash
set -euo pipefail

prefix=$1
output=$2
source_dir=$(dirname "$(realpath "$0")")
env -u SDKROOT -u DYLD_LIBRARY_PATH "$prefix/bin/g++" -std=c++20 -pthread \
    -static-libstdc++ -static-libgcc "$source_dir/smoke.cc" -o "$output"
dependencies=$(otool -L "$output")
if [[ $dependencies == *libstdc++*.dylib* || $dependencies == *libgcc*.dylib* ]]; then
    echo 'C++ smoke executable has a dynamic GCC runtime dependency' >&2
    exit 1
fi
env -u DYLD_LIBRARY_PATH "$output"
