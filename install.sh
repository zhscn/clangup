#!/bin/sh
set -eu

repository="zhscn/clangup"
bin_dir="${CLANGUP_BIN_DIR:-${HOME:+$HOME/.local/bin}}"

usage() {
  cat <<'EOF'
Install the latest clangup and cmk release for the current platform.

Usage:
  install.sh [--bin-dir DIR]

Environment:
  CLANGUP_BIN_DIR  Default installation directory (default: ~/.local/bin)
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --bin-dir)
      [ "$#" -ge 2 ] || { echo "install.sh: --bin-dir requires a value" >&2; exit 2; }
      bin_dir=$2
      shift 2
      ;;
    --bin-dir=*)
      bin_dir=${1#*=}
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "install.sh: unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

[ -n "$bin_dir" ] || {
  echo "install.sh: HOME is unset; use --bin-dir DIR" >&2
  exit 2
}
command -v curl >/dev/null 2>&1 || {
  echo "install.sh: curl is required" >&2
  exit 1
}

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) echo "install.sh: unsupported operating system: $(uname -s)" >&2; exit 1 ;;
esac

case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "install.sh: unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

if [ "$os" = darwin ] && [ "$arch" != arm64 ]; then
  echo "install.sh: macOS releases are available only for Apple Silicon" >&2
  exit 1
fi

tmp_dir=$(mktemp -d "${TMPDIR:-/tmp}/clangup-install.XXXXXX")
trap 'rm -rf "$tmp_dir"' EXIT HUP INT TERM

release_root="https://github.com/${repository}/releases/latest/download"
curl --proto '=https' --tlsv1.2 --fail --location --retry 3 \
  --silent --show-error --output "${tmp_dir}/SHA256SUMS" \
  "${release_root}/SHA256SUMS"

verify() {
  asset=$1
  checksum=$(grep "  ${asset}$" "${tmp_dir}/SHA256SUMS" || true)
  [ -n "$checksum" ] || {
    echo "install.sh: SHA256SUMS has no entry for ${asset}" >&2
    exit 1
  }
  printf '%s\n' "$checksum" > "${tmp_dir}/${asset}.sha256"
  if command -v sha256sum >/dev/null 2>&1; then
    (cd "$tmp_dir" && sha256sum -c "${asset}.sha256") >/dev/null
  elif command -v shasum >/dev/null 2>&1; then
    (cd "$tmp_dir" && shasum -a 256 -c "${asset}.sha256") >/dev/null
  else
    echo "install.sh: sha256sum or shasum is required" >&2
    exit 1
  fi
}

download() {
  tool=$1
  asset="${tool}-${os}-${arch}"
  echo "Downloading ${asset}..."
  curl --proto '=https' --tlsv1.2 --fail --location --retry 3 \
    --silent --show-error --output "${tmp_dir}/${asset}" \
    "${release_root}/${asset}"
  verify "$asset"
  chmod 0755 "${tmp_dir}/${asset}"
}

download clangup
download cmk

if [ "$os" = darwin ] && command -v xattr >/dev/null 2>&1; then
  xattr -d com.apple.quarantine "${tmp_dir}/clangup-${os}-${arch}" 2>/dev/null || true
  xattr -d com.apple.quarantine "${tmp_dir}/cmk-${os}-${arch}" 2>/dev/null || true
fi

"${tmp_dir}/clangup-${os}-${arch}" version >/dev/null
"${tmp_dir}/cmk-${os}-${arch}" version >/dev/null

mkdir -p "$bin_dir"
for tool in clangup cmk; do
  install -m 0755 "${tmp_dir}/${tool}-${os}-${arch}" "${bin_dir}/${tool}"
done

if [ "$os" = darwin ] && command -v xattr >/dev/null 2>&1; then
  xattr -d com.apple.quarantine "${bin_dir}/clangup" 2>/dev/null || true
  xattr -d com.apple.quarantine "${bin_dir}/cmk" 2>/dev/null || true
fi

echo "Installed $("${bin_dir}/clangup" version) and $("${bin_dir}/cmk" version) in ${bin_dir}"
case ":${PATH:-}:" in
  *:"$bin_dir":*) ;;
  *) echo "Add ${bin_dir} to PATH to use clangup and cmk." ;;
esac
