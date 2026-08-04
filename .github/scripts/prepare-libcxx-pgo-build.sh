#!/usr/bin/env bash
set -euo pipefail

: "${SOURCE_URL:?}"
: "${SOURCE_SHA256:?}"
: "${TARGET:?}"
: "${DOWNLOAD_ROOT:?}"
: "${BOOTSTRAP_CHANNEL:?}"
: "${BOOTSTRAP_EXACT:?}"
: "${BUILDER_IMAGE:?}"
: "${GITHUB_ENV:?}"

mkdir -p .cache/sources .cache/bootstrap
curl -fsSL -o .cache/sources/llvm-project.tar.xz "${SOURCE_URL}"
echo "${SOURCE_SHA256}  .cache/sources/llvm-project.tar.xz" | sha256sum -c -
curl -fsSL -o .cache/bootstrap-release.json \
  "${DOWNLOAD_ROOT}/releases/${BOOTSTRAP_CHANNEL}/${BOOTSTRAP_EXACT}/release.json"
artifact_key="$(jq -er --arg target "${TARGET}" '.artifacts[] | select(.target == $target) | .artifact.key' .cache/bootstrap-release.json)"
artifact_sha256="$(jq -er --arg target "${TARGET}" '.artifacts[] | select(.target == $target) | .artifact.sha256' .cache/bootstrap-release.json)"
curl -fsSL -o .cache/bootstrap.tar.zst "${DOWNLOAD_ROOT}/${artifact_key}"
echo "${artifact_sha256}  .cache/bootstrap.tar.zst" | sha256sum -c -
tar --use-compress-program=unzstd -xf .cache/bootstrap.tar.zst -C .cache/bootstrap

index_digest="$(docker buildx imagetools inspect "${BUILDER_IMAGE}" --format '{{json .Manifest}}' | jq -er .digest)"
case "${TARGET}" in
  x86_64-unknown-linux-gnu)
    architecture=amd64
    builder_ref="${BUILDER_IMAGE%:*}@${index_digest}"
    docker build --network host \
      --build-arg "BASE_IMAGE=${builder_ref}" \
      -f docker/clangup-pgo-builder/Dockerfile \
      -t clangup-pgo-builder:local .
    image="clangup-pgo-builder:local"
    dockerfile_sha256="$(sha256sum docker/clangup-pgo-builder/Dockerfile | awk '{print $1}')"
    identity="${BUILDER_IMAGE%:*}@${index_digest}#${architecture}#${dockerfile_sha256}"
    ;;
  aarch64-unknown-linux-gnu)
    architecture=arm64
    image="${BUILDER_IMAGE%:*}@${index_digest}"
    identity="${BUILDER_IMAGE%:*}@${index_digest}#${architecture}"
    ;;
  *)
    echo "unsupported target: ${TARGET}" >&2
    exit 1
    ;;
esac
platform_digest="$(docker buildx imagetools inspect "${BUILDER_IMAGE}" --raw | jq -er --arg architecture "${architecture}" '.manifests[] | select(.platform.os == "linux" and .platform.architecture == $architecture) | .digest')"

{
  echo "CLANGUP_CONTAINER_IMAGE=${image}"
  echo "CLANGUP_BUILD_ENVIRONMENT_IDENTITY=${identity}#${platform_digest}"
  echo "CLANGUP_BOOTSTRAP_IDENTITY=${BOOTSTRAP_CHANNEL}@${BOOTSTRAP_EXACT}#${TARGET}:sha256:${artifact_sha256}"
} >> "${GITHUB_ENV}"
