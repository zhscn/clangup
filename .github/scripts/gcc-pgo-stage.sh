#!/usr/bin/env bash
set -euo pipefail

stage=$1
repo=$PWD
channel=channels/gcc/16.2.0
root=$(realpath "${RUNNER_TEMP:?}")/clangup-gcc-pgo
tar=tar
if [[ $RUNNER_OS == macOS ]]; then
    # GCC stores absolute paths in its build tree. RUNNER_TEMP can vary by runner.
    root=/private/tmp/clangup-gcc-pgo
    tar=gtar
fi

mkdir -p "$root" out/stage
if [[ $stage == profile ]]; then
    if [[ ! $SEED_RELEASE =~ ^[0-9]+\.[0-9]+\.[0-9]+-[1-9][0-9]*$ ]]; then
        echo 'Expected a published GCC release such as 16.2.0-1' >&2
        exit 2
    fi
    mkdir -p "$root/seed"
    go run ./cmd/channel-plan "$channel/release-pgo.yaml" "$root/plan.json"
    curl -fsSL --retry 3 "$(jq -er '.source.url' "$root/plan.json")" -o "$root/gcc.tar.xz"
    curl -fsSL --retry 3 "$DOWNLOAD_ROOT/releases/gcc/$SEED_RELEASE/release.json" -o "$root/seed-release.json"
    key=$(jq -er --arg target "$TARGET" '.artifacts[] | select(.target == $target) | .artifact.key' "$root/seed-release.json")
    sha=$(jq -er --arg target "$TARGET" '.artifacts[] | select(.target == $target) | .artifact.sha256' "$root/seed-release.json")
    curl -fsSL --retry 3 "$DOWNLOAD_ROOT/$key" -o "$root/seed.tar.zst"
    printf '%s  %s\n' "$sha" "$root/seed.tar.zst" | shasum -a 256 -c -
    "$tar" --use-compress-program=unzstd -xf "$root/seed.tar.zst" -C "$root/seed"
    rm "$root/seed.tar.zst"
    printf '%s\n' "gcc@$SEED_RELEASE#$TARGET:sha256:$sha" > "$root/seed-identity"
else
    "$tar" --use-compress-program=unzstd --no-same-owner -xf out/checkpoint/checkpoint.tar.zst -C "$root"
    rm out/checkpoint/checkpoint.tar.zst
fi

export CLANGUP_BUILD_COMMIT=$GITHUB_SHA
export CLANGUP_BOOTSTRAP_IDENTITY
CLANGUP_BOOTSTRAP_IDENTITY=$(cat "$root/seed-identity")
epoch=$(git log -1 --format=%ct)
if [[ $RUNNER_OS == Linux ]]; then
    docker build --tag clangup-gcc-pgo-builder:local - <<DOCKERFILE
FROM $BUILDER_IMAGE
RUN yum -y --setopt=retries=10 install gmp-devel mpfr-devel libmpc-devel flex && yum clean all
DOCKERFILE
    image=clangup-gcc-pgo-builder:local
    packages=$(docker run --rm --network none "$image" rpm -qa | LC_ALL=C sort | sha256sum | cut -d' ' -f1)
    export CLANGUP_BUILD_ENVIRONMENT_IDENTITY="$BUILDER_IMAGE#rpm-sha256:$packages"
    mkdir -p "$root/home"
    docker run --rm --network none --user "$(id -u):$(id -g)" \
        -v "$repo:/workspace:ro" -v "$root:/pgo" -v "$repo/out/stage:/out" \
        -e HOME=/pgo/home -e CLANGUP_BUILD_COMMIT -e CLANGUP_BUILD_ENVIRONMENT_IDENTITY \
        -e CLANGUP_BOOTSTRAP_IDENTITY -e CLANGUP_BOOTSTRAP_PREFIX=/pgo/seed \
        "$image" python3 /workspace/channels/build/run.py \
        --plan /pgo/plan.json --source /pgo/gcc.tar.xz --target "$TARGET" \
        --channel-root "/workspace/$channel" --config-dir "/workspace/$channel/build" \
        --work /pgo/work --output /out --jobs "$(nproc)" --source-date-epoch "$epoch" --gcc-stage "$stage"
else
    export CLANGUP_BOOTSTRAP_PREFIX="$root/seed"
    export CLANGUP_BUILD_ENVIRONMENT_IDENTITY
    CLANGUP_BUILD_ENVIRONMENT_IDENTITY=$(
        { xcodebuild -version; xcrun --show-sdk-path; sw_vers; brew list --versions make gmp mpfr libmpc; } |
            shasum -a 256 | cut -d' ' -f1
    )
    python3 channels/build/run.py --plan "$root/plan.json" --source "$root/gcc.tar.xz" \
        --target "$TARGET" --channel-root "$channel" --config-dir "$channel/build" \
        --work "$root/work" --output out/stage --jobs "$(sysctl -n hw.ncpu)" \
        --source-date-epoch "$epoch" --gcc-stage "$stage"
fi

if [[ $stage != feedback ]]; then
    rm -f "$root/gcc.tar.xz"
    # Preserve subsecond mtimes and links so make can reuse the previous stages.
    "$tar" --format=pax --pax-option=delete=atime,delete=ctime \
        --use-compress-program='zstd -T0 -3' -cf out/stage/checkpoint.tar.zst \
        -C "$root" plan.json seed-identity seed work
    exit 0
fi

mkdir -p "$root/relocated"
"$tar" --use-compress-program=unzstd -xf out/stage/toolchain.tar.zst -C "$root/relocated"
if [[ $RUNNER_OS == Linux ]]; then
    docker run --rm --network none --user "$(id -u):$(id -g)" \
        -v "$root/relocated:/toolchain:ro" -v "$root:/pgo" \
        -v "$repo/$channel/build:/verify:ro" \
        "$image" bash /verify/verify-linux.sh /toolchain /pgo/standalone
    docker run --rm --network none -v "$root/standalone:/app:ro" "$VERIFY_IMAGE" /app
else
    bash "$channel/build/verify-macos.sh" "$root/relocated" "$root/standalone"
fi
