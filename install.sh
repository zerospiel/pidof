#!/usr/bin/env bash

set -euo pipefail

readonly REPO="zerospiel/pidof"
readonly BINARY_NAME="pidof"
readonly RELEASES_URL="https://github.com/${REPO}/releases"

say() {
  printf '==> %s\n' "$*"
}

warn() {
  printf 'warning: %s\n' "$*" >&2
}

die() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"
}

detect_os() {
  case "$(uname -s)" in
    Darwin)
      printf 'Darwin\n'
      ;;
    Linux)
      printf 'Linux\n'
      ;;
    *)
      die "unsupported OS: $(uname -s). Supported OSes are Darwin and Linux."
      ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64 | amd64)
      printf 'x86_64\n'
      ;;
    arm64 | aarch64)
      printf 'arm64\n'
      ;;
    *)
      die "unsupported architecture: $(uname -m). Supported architectures are amd64/x86_64 and arm64/aarch64."
      ;;
  esac
}

normalize_tag() {
  if [[ -z "${1:-}" ]]; then
    die "empty version"
  fi

  if [[ "$1" == v* ]]; then
    printf '%s\n' "$1"
    return
  fi

  printf 'v%s\n' "$1"
}

resolve_tag() {
  if [[ -n "${VERSION:-}" ]]; then
    normalize_tag "$VERSION"
    return
  fi

  local latest_url
  latest_url="$(curl -fsSL -o /dev/null -w '%{url_effective}' "${RELEASES_URL}/latest")"
  [[ -n "$latest_url" ]] || die "failed to resolve the latest release"

  normalize_tag "${latest_url##*/}"
}

default_install_dir() {
  if [[ -n "${INSTALL_DIR:-}" ]]; then
    printf '%s\n' "$INSTALL_DIR"
    return
  fi

  if command -v brew >/dev/null 2>&1; then
    local brew_prefix
    brew_prefix="$(brew --prefix 2>/dev/null || true)"
    if [[ -n "$brew_prefix" && -d "$brew_prefix/bin" && -w "$brew_prefix/bin" ]]; then
      printf '%s/bin\n' "$brew_prefix"
      return
    fi
  fi

  if [[ -d "/usr/local/bin" && -w "/usr/local/bin" ]]; then
    printf '/usr/local/bin\n'
    return
  fi

  printf '%s/.local/bin\n' "$HOME"
}

hash_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
    return
  fi

  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
    return
  fi

  die "required command not found: sha256sum or shasum"
}

main() {
  need_cmd curl
  need_cmd tar
  need_cmd install
  need_cmd awk
  need_cmd mktemp

  local os arch tag version asset archive_url checksums_url tmpdir archive_path checksums_path expected_sha actual_sha install_dir binary_path
  os="$(detect_os)"
  arch="$(detect_arch)"
  tag="$(resolve_tag)"
  version="${tag#v}"
  asset="${BINARY_NAME}_${version}_${os}_${arch}.tar.gz"
  archive_url="${RELEASES_URL}/download/${tag}/${asset}"
  checksums_url="${RELEASES_URL}/download/${tag}/checksums.txt"
  install_dir="$(default_install_dir)"

  tmpdir="$(mktemp -d)"
  trap "rm -rf '$tmpdir'" EXIT

  archive_path="${tmpdir}/${asset}"
  checksums_path="${tmpdir}/checksums.txt"

  say "Installing ${BINARY_NAME} ${tag}"
  say "Detected platform: ${os}/${arch}"
  say "Download URL: ${archive_url}"

  curl -fsSL "$archive_url" -o "$archive_path" || die "failed to download ${archive_url}"
  curl -fsSL "$checksums_url" -o "$checksums_path" || die "failed to download ${checksums_url}"

  expected_sha="$(awk -v asset="$asset" '$2 == asset { print $1 }' "$checksums_path")"
  [[ -n "$expected_sha" ]] || die "could not find checksum for ${asset}"

  actual_sha="$(hash_file "$archive_path")"
  [[ "$actual_sha" == "$expected_sha" ]] || die "checksum mismatch for ${asset}"

  tar -xzf "$archive_path" -C "$tmpdir"

  binary_path="${tmpdir}/${BINARY_NAME}"
  [[ -f "$binary_path" ]] || die "archive did not contain ${BINARY_NAME}"

  mkdir -p "$install_dir" || die "failed to create install directory: $install_dir"

  say "Installing to ${install_dir}"
  install -m 0755 "$binary_path" "${install_dir}/${BINARY_NAME}"

  if [[ ":$PATH:" != *":${install_dir}:"* ]]; then
    warn "${install_dir} is not on your PATH"
    printf "Add this to your shell profile:\n  export PATH=\"%s:$PATH\"\n" "$install_dir"
  fi

  say "Installed ${install_dir}/${BINARY_NAME}"
  say "Run '${BINARY_NAME} -v' to verify the installation"
}

main "$@"
