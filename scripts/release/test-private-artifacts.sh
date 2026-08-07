#!/usr/bin/env bash
# Exercise a native private archive twice without contacting a provider or network.
set -euo pipefail

readonly repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly go_bin="${GO_BIN:-go}"
readonly version='0.0.0-private-test'
readonly epoch='1700000000'
readonly goos="$($go_bin env GOOS)"
readonly goarch="$($go_bin env GOARCH)"
readonly target="$goos/$goarch"

case "$target" in
  darwin/amd64|darwin/arm64|linux/amd64|linux/arm64) ;;
  *) printf 'error: native target %s is not a private release target\n' "$target" >&2; exit 1 ;;
esac

first_dir="$(mktemp -d "${TMPDIR:-/tmp}/grok-search-private-test-first.XXXXXX")"
second_dir="$(mktemp -d "${TMPDIR:-/tmp}/grok-search-private-test-second.XXXXXX")"
extract_dir="$(mktemp -d "${TMPDIR:-/tmp}/grok-search-private-test-extract.XXXXXX")"
trap 'rm -rf "$first_dir" "$second_dir" "$extract_dir"' EXIT

build_once() {
  local destination="$1"
  VERSION="$version" \
    SOURCE_DATE_EPOCH="$epoch" \
    TARGETS="$target" \
    OUTPUT_DIR="$destination" \
    GO_BIN="$go_bin" \
    "$repo_root/scripts/build-private-artifacts.sh" >/dev/null
}

build_once "$first_dir"
build_once "$second_dir"

archive_name="grok-search_${version}_${goos}_${goarch}.tar.gz"
first_archive="$first_dir/$archive_name"
second_archive="$second_dir/$archive_name"

cmp -s "$first_archive" "$second_archive"
cmp -s "$first_archive.sha256" "$second_archive.sha256"

(
  cd "$repo_root"
  env GOFLAGS= GOPROXY=off GOSUMDB=off GONOSUMDB='*' GOTOOLCHAIN=local \
    "$go_bin" run ./scripts/release/private_archive.go \
      --verify "$first_archive" \
      --checksum "$first_archive.sha256" \
      --version "$version" \
      --goos "$goos" \
      --goarch "$goarch" \
      --source-date-epoch "$epoch"
)

tar -xzf "$first_archive" -C "$extract_dir"
[[ "$("$extract_dir/grok-search" version)" == "grok-search $version" ]]

printf 'private artifact test passed for %s\n' "$target"
