#!/usr/bin/env bash
# Offline installer tests. A fake curl serves local fixtures but requires HTTPS URLs.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly repo_root
readonly installer="$repo_root/install.sh"
readonly version='1.2.3-installer-test'

fail() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

sha256() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    sha256sum "$1" | awk '{print $1}'
  fi
}

case "$(uname -s)/$(uname -m)" in
  Darwin/x86_64|Darwin/amd64) os='darwin'; arch='amd64' ;;
  Darwin/arm64) os='darwin'; arch='arm64' ;;
  Linux/x86_64|Linux/amd64) os='linux'; arch='amd64' ;;
  Linux/aarch64|Linux/arm64) os='linux'; arch='arm64' ;;
  *) fail "unsupported native test target: $(uname -s)/$(uname -m)" ;;
esac

test_dir="$(mktemp -d "${TMPDIR:-/tmp}/grok-search-install-test.XXXXXX")"
fixture_dir="$test_dir/fixture"
install_dir="$test_dir/bin"
mkdir -p "$fixture_dir/archive"
trap 'rm -rf "$test_dir"' EXIT

archive_name="grok-search_${version}_${os}_${arch}.tar.gz"
cat >"$fixture_dir/archive/METADATA.json" <<EOF
{
  "name": "grok-search",
  "version": "$version",
  "target": {"os": "$os", "arch": "$arch"}
}
EOF
cat >"$fixture_dir/archive/grok-search" <<EOF
#!/usr/bin/env sh
if [ "\${1:-}" = version ]; then
  printf 'grok-search %s\\n' '$version'
fi
EOF
chmod 755 "$fixture_dir/archive/grok-search"
(cd "$fixture_dir/archive" && tar -czf "$fixture_dir/$archive_name" METADATA.json grok-search)
printf '%s  %s\n' "$(sha256 "$fixture_dir/$archive_name")" "$archive_name" >"$fixture_dir/$archive_name.sha256"
archive_digest="$(sha256 "$fixture_dir/$archive_name")"
printf '{"schema_version":1,"version":"%s","assets":[{"name":"%s","sha256":"%s","os":"%s","arch":"%s"}]}\n' \
  "$version" "$archive_name" "$archive_digest" "$os" "$arch" >"$fixture_dir/manifest.json"

fake_curl="$test_dir/fake-curl"
cat >"$fake_curl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
output=''
url=''
while (($#)); do
  case "$1" in
    --output) output="$2"; shift 2 ;;
    --proto|--proto-redir|--max-redirs|--connect-timeout|--max-time|--max-filesize) shift 2 ;;
    --disable|--fail|--location) shift ;;
    https://*) url="$1"; shift ;;
    *) exit 91 ;;
  esac
done
[[ -n "$output" && "$url" == https://* ]] || exit 92
case "$url" in
  */manifest.json) cp "$FIXTURE_DIR/manifest.json" "$output" ;;
  *.sha256)
    if [[ "${TAMPER_CHECKSUM:-}" == 1 ]]; then printf '%064d  %s\n' 0 "${url##*/}" >"$output"
    else cp "$FIXTURE_DIR/${url##*/}" "$output"; fi ;;
  *.tar.gz) cp "$FIXTURE_DIR/${url##*/}" "$output" ;;
  *) exit 93 ;;
esac
[[ -z "${URL_LOG:-}" ]] || printf '%s\n' "$url" >>"$URL_LOG"
EOF
chmod 755 "$fake_curl"

run_install() {
  INSTALL_DIR="$install_dir" \
    RELEASE_BASE='https://fixture.invalid/releases' \
    CURL_BIN="$fake_curl" \
    FIXTURE_DIR="$fixture_dir" \
    URL_LOG="${url_log:-}" \
    bash "$installer"
}

success_output="$(run_install)"
[[ "$success_output" == "Installed grok-search $version to $install_dir/grok-search" ]]
[[ "$("$install_dir/grok-search" version)" == "grok-search $version" ]]

latest_log="$test_dir/latest-urls"
url_log="$latest_log" run_install >/dev/null
[[ "$(rg -xc 'https://fixture.invalid/releases/latest/download/manifest.json' "$latest_log")" == 1 ]]
[[ "$(rg -xc "https://fixture.invalid/releases/download/v$version/grok-search_${version}_${os}_${arch}.tar.gz" "$latest_log")" == 1 ]]

url_log="$test_dir/urls"
VERSION="$version" run_install >/dev/null
VERSION="v$version" run_install >/dev/null
[[ "$(rg -xc "https://fixture.invalid/releases/download/v$version/manifest.json" "$url_log")" == 2 ]]
[[ "$(rg -xc "https://fixture.invalid/releases/download/v$version/grok-search_${version}_${os}_${arch}.tar.gz" "$url_log")" == 2 ]]
if VERSION=latest run_install >/dev/null 2>&1; then
  fail 'non-SemVer VERSION unexpectedly installed'
fi

if GROK_SEARCH_INSTALL_OS=windows run_install >/dev/null 2>&1; then
  fail 'unsupported platform unexpectedly installed'
fi

cat >"$install_dir/grok-search" <<'EOF'
#!/usr/bin/env sh
printf 'old binary\n'
EOF
chmod 755 "$install_dir/grok-search"
if TAMPER_CHECKSUM=1 run_install >/dev/null 2>&1; then
  fail 'tampered checksum unexpectedly installed'
fi
[[ "$("$install_dir/grok-search")" == 'old binary' ]]

rm "$install_dir/grok-search"
ln -s "$test_dir/elsewhere" "$install_dir/grok-search"
if run_install >/dev/null 2>&1; then
  fail 'symlink target unexpectedly replaced'
fi
[[ -L "$install_dir/grok-search" ]]

printf 'installer tests passed for %s/%s\n' "$os" "$arch"
