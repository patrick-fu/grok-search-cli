#!/usr/bin/env bash
# Install a verified grok-search release without changing shell or credential state.
set -euo pipefail

readonly program_name='grok-search'
readonly default_release_base='https://github.com/patrick-fu/grok-search-cli/releases'
readonly release_base="${RELEASE_BASE:-$default_release_base}"
readonly requested_version="${VERSION:-}"
readonly install_dir="${INSTALL_DIR:-${HOME:?HOME must be set}/.local/bin}"
readonly curl_bin="${CURL_BIN:-curl}"

fail() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

need_command() {
  command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1"
}

download() {
  local url="$1"
  local output="$2"
  local max_bytes="$3"
  "$curl_bin" \
    --disable \
    --fail \
    --location \
    --proto '=https' \
    --proto-redir '=https' \
    --max-redirs 3 \
    --connect-timeout 15 \
    --max-time 180 \
    --max-filesize "$max_bytes" \
    --output "$output" \
    "$url" \
    || fail "download failed: $url"
}

sha256() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  elif command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    fail 'required command not found: shasum or sha256sum'
  fi
}

detect_target() {
  local os_raw arch_raw
  os_raw="${GROK_SEARCH_INSTALL_OS:-$(uname -s)}"
  arch_raw="${GROK_SEARCH_INSTALL_ARCH:-$(uname -m)}"
  case "$os_raw" in
    Darwin|darwin) target_os='darwin' ;;
    Linux|linux) target_os='linux' ;;
    *) fail "unsupported operating system: $os_raw" ;;
  esac
  case "$arch_raw" in
    x86_64|amd64) target_arch='amd64' ;;
    arm64|aarch64) target_arch='arm64' ;;
    *) fail "unsupported architecture: $arch_raw" ;;
  esac
}

manifest_version() {
  local manifest="$1" matches value
  matches="$(LC_ALL=C grep -Eo '"version"[[:space:]]*:[[:space:]]*"[0-9A-Za-z][0-9A-Za-z._+-]*"' "$manifest" || true)"
  [[ "$(printf '%s\n' "$matches" | sed '/^$/d' | wc -l | tr -d ' ')" == '1' ]] || fail 'release manifest must contain exactly one version'
  value="$(printf '%s\n' "$matches" | sed -E 's/.*"([0-9A-Za-z][0-9A-Za-z._+-]*)"$/\1/')"
  is_semver "$value" || fail 'release manifest contains an invalid version'
  printf '%s\n' "$value"
}

manifest_asset_digest() {
  local manifest="$1" archive_name="$2" compact matches digest
  compact="$(LC_ALL=C tr -d '[:space:]' <"$manifest")"
  [[ "$compact" == *'"schema_version":1'* ]] || fail 'release manifest has an unsupported schema version'
  matches="$(printf '%s' "$compact" | grep -Eo "\{\"name\":\"$archive_name\",\"sha256\":\"[0-9a-f]{64}\",\"os\":\"$target_os\",\"arch\":\"$target_arch\"\}" || true)"
  [[ "$(printf '%s\n' "$matches" | sed '/^$/d' | wc -l | tr -d ' ')" == '1' ]] \
    || fail 'release manifest must contain exactly one asset for this platform'
  digest="$(printf '%s' "$matches" | sed -E 's/.*"sha256":"([0-9a-f]{64})".*/\1/')"
  printf '%s\n' "$digest"
}

is_semver() {
  local numeric='(0|[1-9][0-9]*)'
  local prerelease='(0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*)'
  local build='[0-9A-Za-z-]+'
  [[ "$1" =~ ^$numeric\.$numeric\.$numeric(-$prerelease(\.$prerelease)*)?(\+$build(\.$build)*)?$ ]]
}

verify_checksum() {
  local archive="$1" checksum="$2" manifest_digest="$3" archive_name expected actual fields
  archive_name="$(basename "$archive")"
  fields="$(LC_ALL=C awk 'NF == 2 { print $1 " " $2 }' "$checksum")"
  [[ "$(printf '%s\n' "$fields" | sed '/^$/d' | wc -l | tr -d ' ')" == '1' ]] || fail 'checksum sidecar must contain exactly one SHA-256 entry'
  expected="${fields%% *}"
  [[ "$expected" =~ ^[0-9a-f]{64}$ ]] || fail 'checksum sidecar contains an invalid SHA-256 digest'
  [[ "${fields#* }" == "$archive_name" || "${fields#* }" == "*$archive_name" ]] || fail 'checksum sidecar does not name the downloaded archive'
  [[ "$expected" == "$manifest_digest" ]] || fail 'checksum sidecar does not match the release manifest'
  actual="$(sha256 "$archive")"
  [[ "$actual" == "$expected" ]] || fail 'archive SHA-256 does not match checksum sidecar'
}

verify_archive() {
  local archive="$1" expected_version="$2" metadata compact output
  local -a entries listing
  local entry line

  entries=()
  while IFS= read -r entry; do entries+=("$entry"); done < <(tar -tzf "$archive")
  [[ ${#entries[@]} -eq 2 && "${entries[0]}" == 'METADATA.json' && "${entries[1]}" == "$program_name" ]] \
    || fail 'archive entries must be exactly METADATA.json and grok-search'

  listing=()
  while IFS= read -r line; do listing+=("$line"); done < <(tar -tvzf "$archive")
  [[ ${#listing[@]} -eq 2 && "${listing[0]}" == -*' METADATA.json' && "${listing[1]}" == -*" $program_name" ]] \
    || fail 'archive entries must be regular files, not directories, links, or special files'

  tar -xzf "$archive" -C "$stage_dir" || fail 'archive extraction failed'
  [[ ! -L "$stage_dir/METADATA.json" && -f "$stage_dir/METADATA.json" ]] || fail 'archive metadata is not a regular file'
  [[ ! -L "$stage_dir/$program_name" && -f "$stage_dir/$program_name" && -x "$stage_dir/$program_name" ]] \
    || fail 'archive binary is missing, a link, or not executable'

  metadata="$(<"$stage_dir/METADATA.json")"
  compact="$(printf '%s' "$metadata" | LC_ALL=C tr -d '[:space:]')"
  [[ "$compact" == *"\"name\":\"$program_name\",\"version\":\"$expected_version\",\"target\":{\"os\":\"$target_os\",\"arch\":\"$target_arch\"}"* ]] \
    || fail 'archive metadata does not match the requested version and target'
  output="$("$stage_dir/$program_name" version 2>&1)" || fail 'downloaded binary failed its version check'
  [[ "$output" == "$program_name $expected_version" ]] || fail 'downloaded binary version does not match release metadata'
}

[[ "$release_base" == https://* ]] || fail 'RELEASE_BASE must use HTTPS'
if [[ -n "$requested_version" ]]; then
  requested_release_version="${requested_version#v}"
  is_semver "$requested_release_version" \
    || fail 'VERSION must be v?MAJOR.MINOR.PATCH with optional SemVer prerelease/build identifiers'
  requested_tag="v$requested_release_version"
fi
[[ "$install_dir" == /* ]] || fail 'INSTALL_DIR must be an absolute path'
need_command "$curl_bin"
need_command tar
need_command awk
detect_target

work_dir="$(mktemp -d "${TMPDIR:-/tmp}/grok-search-install.XXXXXX")"
stage_dir="$work_dir/stage"
mkdir -p "$stage_dir"
trap 'rm -rf "$work_dir"' EXIT

if [[ -n "$requested_version" ]]; then
  release_path="$release_base/download/$requested_tag"
else
  release_path="$release_base/latest/download"
fi
manifest="$work_dir/manifest.json"
download "$release_path/manifest.json" "$manifest" 1048576
manifest_release_version="$(manifest_version "$manifest")"
if [[ -n "$requested_version" && "$manifest_release_version" != "$requested_release_version" ]]; then
  fail 'release manifest version does not match VERSION'
fi
release_version="$manifest_release_version"

if [[ -z "$requested_version" ]]; then
  release_path="$release_base/download/v$release_version"
fi

archive_name="${program_name}_${release_version}_${target_os}_${target_arch}.tar.gz"
manifest_digest="$(manifest_asset_digest "$manifest" "$archive_name")"
archive="$work_dir/$archive_name"
checksum="$work_dir/$archive_name.sha256"
download "$release_path/$archive_name" "$archive" 134217728
download "$release_path/$archive_name.sha256" "$checksum" 4096
verify_checksum "$archive" "$checksum" "$manifest_digest"
verify_archive "$archive" "$release_version"

mkdir -p "$install_dir" || fail "could not create install directory: $install_dir"
target="$install_dir/$program_name"
[[ ! -L "$target" ]] || fail "refusing to replace symlink: $target"
[[ ! -e "$target" || -f "$target" ]] || fail "installation target is not a regular file: $target"
temporary="$(mktemp "$install_dir/.${program_name}.XXXXXX")" || fail 'could not create installation temporary file'
trap 'rm -f "$temporary"; rm -rf "$work_dir"' EXIT
cp "$stage_dir/$program_name" "$temporary" || fail 'could not stage verified binary'
chmod 755 "$temporary" || fail 'could not set binary permissions'
[[ ! -L "$target" ]] || fail "refusing to replace symlink: $target"
mv -f "$temporary" "$target" || fail "could not install $program_name"

printf 'Installed %s %s to %s\n' "$program_name" "$release_version" "$target"
