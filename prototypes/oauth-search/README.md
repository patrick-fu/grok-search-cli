# OAuth search probe

> **PROTOTYPE — not production code.**

## Question

Can a standalone process read the current Grok Build OAuth access token and,
without launching Grok Build, run isolated Web Search and X Search requests?

This probe sends one benign request directly to the fixed Grok Build subscription
proxy. It never logs in, refreshes credentials, follows redirects, or writes the
credential store. It verifies the credential file fingerprint after the request
and reports only a sanitized result summary.

The proxy and credential format are not documented as stable third-party APIs.
Technical success does not imply xAI authorization, compatibility guarantees,
or permission to redistribute this integration.

## Run

```bash
./prototypes/oauth-search/probe.sh web
./prototypes/oauth-search/probe.sh x
```

Pass a custom benign query as the second argument when needed:

```bash
./prototypes/oauth-search/probe.sh x 'Find one recent post from @SpaceXAI about Grok Build.'
```

Requirements: Bash, `curl`, `jq`, `awk`, and an existing unexpired Grok Build
OAuth login in `~/.grok/auth.json`.

## Result

Validated on 2026-08-03 with Grok Build OAuth credentials created by version
0.2.117:

- `web` returned HTTP 200, emitted Web Search lifecycle events, and cited the
  official xAI X Search documentation.
- `x` returned HTTP 200, emitted only X Search lifecycle events, executed
  `x_keyword_search`, and cited a concrete X post.
- X's public oEmbed endpoint independently confirmed that post's author, text,
  URL, and date.
- The credential store fingerprint was unchanged after both requests.

This proves current technical feasibility for this account. It does not turn
the OAuth proxy, credential schema, or compatibility headers into a supported
third-party API contract.
