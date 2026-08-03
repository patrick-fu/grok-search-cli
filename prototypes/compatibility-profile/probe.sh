#!/usr/bin/env bash

# PROTOTYPE — answers which minimal, truthful request profile can run one
# isolated Grok Web Search or X Search through the fixed subscription proxy.

set -euo pipefail

readonly endpoint="https://cli-chat-proxy.grok.com/v1/responses"
readonly model="${GROK_MODEL:-grok-4.5}"
readonly client_identifier="grok-search-cli-prototype"
readonly client_version="0.0.0"
readonly grok_compat_base="${GROK_COMPAT_VERSION:-0.2.117}"
readonly qualified_compat_version="$grok_compat_base+$client_identifier.$client_version"

usage() {
  printf 'Usage: %s <minimal|actual-version|without-client-version|official-compatible-version|without-token-auth|without-authenticate-response> <web|x>\n' "$0" >&2
  exit 2
}

fingerprint() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    sha256sum "$1" | awk '{print $1}'
  fi
}

file_mode() {
  if stat -f '%Lp' "$1" >/dev/null 2>&1; then
    stat -f '%Lp' "$1"
  else
    stat -c '%a' "$1"
  fi
}

profile="${1:-}"
mode="${2:-}"
case "$profile" in
  minimal|actual-version|without-client-version|official-compatible-version|without-token-auth|without-authenticate-response) ;;
  *) usage ;;
esac

case "$mode" in
  web)
    tool_type="web_search"
    query='Find the official xAI documentation homepage and return its title and URL.'
    ;;
  x)
    tool_type="x_search"
    query='Find one recent public X post from @SpaceXAI that mentions Grok Build and return its URL.'
    ;;
  *) usage ;;
esac

for command_name in curl jq awk stat; do
  if ! command -v "$command_name" >/dev/null 2>&1; then
    printf 'Missing required command: %s\n' "$command_name" >&2
    exit 2
  fi
done

grok_home_dir="${GROK_HOME:-$HOME/.grok}"
auth_file="${GROK_AUTH_FILE:-$grok_home_dir/auth.json}"
if [[ ! -f "$auth_file" || -L "$auth_file" || ! -r "$auth_file" ]]; then
  printf 'Grok credential store must be a readable regular file; run: grok login\n' >&2
  exit 3
fi

mode_bits="$(file_mode "$auth_file")"
if (( (8#$mode_bits & 077) != 0 )); then
  printf 'Grok credential store permissions are too broad\n' >&2
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
  printf 'Expected exactly one Grok OAuth credential, found %s\n' "$credential_count" >&2
  exit 3
fi

access_token="$(jq -er "$oauth_records | .[0].key" "$auth_file")"
auth_header_config="$(jq -nr --arg value "Authorization: Bearer $access_token" '$value | @json')"
unset access_token

auth_fingerprint_before="$(fingerprint "$auth_file")"
prototype_tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/grok-compat-probe.XXXXXX")"
trap 'rm -rf "$prototype_tmp_dir"' EXIT
request_file="$prototype_tmp_dir/request.json"
response_headers_file="$prototype_tmp_dir/response.headers"
response_file="$prototype_tmp_dir/response.sse"
events_file="$prototype_tmp_dir/events.jsonl"

jq -n \
  --arg model "$model" \
  --arg query "$query" \
  --arg tool_type "$tool_type" \
  '{
    model: $model,
    input: $query,
    tools: [{type: $tool_type}],
    tool_choice: "required",
    stream: true,
    store: false,
    max_output_tokens: 512,
    reasoning: {effort: "low"}
  }' >"$request_file"

curl_args=(
  --silent
  --show-error
  --proto '=https'
  --tlsv1.2
  --connect-timeout 10
  --max-time 120
  --max-redirs 0
  --request POST
  --url "$endpoint"
  --header 'Accept: text/event-stream'
  --header 'Content-Type: application/json'
  --header 'x-grok-client-mode: headless'
  --header "x-grok-client-identifier: $client_identifier"
  --user-agent "$client_identifier/$client_version"
  --data-binary "@$request_file"
  --dump-header "$response_headers_file"
  --output "$response_file"
  --write-out '%{http_code}'
)

if [[ "$profile" == "actual-version" ]]; then
  curl_args+=(--header "x-grok-client-version: $client_version")
elif [[ "$profile" != "without-client-version" ]]; then
  if [[ "$profile" == "minimal" ]]; then
    curl_args+=(--header "x-grok-client-version: $qualified_compat_version")
  else
    curl_args+=(--header "x-grok-client-version: $grok_compat_base")
  fi
fi

if [[ "$profile" != "minimal" && "$profile" != "without-token-auth" ]]; then
  curl_args+=(--header 'X-XAI-Token-Auth: xai-grok-cli')
fi
if [[ "$profile" != "minimal" && "$profile" != "without-authenticate-response" ]]; then
  curl_args+=(--header 'x-authenticateresponse: authenticate-response')
fi

curl_exit=0
http_status="$({
  printf 'header = %s\n' "$auth_header_config"
} | curl --config - "${curl_args[@]}")" || curl_exit=$?
unset auth_header_config

auth_fingerprint_after="$(fingerprint "$auth_file")"
if [[ "$auth_fingerprint_before" != "$auth_fingerprint_after" ]]; then
  printf '{"ok":false,"error":"credential_store_changed"}\n' >&2
  exit 1
fi

content_type="$(awk 'BEGIN {IGNORECASE=1} /^content-type:/ {sub(/^[^:]+:[[:space:]]*/, ""); sub(/\r$/, ""); value=$0} END {print value}' "$response_headers_file")"

if [[ "$curl_exit" == "0" && "$http_status" =~ ^2[0-9][0-9]$ ]]; then
  awk '
    /^data: / {
      payload = substr($0, 7)
      sub(/\r$/, "", payload)
      if (payload != "[DONE]") print payload
    }
  ' "$response_file" | jq -ce 'select(type == "object")' >"$events_file"
else
  : >"$events_file"
fi

web_event_count="$(jq -s '[.[] | select((.type? // "") | contains("web_search"))] | length' "$events_file")"
x_event_count="$(jq -s '[.[] | select(
  ((.item.type? // "") == "custom_tool_call" and ((.item.name? // "") | startswith("x_")))
  or ((.type? // "") | contains("custom_tool_call"))
)] | length' "$events_file")"
completed_event_count="$(jq -s '[.[] | select(.type? == "response.completed")] | length' "$events_file")"
answer_length="$(jq -rs '[.[] | select(.type? == "response.output_text.delta") | (.delta? // "")] | join("") | length' "$events_file")"
citation_count="$(jq -s '[
  .[]
  | (.response?.citations[]? // empty),
    (.response?.output[]?.content[]?.annotations[]?.url? // empty)
] | unique | length' "$events_file")"

tool_observed=false
if [[ "$mode" == "web" && "$web_event_count" -gt 0 ]]; then
  tool_observed=true
elif [[ "$mode" == "x" && "$x_event_count" -gt 0 ]]; then
  tool_observed=true
fi

ok=false
if [[ "$curl_exit" == "0" \
  && "$http_status" =~ ^2[0-9][0-9]$ \
  && "$content_type" == text/event-stream* \
  && "$completed_event_count" -gt 0 \
  && "$tool_observed" == "true" \
  && "$answer_length" -gt 0 ]]; then
  ok=true
fi

jq -n \
  --argjson ok "$ok" \
  --arg profile "$profile" \
  --arg mode "$mode" \
  --arg http_status "$http_status" \
  --arg content_type "$content_type" \
  --argjson curl_exit "$curl_exit" \
  --argjson web_event_count "$web_event_count" \
  --argjson x_event_count "$x_event_count" \
  --argjson completed_event_count "$completed_event_count" \
  --argjson answer_length "$answer_length" \
  --argjson citation_count "$citation_count" \
  '{
    ok: $ok,
    profile: $profile,
    mode: $mode,
    http_status: $http_status,
    content_type: $content_type,
    curl_exit: $curl_exit,
    credential_store: "unchanged",
    evidence: {
      web_event_count: $web_event_count,
      x_event_count: $x_event_count,
      completed_event_count: $completed_event_count,
      answer_length: $answer_length,
      citation_count: $citation_count
    }
  }'

[[ "$ok" == "true" ]]
