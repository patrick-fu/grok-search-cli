---
name: grok-search
description: Search current public web pages or X posts through the local grok-search CLI. Use when the user needs current public web information or X posts.
---

# grok-search

One search per invocation. Use `web` for current public web information and `x` for X posts:

```sh
printf '%s' "$query" | grok-search web
printf '%s' "$query" | grok-search x
```

Prefer stdin: it avoids shell-escaping hazards. A positional argument is acceptable only for a simple, single-line query:

```sh
grok-search web "current date"
```

Stdout carries exactly one JSON object. Read it and consume `answer`, `sources`, and `warnings`:

- `answer` — the normalized search answer.
- `sources` — URLs cited by the final answer in first-appearance order. That order is citation order, not a relevance ranking.
- `warnings` — degraded-evidence conditions (`invalid_citations_ignored`, `missing_citations`); the answer is still usable.

Use options only when the user's request requires them:

- `--model <id>` — override the default `grok-4.5` when the user asks for a specific model.
- Web: `--allow-domain` / `--exclude-domain` (mutually exclusive).
- X: `--allow-handle` / `--exclude-handle` (mutually exclusive), `--from` / `--to YYYY-MM-DD`.
- `--auth-file <path>` selects an existing read-only credential store; `--timeout` and
  `--max-output-tokens` are bounded execution controls. Use `--verbose` only for
  sanitized stderr diagnostics; never parse it as output.

On failure stdout is one JSON error object: `error.code`, `error.message`, `error.retryable`, and optional `error.http_status`. Report the typed code and client-owned message; do not invent provider reasons. Retry later only when `retryable` is true.

Exit codes: 0 success, 2 usage, 3 auth, 4 transport/provider, 70 internal.

For auth errors (exit 3), tell the user to run `grok login`; do not run it yourself unless the user explicitly approves.

Private, repository-owner use at the owner's risk. xAI has not authorized this project; do not publicly distribute it or operate it as a shared service. No credentials, OAuth, proxy, MCP, or chat features.
