# grok-search-cli

Agent-friendly CLI for Grok-powered web and X search.

> [!IMPORTANT]
> This is an unofficial, experimental project. It is not affiliated with or
> endorsed by xAI. It reuses a Grok Build login at the user's risk. xAI has
> not authorized this integration, and a technically successful request is
> **not** authorization. The user is responsible for whether their use
> complies with applicable xAI terms.

## Status

V1 is implemented. Source and GitHub Release binaries are public. Use remains
unofficial and at the user's risk. Do not operate the CLI as a shared service.
See the [V1 specification](docs/spec/v1.md).

## Install

V1 supports macOS and Linux on amd64 and arm64. The installer downloads the
matching GitHub Release archive, verifies its SHA-256 sidecar, and writes
`grok-search` to `${INSTALL_DIR:-$HOME/.local/bin}`:

```sh
curl -fsSL https://github.com/patrick-fu/grok-search-cli/releases/latest/download/install.sh | bash
```

Pin a release tag with `VERSION=<tag>`. Put `$HOME/.local/bin` on `PATH` if
needed. The installer does not edit shell rc files, use `sudo`, or read
credentials.

From source:

```sh
go build -o grok-search ./cmd/grok-search
```

## Update

```sh
grok-search update
```

`update` replaces the current binary with the latest GitHub Release for this
platform after the same checksum verification as the installer. It does not
refresh credentials or call xAI.

## Agent skill

This repository is the only source for the `grok-search` skill. Installing
the skill does not install the CLI:

```sh
npx skills add patrick-fu/grok-search-cli -g -s grok-search -a universal claude-code -y --full-depth
```

## Authentication

Users authenticate with the official Grok Build CLI:

```sh
grok login
```

The search CLI treats Grok Build's credential store
(`${GROK_HOME:-$HOME/.grok}/auth.json`) as **read-only** input. It never logs in,
refreshes, rotates, copies, or rewrites credentials, and it never starts a login
flow itself.

Missing, unreadable, insecure, malformed, locally expired, or provider-rejected
credentials produce typed errors (exit `3`). The error message directs the user
to run `grok login`. An **agent must request explicit permission before running
`grok login`** on a user's behalf; the search CLI only tells the user to do so.

## Provider compatibility

Before each search, the CLI reads the public Grok Build stable-version pointer
at `https://x.ai/cli/stable`. It uses that value in the compatibility header,
while retaining its own `grok-search-cli` identifier and User-Agent version.
It does not check the installed Grok Build version, run Grok Build, or impose a local
minimum version. A provider `426` remains `client_incompatible`.

Metadata reads are bounded by the search's total timeout and take at most 15
seconds each. No credential is sent to the public version endpoint. A failed
metadata lookup stops the request; it never triggers a pinned-version or
pinned-model fallback. Explicit `--model` skips model discovery, but still
needs the compatibility-version lookup.

Responses requests use the Grok Build token-auth headers, one user message,
and fresh session/conversation and request UUIDs. The conversation group is
derived from that session. These IDs remain unchanged for a permitted retry
and are never reused for a later search. An existing `$GROK_HOME/agent_id`
(default `~/.grok/agent_id`) is read without modification when it contains a
usable UUID; a missing or invalid cache simply omits that optional header.
The CLI never executes Grok Build or creates its machine identity cache.

The search profile keeps one required search tool and bounded output. It does
not send the coding agent's filesystem tools, telemetry uploads, compaction
state, or encrypted continuation state. Its client identifier and User-Agent
identify `grok-search-cli`.

## Usage

```text
grok-search web [common flags] [web flags] [query]
grok-search x   [common flags] [x flags]   [query]
grok-search help
grok-search version
grok-search update
```

### help, version, and update

`help`, `--help`, and `-h` print human-readable usage text and exit `0`.
`version` and `--version` print `grok-search <version>` and exit `0`.
`update` writes human-readable status and replaces the current binary; it is
not a search command and does not emit search JSON.

### Query input: stdin or positional

Prefer piping arbitrary queries through stdin to avoid shell-escaping hazards:

```sh
echo "Find the official xAI documentation." | grok-search web
```

A single positional argument also works:

```sh
grok-search web "Find the official xAI documentation."
```

Rules (verified against the implementation):

- If stdin is not a terminal, it is read first (up to 64 KiB).
- Non-empty stdin **and** a positional query together is `usage_error` (exit 2).
- Empty non-terminal stdin is treated as absent; the positional query is then used.
- With a terminal stdin, exactly one positional query is required.
- Invalid UTF-8, a query of only whitespace, or exceeding 64 KiB is `usage_error`.
- For stdin only, one trailing LF (and an optional preceding CR) is removed; all
  other bytes are preserved. The preserved query is what is sent upstream.

### web and x

`web` searches current public web information; `x` searches X posts. Each
invocation performs exactly one search and emits exactly one JSON object.

### Common options

| Option | Contract |
| --- | --- |
| `--auth-file <path>` | Read credentials from this path instead of `${GROK_HOME:-$HOME/.grok}/auth.json`; the same read-only checks apply. |
| `--timeout <duration>` | Total deadline; default `90s`, valid from `1s` through `180s`. |
| `--max-output-tokens <n>` | Provider output bound; default `1024`, valid from `128` through `4096`. |
| `--verbose` | Write sanitized, non-contractual diagnostics to stderr only. |

### --model

Without `--model`, the CLI fetches the provider catalog and selects its first
entry with `api_backend: responses` and `supports_backend_search: true`, in
provider order. It sends that entry's `model` value, or its `id` when `model` is
absent. No model name or numeric version is pinned locally. If the catalog
contains no usable search model, the command returns `search_model_unavailable`.

`--model <id>` skips automatic catalog selection. The model ID is treated as
opaque: valid UTF-8, 1–256 bytes, no control or whitespace characters. It is
sent once and never silently swapped; the CLI never falls back to another model.
The returned `model` is always the requested ID. An explicit empty value is
`usage_error`.

### Filters

Web filters (`--allow-domain`, `--exclude-domain`) and X filters
(`--allow-handle`, `--exclude-handle`) are mutually exclusive within each set.

| Surface | Flag | Limit |
| --- | --- | --- |
| web | repeatable `--allow-domain <domain>` / `--exclude-domain <domain>` | 5 unique values |
| x | repeatable `--allow-handle <handle>` / `--exclude-handle <handle>` | 20 unique values |
| x | `--from <YYYY-MM-DD>`, `--to <YYYY-MM-DD>` | `from` must not be later than `to` |

Domains are ASCII hostnames (no scheme, port, path, or wildcard), lowercased and
deduplicated. Handles omit `@` and contain no whitespace. Use filters only when
the user's request requires them.

## Output

Search commands write exactly one compact JSON object followed by a newline to
**stdout**, on both success and expected failure. Diagnostics go only to stderr.

### Success

```json
{
  "schema_version": 1,
  "type": "web",
  "query": "Find the official xAI documentation.",
  "model": "grok-4.7",
  "provider_model": "grok-4.7-build",
  "answer": "The documentation is available at the cited URL.",
  "sources": [
    {"url": "https://docs.x.ai/", "title": "xAI Documentation"}
  ],
  "warnings": []
}
```

- `schema_version`, `type`, `query`, `model`, `answer`, `sources`, and
  `warnings` are always present.
- `provider_model` appears only when the provider reported a completed-response
  model that differs from the requested `model`. That is disclosure, not a
  client-side fallback.
- `sources` are provider-supplied URL **citations** attached to the answer, not
  ranked search hits. They are deduplicated by exact URL in first-citation order;
  that order is not relevance rank. `title` is omitted when absent.
- Agents should consume `answer`, `sources`, and `warnings`, and treat sources as
  citations rather than ranked results.

### Warnings

`warnings` is always an array. A warning describes **degraded evidence in an
otherwise usable response** — not an error. V1 uses a closed set:

| Code | Meaning |
| --- | --- |
| `invalid_citations_ignored` | Some provider-supplied citations were in an unsupported shape and were ignored. The remaining answer is still valid. |
| `missing_citations` | The answer has no supporting citations. The response succeeds with `sources: []`. |

When both apply, they appear in the order shown. A response with no citations
left after ignoring invalid ones is a success with `sources: []` plus
`missing_citations`.

### Errors

Expected failures write a JSON error object and a non-zero exit status:

```json
{
  "schema_version": 1,
  "error": {
    "code": "rate_limited",
    "message": "The provider rate-limited the search request.",
    "retryable": true,
    "http_status": 429,
    "retry_after_seconds": 30,
    "request_id": "safe-request-id"
  }
}
```

`code` and `message` are stable and client-owned. `retryable` says whether a
caller *may* retry later; it does not mean the CLI already retried.
`http_status`, `retry_after_seconds`, and `request_id` appear only when reliably
observed and sanitized. The CLI never emits provider bodies or mutable provider
messages on stdout.

## Exit codes

| Exit | Meaning |
| --- | --- |
| `0` | Search success, `help`, `version`, or successful/no-op `update`. |
| `2` | Local usage or input-validation failure (`usage_error` for search commands). |
| `3` | Credential/authentication failure (missing, unreadable, insecure, invalid, expired, or rejected). |
| `4` | Transport, provider, compatibility, response, search, or update failure. |
| `70` | Sanitized unexpected internal failure. |

## Diagnostics (--verbose)

`--verbose` writes allowlisted, sanitized diagnostics to **stderr only**. It does
not change stdout and does not affect the exit code. The diagnostics are a
**non-contractual** debugging aid: a line carries only a phase name, elapsed
time, a stable code, and narrow evidence such as HTTP status.

They never contain the query, answer, citations, request or response bodies,
headers, paths containing a username, or any credential material. Do not parse
stderr as part of the contract; rely on the stdout JSON and the exit code.

## Verifying without network

Everything can be verified offline without calling the live xAI proxy. From a
clone:

```sh
go test ./...
go build -o grok-search ./cmd/grok-search
```

You can also exercise the local command surface with no credential and no
network. Point `--auth-file` at a missing path so the command fails closed at the
auth step regardless of whether a real `grok login` exists:

```sh
./grok-search help                                              # exit 0
./grok-search version                                           # exit 0
echo "x" | ./grok-search web --auth-file /tmp/no-such-cred      # exit 3 (auth_missing)
```

Each line is fully offline: `help` and `version` never touch auth or network,
and the `web` line fails at credential loading (exit `3`) before any request is
made. They contain no real credentials. `update` and the curl installer need
GitHub Release HTTPS and are not part of these offline checks. A search that
reaches the provider requires a valid `grok login` and is an explicit,
user-risk manual action — not part of these offline checks. See the
[explicit live smoke procedure](docs/manual-smoke.md).

## Security

Never commit or share `~/.grok/auth.json`, OAuth tokens, callback URLs, API
keys, or captured request headers. The CLI never prints, copies, or exposes
credentials. See [SECURITY.md](SECURITY.md).

## License

[MIT](LICENSE)
