#!/usr/bin/env bash
set -euo pipefail

prefix=$1
output=$2
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

source_dir=$(dirname "$(realpath "$0")")
env -u LD_LIBRARY_PATH "$prefix/bin/g++" -std=c++20 -pthread -static-libstdc++ -static-libgcc \
    "$source_dir/smoke.cc" -Wl,-Map,"$work/link.map" -o "$output"

prefix=$(realpath "$prefix")
for library in libstdc++.a libgcc.a libgcc_eh.a; do
    found=false
    while IFS= read -r archive; do
        resolved=$(realpath "$archive")
        if [[ $resolved != "$prefix/"* ]]; then
            echo "$library resolved outside the toolchain: $resolved" >&2
            exit 1
        fi
        found=true
    done < <(awk -v library="$library" '
        /^LOAD / && substr($0, length($0) - length(library)) == "/" library {
            print substr($0, 6)
        }
    ' "$work/link.map")
    if [[ $found == false ]]; then
        echo "$library is missing from the linker map" >&2
        exit 1
    fi
done

dependencies=$(readelf -dW "$output")
if [[ $dependencies == *libstdc++.so* || $dependencies == *libgcc_s.so* ]]; then
    echo 'C++ smoke executable has a dynamic GCC runtime dependency' >&2
    exit 1
fi
env -u LD_LIBRARY_PATH "$output"
