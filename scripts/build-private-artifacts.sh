#!/usr/bin/env bash
# Build private, offline release archives. This script never invokes the CLI.
set -euo pipefail

readonly repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly go_bin="${GO_BIN:-go}"
readonly output_dir="${OUTPUT_DIR:-${repo_root}/dist/private}"
readonly targets="${TARGETS:-darwin/amd64 darwin/arm64 linux/amd64 linux/arm64}"

fail() {
  printf '%s\n' "error: $*" >&2
  exit 1
}

[[ -n "${VERSION:-}" ]] || fail 'VERSION must be set (for example, VERSION=0.3.0-private.1)'
[[ -n "${SOURCE_DATE_EPOCH:-}" ]] || fail 'SOURCE_DATE_EPOCH must be set (Unix seconds)'
[[ "$VERSION" =~ ^[0-9A-Za-z._+-]+$ ]] || fail 'VERSION may contain only letters, digits, dot, underscore, plus, and hyphen'
[[ "$SOURCE_DATE_EPOCH" =~ ^[0-9]+$ ]] || fail 'SOURCE_DATE_EPOCH must be a non-negative integer'
command -v "$go_bin" >/dev/null 2>&1 || fail "Go executable not found: $go_bin"

mkdir -p "$output_dir"
build_dir="$(mktemp -d "${TMPDIR:-/tmp}/grok-search-private-build.XXXXXX")"
trap 'rm -rf "$build_dir"' EXIT

for target in $targets; do
  IFS=/ read -r goos goarch extra <<<"$target"
  [[ -n "${goos:-}" && -n "${goarch:-}" && -z "${extra:-}" ]] || fail "invalid target: $target"
  case "$goos/$goarch" in
    darwin/amd64|darwin/arm64|linux/amd64|linux/arm64) ;;
    *) fail "unsupported private release target: $target" ;;
  esac

  binary="$build_dir/grok-search-$goos-$goarch"
  archive="$output_dir/grok-search_${VERSION}_${goos}_${goarch}.tar.gz"

  (
    cd "$repo_root"
    env \
      CGO_ENABLED=0 \
      GOOS="$goos" \
      GOARCH="$goarch" \
      GOFLAGS= \
      GOPROXY=off \
      GOSUMDB=off \
      GONOSUMDB='*' \
      GOTOOLCHAIN=local \
      "$go_bin" build \
        -trimpath \
        -buildvcs=false \
        -ldflags="-buildid= -s -w -X main.version=$VERSION" \
        -o "$binary" \
        ./cmd/grok-search

    env \
      GOFLAGS= \
      GOPROXY=off \
      GOSUMDB=off \
      GONOSUMDB='*' \
      GOTOOLCHAIN=local \
      "$go_bin" run ./scripts/release/private_archive.go \
        --input "$binary" \
        --output "$archive" \
        --version "$VERSION" \
        --goos "$goos" \
        --goarch "$goarch" \
        --source-date-epoch "$SOURCE_DATE_EPOCH"
  )

  printf 'built %s\n' "$archive"
done
