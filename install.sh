#!/bin/sh
set -eu

repo="${REPO:-SomeoneWithOptions/slack-utils}"
bin_name="${BIN_NAME:-slack-utils}"
install_dir="${INSTALL_DIR:-/usr/local/bin}"
version="${VERSION:-latest}"

say() {
  printf '%s\n' "$*" >&2
}

fail() {
  say "error: $*"
  exit 1
}

download() {
  url="$1"
  dest="$2"

  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$url" -o "$dest"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "$dest" "$url"
  else
    fail "curl or wget is required"
  fi
}

sha256_file() {
  file="$1"

  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$file" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$file" | awk '{print $1}'
  else
    fail "sha256sum or shasum is required"
  fi
}

os_name="$(uname -s 2>/dev/null || true)"
case "$os_name" in
  Darwin*) os="darwin" ;;
  Linux*) os="linux" ;;
  *) fail "unsupported OS: ${os_name:-unknown}. Use install.ps1 on Windows." ;;
esac

machine="$(uname -m 2>/dev/null || true)"
case "$machine" in
  x86_64|amd64) arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *) fail "unsupported architecture: ${machine:-unknown}" ;;
esac

archive="${bin_name}_${os}_${arch}.tar.gz"
if [ "$version" = "latest" ]; then
  base_url="https://github.com/${repo}/releases/latest/download"
else
  base_url="https://github.com/${repo}/releases/download/${version}"
fi

tmpdir="${TMPDIR:-/tmp}/${bin_name}-install.$$"
if ! (umask 077 && mkdir "$tmpdir") 2>/dev/null; then
  tmpdir="$(mktemp -d "${TMPDIR:-/tmp}/${bin_name}-install.XXXXXX")" || fail "could not create temporary directory"
fi
trap 'rm -rf "$tmpdir"' 0 HUP INT TERM

archive_path="${tmpdir}/${archive}"
checksums_path="${tmpdir}/checksums.txt"

say "Downloading ${archive}..."
download "${base_url}/${archive}" "$archive_path"
download "${base_url}/checksums.txt" "$checksums_path"

expected="$(awk -v file="$archive" '$2 == file { print $1 }' "$checksums_path")"
[ -n "$expected" ] || fail "checksum for ${archive} not found"

actual="$(sha256_file "$archive_path")"
if [ "$actual" != "$expected" ]; then
  fail "checksum mismatch for ${archive}"
fi

say "Installing ${bin_name} to ${install_dir}..."
tar -xzf "$archive_path" -C "$tmpdir"
[ -f "${tmpdir}/${bin_name}" ] || fail "archive did not contain ${bin_name}"
chmod 755 "${tmpdir}/${bin_name}"

needs_sudo="no"
if [ "$(id -u)" -ne 0 ]; then
  if [ -d "$install_dir" ]; then
    [ -w "$install_dir" ] || needs_sudo="yes"
  else
    parent_dir="$(dirname "$install_dir")"
    [ -w "$parent_dir" ] || needs_sudo="yes"
  fi
fi

if [ "$needs_sudo" = "yes" ]; then
  command -v sudo >/dev/null 2>&1 || fail "${install_dir} is not writable and sudo is not available; set INSTALL_DIR to a writable directory"
  sudo mkdir -p "$install_dir"
  sudo cp "${tmpdir}/${bin_name}" "${install_dir}/${bin_name}"
  sudo chmod 755 "${install_dir}/${bin_name}"
else
  mkdir -p "$install_dir"
  cp "${tmpdir}/${bin_name}" "${install_dir}/${bin_name}"
  chmod 755 "${install_dir}/${bin_name}"
fi

case ":${PATH}:" in
  *":${install_dir}:"*) ;;
  *) say "Warning: ${install_dir} is not in PATH." ;;
esac

say "Installed ${bin_name} at ${install_dir}/${bin_name}"
