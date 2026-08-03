# Compatibility-profile probe

> **PROTOTYPE — not production code.**

## Question

What is the smallest fail-closed endpoint, header, request, and response profile
that can run isolated `web_search` and `x_search` with the existing Grok Build
OAuth credential while truthfully identifying this standalone client?

The minimal profile does not send a bare Grok Build version, model-override
header, proxy-auth marker headers, or synthetic Grok conversation/request/
session/agent IDs. It sends this prototype's own identifier and version, marks
the client as headless, never refreshes credentials, and verifies that
`auth.json` is unchanged.

## Run

```bash
./prototypes/compatibility-profile/probe.sh actual-version web
./prototypes/compatibility-profile/probe.sh without-client-version web
./prototypes/compatibility-profile/probe.sh official-compatible-version web
./prototypes/compatibility-profile/probe.sh without-token-auth web
./prototypes/compatibility-profile/probe.sh without-authenticate-response web
./prototypes/compatibility-profile/probe.sh minimal web
./prototypes/compatibility-profile/probe.sh minimal x
```

The first three profiles determine how the proxy gates client compatibility.
The auth-header controls show whether the two Grok-specific routing markers are
required. A request succeeds only when it returns an SSE response, a
`response.completed` event, the requested search-tool event, and non-empty
output text. Raw response bodies, queries, credentials, and answers are not
printed.

## Observed result

Validated on 2026-08-04 with the OAuth credential created by Grok Build
`0.2.117`:

| Profile | Web | X | Observation |
| --- | --- | --- | --- |
| actual product version `0.0.0` | `426` | not run | Proxy rejects a version below its compatibility floor. |
| no `x-grok-client-version` | `426` | not run | The version header is mandatory. |
| bare compatible version `0.2.117` | `200` | `200` | Both searches emitted their requested tool event and completed. |
| omit either proxy-auth marker | `200` | not run | Neither marker is individually required. |
| omit both markers | `200` | `200` | Both markers can be removed. |
| qualified `0.2.117+grok-search-cli-prototype.0.0.0` | `200` | `200` | SemVer build metadata preserves the gate and identifies this client. |

A parenthetical value such as `0.2.117 (grok-search-cli-prototype/0.0.0)` was
rejected with `426`; the accepted qualified form is SemVer build metadata.

## Recommended V1 profile

- Exact `POST https://cli-chat-proxy.grok.com/v1/responses`; HTTPS only, no
  redirects or configurable origin.
- Headers: Bearer authorization, JSON content type, SSE accept type,
  `x-grok-client-mode: headless`, `x-grok-client-identifier: grok-search-cli`,
  a truthful `User-Agent`, and a compile-time compatibility value shaped as
  `<grok-floor>+grok-search-cli.<cli-version>` in `x-grok-client-version`.
- Body: `model`, string `input`, exactly one search tool, `tool_choice:
  "required"`, `stream: true`, `store: false`, a bounded `max_output_tokens`,
  and low reasoning effort.
- Do not send `X-XAI-Token-Auth`, `x-authenticateresponse`,
  `x-grok-model-override`, synthetic Grok IDs, reasoning summaries, or encrypted
  continuation state.
- Accept only a 2xx SSE response that parses within the local byte limit,
  reaches `response.completed`, contains the requested search-tool event, and
  returns output text. Missing citations remain a successful warning.
- Treat `426` as incompatible client/proxy protocol and instruct the user to
  upgrade this CLI. Do not discover or borrow a new version by invoking Grok
  Build at request time.

Technical success is account- and date-specific. It is not evidence of xAI
authorization or a stable third-party contract. The repository owner has chosen
to continue private use at their own risk; public distribution remains blocked.
