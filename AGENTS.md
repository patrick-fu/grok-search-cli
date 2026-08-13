# Project instructions

## Product boundary

- Keep the first version focused on Grok-backed web and X search.
- The public surface is an agent-friendly CLI plus a small skill, not MCP.
- Do not add general chat, proxy, media generation, or credential-management
  features without an explicit product decision.

## Authentication

- Reuse the official Grok Build credential store as read-only input.
- Never refresh, rewrite, migrate, copy, or expose OAuth credentials in V1.
- Pin OAuth-authenticated requests to trusted xAI origins.
- Distinguish missing, expired, unreadable, rejected, permission, and rate-limit
  failures only when supported by local or protocol evidence. Never claim
  revocation or subscription entitlement from a generic provider response.

## CLI contract

- stdout is reserved for the command result; diagnostics go to stderr.
- Agent-facing commands return versioned, stable JSON by default.
- Prefer stdin for arbitrary search queries to avoid shell-escaping hazards.
- Keep commands non-interactive. Authentication remediation is message-only;
  the search CLI never invokes a login flow.
- Bound timeouts, retries, output size, and upstream search work.

## Engineering

- Keep changes minimal and test observable behavior rather than implementation
  details.
- Never commit real credentials or credential-shaped fixtures.
- Target macOS and Linux in V1. Do not introduce unnecessary barriers to future
  Windows support.

## Agent skills

### Issue tracker

Issues and PRDs are tracked in this repository's GitHub Issues. See `docs/agents/issue-tracker.md`.

Before any public issue, pull request, or comment operation, read
`docs/agents/issue-tracker.md` and apply its redaction gate. That document is
the single source of truth for public-data handling; do not duplicate its
rules in other agent instructions.

### Local skill source

`skills/grok-search/SKILL.md` is the unique source for this repository's
`grok-search` skill behavior. When a task invokes or changes that skill, read
that file; do not restate or fork its instructions elsewhere.

### Triage labels

Use the five default canonical triage labels. See `docs/agents/triage-labels.md`.

### Domain docs

Use the single-context domain documentation layout. See `docs/agents/domain.md`.
