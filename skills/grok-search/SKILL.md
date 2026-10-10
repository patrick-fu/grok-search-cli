---
name: grok-search
description: >-
  Search current public web pages or X posts by query through the local
  grok-search CLI (`web` for web, `x` for posts). Use when a query is needed to
  discover current public web information or X posts. Not for reading or
  summarizing a Web or X URL the user already has, and not for Grok coding (the
  separate `grok` CLI).
---

# grok-search

One search per invocation. `web` finds current public web information; `x` finds
X posts. This skill is query-based discovery — for reading or summarizing a Web
or X URL the user already has, reach a URL reader instead.

## Run the search

1. Confirm `grok-search` resolves on PATH. If it does not, stop and tell the
   user to install the CLI with:

   ```sh
   curl -fsSL https://github.com/patrick-fu/grok-search-cli/releases/latest/download/install.sh | bash
   ```

   Do not run the installer unless they ask. Installing this skill does not
   install the binary.

   Done when `grok-search` is on PATH, or the user has been given that install
   command and the search has stopped.

2. Pipe the query through stdin and run one command:

   ```sh
   printf '%s' "$query" | grok-search web
   printf '%s' "$query" | grok-search x
   ```

   A single positional argument is acceptable only for a simple, single-line
   query. Add filters only when the request needs them: web takes
   `--allow-domain` / `--exclude-domain` (mutually exclusive); x takes
   `--allow-handle` / `--exclude-handle` (mutually exclusive) and
   `--from` / `--to YYYY-MM-DD` where `from` is not later than `to`. An explicit
   `--model` always wins and is sent once, never silently swapped; without it,
   the CLI selects a search-capable model from the current provider catalog.
   For other shared controls, check `grok-search help`.

   Done when one command is issued with the query on stdin (or one clean
   positional) and only the filters the request needs.

3. Consume the single JSON object on stdout. Read `answer`, `sources`, and
   `warnings`:

   - `answer` — the normalized search answer.
   - `sources` — citations attached to the answer, deduplicated by URL in
     first-appearance order; that order is citation order, not a relevance rank.
   - `warnings` — degraded-evidence conditions (`invalid_citations_ignored`,
     `missing_citations`); the answer is still usable.

   Done when all three fields are read and sources are treated as citations.

## Handle failure

A non-zero search exit writes one JSON error object on stdout: `error.code`,
`error.message`, `error.retryable` (and optional `error.http_status`). Report the
typed `code` and the client-owned `message`; retry later only when `retryable`
is true. Exit codes: `0` success, `2` usage, `3` auth, `4` transport/provider,
`70` internal.

Auth errors are exit `3`: tell the user to run `grok login`, and do not run it
yourself unless they explicitly approve.

Done when the typed code is reported and no `grok login` ran without explicit
approval.

## Update

Only when the user asks to update the CLI, run `grok-search update`. Do not
check for updates during search.

Done when either no update was requested, or `grok-search update` ran once and
its human-readable status was reported.

## Boundary

Unofficial and not endorsed by xAI; the user accepts the risk. The CLI reuses a
Grok Build credential store read-only and never refreshes or exposes it. Search
only — no chat, proxy, MCP, or media generation. Do not operate it as a shared
service.
