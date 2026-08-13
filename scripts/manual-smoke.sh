#!/usr/bin/env bash
# User-invoked compatibility smoke. It is intentionally absent from CI.
set -euo pipefail

usage() {
  printf '%s\n' 'usage: manual-smoke.sh --confirm-live-smoke [--binary <path>] [--auth-file <path>]'
}

binary="grok-search"
auth_file=""
confirmed=false
while (($#)); do
  case "$1" in
    --confirm-live-smoke) confirmed=true ;;
    --binary)
      shift
      (($#)) || { usage >&2; exit 2; }
      binary="$1"
      ;;
    --auth-file)
      shift
      (($#)) || { usage >&2; exit 2; }
      auth_file="$1"
      ;;
    --help|-h) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
  shift
done

if [[ "$confirmed" != true ]]; then
  printf '%s\n' 'refusing live smoke without --confirm-live-smoke' >&2
  exit 2
fi
if [[ -z "$auth_file" ]]; then
  auth_file="${GROK_HOME:-$HOME/.grok}/auth.json"
fi
[[ -f "$auth_file" ]] || { printf '%s\n' 'credential file does not exist' >&2; exit 2; }
command -v "$binary" >/dev/null 2>&1 || { printf '%s\n' 'grok-search binary was not found' >&2; exit 2; }
command -v python3 >/dev/null 2>&1 || { printf '%s\n' 'python3 is required for sanitized summaries' >&2; exit 2; }

hash_file() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    sha256sum "$1" | awk '{print $1}'
  fi
}

file_metadata() {
  if stat -f '%Sp:%z:%m:%i' "$1" >/dev/null 2>&1; then
    stat -f '%Sp:%z:%m:%i' "$1"
  else
    stat -c '%a:%s:%Y:%i' "$1"
  fi
}

before_hash="$(hash_file "$auth_file")"
before_metadata="$(file_metadata "$auth_file")"
work_dir="$(mktemp -d "${TMPDIR:-/tmp}/grok-search-manual-smoke.XXXXXX")"
trap 'rm -rf "$work_dir"' EXIT

summarize() {
  python3 - "$1" "$2" "$3" <<'PY'
import json
import sys

path, kind, exit_code = sys.argv[1:]
try:
    with open(path, encoding="utf-8") as handle:
        value = json.load(handle)
except Exception:
    print(f"{kind}: exit={exit_code} invalid-json")
    raise SystemExit(0)

if "error" in value:
    error = value["error"]
    print(f"{kind}: exit={exit_code} error={error.get('code', 'unknown')} retryable={error.get('retryable', False)}")
else:
    print(f"{kind}: exit={exit_code} sources={len(value.get('sources', []))} warnings={len(value.get('warnings', []))}")
PY
}

run_search() {
  local kind="$1"
  local query="$2"
  local result="$work_dir/$kind.json"
  local diagnostics="$work_dir/$kind.stderr"
  local status=0
  if printf '%s' "$query" | "$binary" "$kind" --auth-file "$auth_file" --timeout 30s --max-output-tokens 128 >"$result" 2>"$diagnostics"; then
    status=0
  else
    status=$?
  fi
  summarize "$result" "$kind" "$status"
  [[ "$status" -eq 0 ]]
}

failures=0
run_search web 'What is the official OpenAI website?' || failures=$((failures + 1))
run_search x 'Find one recent post from the OpenAI account.' || failures=$((failures + 1))

after_hash="$(hash_file "$auth_file")"
after_metadata="$(file_metadata "$auth_file")"
if [[ "$before_hash" != "$after_hash" || "$before_metadata" != "$after_metadata" ]]; then
  printf '%s\n' 'credential file changed during smoke; investigate before any further use' >&2
  exit 1
fi
if ((failures)); then
  printf '%s\n' 'one or more smoke requests failed; sanitized summaries are above' >&2
  exit 1
fi
printf '%s\n' 'manual smoke completed; credential bytes and metadata are unchanged'
