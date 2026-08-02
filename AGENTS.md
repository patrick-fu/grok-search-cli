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
- Distinguish missing, expired, unreadable, revoked, entitlement, and rate-limit
  failures with stable machine-readable errors.

## CLI contract

- stdout is reserved for the command result; diagnostics go to stderr.
- Agent-facing commands return versioned, stable JSON by default.
- Prefer stdin for arbitrary search queries to avoid shell-escaping hazards.
- Keep commands non-interactive except explicit authentication remediation.
- Bound timeouts, retries, output size, and upstream search work.

## Engineering

- Keep changes minimal and test observable behavior rather than implementation
  details.
- Never commit real credentials or credential-shaped fixtures.
- Preserve compatibility with macOS and Linux; do not preclude Windows.
