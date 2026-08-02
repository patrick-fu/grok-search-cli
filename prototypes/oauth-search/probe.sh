#!/usr/bin/env bash

# PROTOTYPE — throw away after answering this question:
# Can a standalone process use the current Grok Build OAuth access token,
# without launching Grok Build, to run isolated Web Search and X Search calls?

set -euo pipefail

readonly endpoint="https://cli-chat-proxy.grok.com/v1/responses"
readonly model="grok-4.5"
readonly grok_compat_version="0.2.117"

usage() {
  printf 'Usage: %s <web|x> [query]\n' "$0" >&2
  exit 2
}

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    printf 'Missing required command: %s\n' "$1" >&2
    exit 2
  fi
}

credential_fingerprint() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  elif command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    printf 'Neither shasum nor sha256sum is available\n' >&2
    exit 2
  fi
}

file_mode() {
  if stat -f '%Lp' "$1" >/dev/null 2>&1; then
    stat -f '%Lp' "$1"
  else
    stat -c '%a' "$1"
  fi
}

mode="${1:-}"
case "$mode" in
  web)
    tool_type="web_search"
    default_query='Find the current official xAI documentation page for X Search. Return its exact title and URL with a citation.'
    ;;
  x)
    tool_type="x_search"
    default_query='Search recent X posts from @SpaceXAI containing the exact phrase "Grok Build". Return one exact post URL, date, and post text.'
    ;;
  *)
    usage
    ;;
esac

query="${2:-$default_query}"

require_command curl
require_command jq
require_command awk
require_command stat

grok_home_dir="${GROK_HOME:-$HOME/.grok}"
auth_file="${GROK_AUTH_FILE:-$grok_home_dir/auth.json}"

if [[ ! -f "$auth_file" || -L "$auth_file" || ! -r "$auth_file" ]]; then
  printf 'Grok credential store must be a readable regular file; run: grok login\n' >&2
  exit 3
fi

auth_mode_bits="$(file_mode "$auth_file")"
if (( (8#$auth_mode_bits & 077) != 0 )); then
  printf 'Grok credential store permissions are too broad: expected owner-only access\n' >&2
  exit 3
fi

readonly oauth_records='[
  .[]
  | objects
  | select((.key? | type) == "string" and (.key | length) > 0)
  | select((.oidc_issuer? == "https://auth.x.ai") or (.auth_mode? == "oauth"))
]'

credential_count="$(jq -r "$oauth_records | length" "$auth_file")"
if [[ "$credential_count" != "1" ]]; then
  printf 'Expected exactly one Grok OAuth credential, found %s; refusing to guess\n' "$credential_count" >&2
  exit 3
fi

access_token="$(jq -er "$oauth_records | .[0].key" "$auth_file")"
auth_header_config="$(
  jq -nr --arg header "Authorization: Bearer $access_token" '$header | @json'
)"

auth_fingerprint_before="$(credential_fingerprint "$auth_file")"

prototype_tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/grok-oauth-probe.XXXXXX")"
trap 'rm -rf "$prototype_tmp_dir"' EXIT
request_file="$prototype_tmp_dir/request.json"
response_file="$prototype_tmp_dir/response.sse"
events_file="$prototype_tmp_dir/events.jsonl"

jq -n \
  --arg model "$model" \
  --arg query "$query" \
  --arg tool_type "$tool_type" \
  '{
    model: $model,
    input: [{
      role: "user",
      content: [{type: "input_text", text: $query}]
    }],
    tools: [{type: $tool_type}],
    tool_choice: "required",
    stream: true,
    store: false,
    max_output_tokens: 1024,
    reasoning: {effort: "low", summary: "concise"},
    include: ["reasoning.encrypted_content"]
  }' >"$request_file"

request_nonce="prototype-${mode}-$$-${RANDOM}"
curl_exit=0
http_status="$({
  printf 'header = %s\n' "$auth_header_config"
} | curl --config - \
  --silent \
  --show-error \
  --proto '=https' \
  --tlsv1.2 \
  --connect-timeout 10 \
  --max-time 120 \
  --max-redirs 0 \
  --request POST \
  --url "$endpoint" \
  --header 'Accept: text/event-stream' \
  --header 'Content-Type: application/json' \
  --header 'X-XAI-Token-Auth: xai-grok-cli' \
  --header 'x-authenticateresponse: authenticate-response' \
  --header 'x-grok-client-mode: headless' \
  --header 'x-grok-client-identifier: grok-search-cli-prototype' \
  --header "x-grok-client-version: $grok_compat_version" \
  --header "x-grok-model-override: $model" \
  --header "x-grok-conv-id: $request_nonce" \
  --header "x-grok-req-id: $request_nonce" \
  --header "x-grok-session-id: $request_nonce" \
  --header "x-grok-agent-id: $request_nonce" \
  --user-agent 'grok-search-cli-oauth-prototype/0.0.0' \
  --data-binary "@$request_file" \
  --output "$response_file" \
  --write-out '%{http_code}')" || curl_exit=$?
unset access_token auth_header_config

auth_fingerprint_after="$(credential_fingerprint "$auth_file")"
if [[ "$auth_fingerprint_before" != "$auth_fingerprint_after" ]]; then
  printf '{"ok":false,"error":"credential_store_changed"}\n' >&2
  exit 1
fi

if [[ "$curl_exit" != "0" ]]; then
  jq -n --arg mode "$mode" --argjson curl_exit "$curl_exit" \
    '{ok:false, mode:$mode, error:"transport_error", curl_exit:$curl_exit}' >&2
  exit 1
fi

if [[ ! "$http_status" =~ ^2[0-9][0-9]$ ]]; then
  jq -n --arg mode "$mode" --arg http_status "$http_status" \
    '{ok:false, mode:$mode, error:"upstream_rejected", http_status:$http_status}' >&2
  exit 1
fi

awk '
  /^data: / {
    payload = substr($0, 7)
    if (payload != "[DONE]") print payload
  }
' "$response_file" | jq -c 'select(type == "object")' >"$events_file"

web_event_count="$(jq -s '[.[] | select((.type? // "") | contains("web_search"))] | length' "$events_file")"
x_event_count="$(jq -s '[.[] | select(
  ((.item.type? // "") == "custom_tool_call" and ((.item.name? // "") | startswith("x_")))
  or ((.type? // "") | contains("custom_tool_call"))
)] | length' "$events_file")"
x_operations="$(jq -cs '[
  .[]
  | .item?
  | select((.type? // "") == "custom_tool_call")
  | .name?
  | select(startswith("x_"))
] | unique' "$events_file")"
answer="$(jq -rs '
  [.[] | select(.type? == "response.output_text.delta") | (.delta? // "")] as $deltas
  | if ($deltas | length) > 0 then
      $deltas | join("")
    else
      [.[] | .response?.output[]?.content[]? | select(.type? == "output_text") | (.text? // "")]
      | join("\n")
    end
' "$events_file")"
citations="$(jq -cs '[
  .[]
  | (.response?.citations[]? // empty),
    (.response?.output[]?.content[]?.annotations[]?.url? // empty)
] | unique' "$events_file")"

tool_observed=false
if [[ "$mode" == "web" && "$web_event_count" -gt 0 ]]; then
  tool_observed=true
elif [[ "$mode" == "x" && "$x_event_count" -gt 0 ]]; then
  tool_observed=true
fi

jq -n \
  --argjson ok "$tool_observed" \
  --arg mode "$mode" \
  --arg tool_type "$tool_type" \
  --arg http_status "$http_status" \
  --argjson web_event_count "$web_event_count" \
  --argjson x_event_count "$x_event_count" \
  --argjson x_operations "$x_operations" \
  --arg answer "$answer" \
  --argjson citations "$citations" \
  '{
    ok: $ok,
    mode: $mode,
    tool_type: $tool_type,
    http_status: $http_status,
    credential_store: "unchanged",
    evidence: {
      web_event_count: $web_event_count,
      x_event_count: $x_event_count,
      x_operations: $x_operations
    },
    answer: $answer,
    citations: $citations
  }'

if [[ "$tool_observed" != "true" ]]; then
  exit 1
fi
