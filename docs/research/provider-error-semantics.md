# Provider error semantics

- Research ticket: [#4, Characterize provider error semantics](https://github.com/patrick-fu/grok-search-cli/issues/4)
- Accessed: 2026-08-03
- Official source snapshot: [`xai-org/grok-build@780d1388fff103ff0db0d8c14de65af6225b4860`](https://github.com/xai-org/grok-build/tree/780d1388fff103ff0db0d8c14de65af6225b4860)

## Decision

The V1 CLI can assign stable machine errors from three evidence classes without returning raw provider bodies:

1. local credential-store state (`missing`, explicitly expired);
2. protocol metadata (HTTP status and selected headers); and
3. client-owned observations (deadline exceeded, output byte budget exceeded, typed response parsing failed).

Provider message text is not a stable classifier. First-party evidence does **not** support reliably distinguishing an expired, reused, or revoked refresh token; an entitlement denial from other `403` policy denials; or an unknown model from a bad endpoint using status alone. V1 must therefore use broader errors for those cases.

## Evidence matrix

| Requested failure | Stable observable evidence | Recommended V1 error | Stable distinction? | Important non-evidence |
| --- | --- | --- | --- | --- |
| Missing credential | `auth.json` is absent, or it is readable but the expected scope entry is absent | `auth_missing` with a local remediation | **Yes, local** | A provider `401` does not prove the local store was missing; it may mean an invalid token was sent. |
| Expired access token | Parsed `expires_at <= now`; when absent, Grok Build applies its documented `create_time + 30 days` fallback | `auth_expired` | **Yes as Grok Build local policy** | A `401` does not prove expiry. Clock-based fallback is a client policy, not a provider revocation verdict. |
| Rejected access token | A request sent an Authorization credential and the trusted xAI origin returned `401` | `auth_rejected` | **Yes** | Do not relabel this as `expired` or `revoked`. A `401` without a sent credential is `auth_missing`, not rejection. |
| Revoked refresh token | OAuth refresh returns `invalid_grant` | No V1 `auth_revoked`; if ever observed by an authorized refresher, use `auth_refresh_rejected` | **No** | xAI's source says `invalid_grant` combines expired, reused, and revoked. V1 is read-only and must not perform refresh, so it cannot observe this signal itself. |
| Entitlement / subscription | No documented dedicated status or machine code. `403` is the broad permission-denied signal | `permission_denied` | **No** | Do not classify generic `403` as entitlement. It also covers blocked keys/teams, content safety, ZDR, and remote policy. Subscription message matching is diagnostic-only. |
| Rate limit | HTTP `429`; optionally capture a parsed integer `Retry-After` value | `rate_limited` with bounded `retry_after_seconds` | **Yes** | Do not fold `503`, `529`, or message text such as “try later” into rate limiting. Those are capacity/upstream failures. |
| Unsupported / unavailable model | Preferred: requested model absent from a successfully fetched, credential-scoped `/v1/models` catalog. Conditional fallback: `404` from a compile-time-known valid inference route with a model in the request | `unsupported_model` only with catalog/known-route evidence; otherwise `not_found` | **Conditional** | Public xAI docs say `404` also means an invalid endpoint URL. A bare `404` is insufficient. Catalog fetch failure or an empty catalog is also insufficient. |
| Timeout | A client-owned connect/request/idle deadline elapsed; record stage and elapsed time | `timeout` with `stage` | **Yes, local** | `502`–`504` and `520`–`524` are upstream/edge statuses, not proof that this client's own deadline elapsed. They should remain upstream errors. |
| Oversize | Request: HTTP `413`. Output: the CLI's own byte counter crosses its configured cap. Provider truncation: a typed `max_tokens` / length stop reason | `request_too_large`, `output_too_large`, or `output_truncated` respectively | **Yes when based on these exact signals** | A reset/broken pipe is only a heuristic for an oversized upload. Context-window overflow has no stable code and Grok Build falls back to message matching. Do not conflate request bytes, context tokens, output bytes, and token truncation. |
| Upstream incompatible | A success response or SSE data event cannot be decoded as any supported, typed xAI/OpenAI envelope after known xAI extensions are handled | `upstream_incompatible` | **Yes as a client/protocol observation** | This does not prove the provider is at fault; it can also expose client/server version skew. A transport-interrupted SSE stream is a transport error, not schema incompatibility. |

## First-party evidence

### Credential state and rejection

Grok Build documents `~/.grok/auth.json`, background refresh, re-login when refresh fails, and a 30-day fallback lifetime when the server supplies no expiry in its [authentication guide, lines 7–23](https://github.com/xai-org/grok-build/blob/780d1388fff103ff0db0d8c14de65af6225b4860/crates/codegen/xai-grok-pager/docs/user-guide/02-authentication.md#L7-L23). Its credential schema stores `create_time` and optional `expires_at`; `expires_at` takes precedence over the fallback ([source lines 46–107](https://github.com/xai-org/grok-build/blob/780d1388fff103ff0db0d8c14de65af6225b4860/crates/codegen/xai-grok-shell/src/auth/model.rs#L46-L107)). Expiry is calculated from `expires_at`, otherwise from a 30-day TTL ([source lines 333–356](https://github.com/xai-org/grok-build/blob/780d1388fff103ff0db0d8c14de65af6225b4860/crates/codegen/xai-grok-shell/src/auth/model.rs#L333-L356)).

The official client explicitly represents `FileMissing`, `EntryMissing`, and `Unreadable` as separate disk states ([source lines 227–242](https://github.com/xai-org/grok-build/blob/780d1388fff103ff0db0d8c14de65af6225b4860/crates/codegen/xai-grok-shell/src/auth/manager.rs#L227-L242)) and derives them from file I/O plus scope lookup ([source lines 327–380](https://github.com/xai-org/grok-build/blob/780d1388fff103ff0db0d8c14de65af6225b4860/crates/codegen/xai-grok-shell/src/auth/manager.rs#L327-L380)). That supports local `missing` versus `unreadable` without any provider call. Implementations must parse the file internally but must never put tokens, credential fragments, or the raw file in output or logs.

At the wire boundary, Grok Build records whether a credential was actually sent: `Sent` means the server rejected a credential; `Missing` means no credential header was sent ([source lines 79–136](https://github.com/xai-org/grok-build/blob/780d1388fff103ff0db0d8c14de65af6225b4860/crates/codegen/xai-grok-sampling-types/src/error.rs#L79-L136)). It classifies only `401` as an authentication error and explicitly excludes `403` ([source lines 225–243](https://github.com/xai-org/grok-build/blob/780d1388fff103ff0db0d8c14de65af6225b4860/crates/codegen/xai-grok-sampling-types/src/error.rs#L225-L243)). The xAI public error table likewise defines `401` as a missing Authorization header or invalid token, while also noting that an incorrect API key can appear under `400` ([Debugging Errors](https://docs.x.ai/developers/debugging)). Therefore V1 should classify a credentialed `401` as rejection, but never turn every `400` into an auth error.

### Rejected is not revoked

The official OAuth refresh implementation recognizes `invalid_grant` and `invalid_client` as its only terminal OAuth codes ([source lines 24–32](https://github.com/xai-org/grok-build/blob/780d1388fff103ff0db0d8c14de65af6225b4860/crates/codegen/xai-grok-shell/src/auth/oidc/refresh.rs#L24-L32)). Crucially, its machine reason documents `invalid_grant` as “expired, reused, or revoked; the IdP does not distinguish these” ([source lines 79–96](https://github.com/xai-org/grok-build/blob/780d1388fff103ff0db0d8c14de65af6225b4860/crates/codegen/xai-grok-shell/src/auth/error.rs#L79-L96)).

Consequences:

- `401` + credential sent supports `auth_rejected`, not `auth_revoked`.
- `invalid_grant` supports `auth_refresh_rejected`, not the narrower `auth_revoked`.
- The V1 read-only credential boundary prohibits performing a refresh merely to improve classification. Revocation is therefore unobservable as a distinct V1 state.

### Forbidden is not entitlement-specific

xAI's public error table assigns `403` to either insufficient permission or a blocked API key/team ([Debugging Errors](https://docs.x.ai/developers/debugging)). Grok Build broadens the same status further: its code says `403` can be content-safety, ZDR, or other policy denial ([source lines 225–234](https://github.com/xai-org/grok-build/blob/780d1388fff103ff0db0d8c14de65af6225b4860/crates/codegen/xai-grok-sampling-types/src/error.rs#L225-L234)); its ACP mapping repeats that `403` must not trigger re-authentication ([source lines 123–158](https://github.com/xai-org/grok-build/blob/780d1388fff103ff0db0d8c14de65af6225b4860/crates/codegen/xai-grok-shell/src/sampling/error.rs#L123-L158)).

The client does contain a subscription-message special case, matching the human string `requires a Grok subscription` ([source lines 133–147](https://github.com/xai-org/grok-build/blob/780d1388fff103ff0db0d8c14de65af6225b4860/crates/codegen/xai-grok-shell/src/sampling/error.rs#L133-L147)). No public first-party contract makes that message a stable machine value. A raw-body-free public API should therefore emit `permission_denied`; it may retain a private, sanitized diagnostic reason only if a future documented machine code is available.

### Rate limiting

xAI documents that exceeding model RPS or TPM limits returns `429`, and recommends exponential backoff ([Rate Limits](https://docs.x.ai/developers/rate-limits)). Grok Build gives actual `429` a dedicated stable error code and explicitly refuses to infer rate limiting from other statuses ([source lines 12–19](https://github.com/xai-org/grok-build/blob/780d1388fff103ff0db0d8c14de65af6225b4860/crates/codegen/xai-grok-shell/src/sampling/error.rs#L12-L19)). The sampler also captures integer `Retry-After` seconds, capped at 120 seconds, and the optional `x-should-retry` boolean ([source lines 209–230](https://github.com/xai-org/grok-build/blob/780d1388fff103ff0db0d8c14de65af6225b4860/crates/codegen/xai-grok-sampler/src/client.rs#L209-L230)).

Only expose parsed, bounded metadata. Never return the upstream error body. `Retry-After` absence or invalid syntax should result in no field, not a guessed value.

### Model availability

The authenticated [`GET /v1/models` API](https://docs.x.ai/developers/rest-api-reference/inference/models) is documented to list models available to the authenticating API key. This is the best positive evidence for model availability. Conversely, xAI's error table says `404` can mean either a model was not found **or** the endpoint URL is invalid ([Debugging Errors](https://docs.x.ai/developers/debugging)). Grok Build accordingly maps `404` only to generic `resource_not_found` ([source lines 149–153](https://github.com/xai-org/grok-build/blob/780d1388fff103ff0db0d8c14de65af6225b4860/crates/codegen/xai-grok-shell/src/sampling/error.rs#L149-L153)).

V1 may use `unsupported_model` when a successfully fetched credential-scoped catalog excludes the requested model, or when a `404` comes from a hard-coded, already-validated inference route. If endpoint configuration is variable, retain generic `not_found`.

### Timeouts

Grok Build distinguishes a transport timeout (`reqwest::Error::is_timeout`) from its own inference idle timeout ([source lines 545–562](https://github.com/xai-org/grok-build/blob/780d1388fff103ff0db0d8c14de65af6225b4860/crates/codegen/xai-grok-sampling-types/src/error.rs#L545-L562)). For streams it owns the timer: no transport chunk, or only non-content keepalives until the deadline, produces `IdleTimeout` ([source lines 20–41](https://github.com/xai-org/grok-build/blob/780d1388fff103ff0db0d8c14de65af6225b4860/crates/codegen/xai-grok-sampler/src/stream/chat_completions.rs#L20-L41), [lines 86–109](https://github.com/xai-org/grok-build/blob/780d1388fff103ff0db0d8c14de65af6225b4860/crates/codegen/xai-grok-sampler/src/stream/chat_completions.rs#L86-L109)).

The safe contract is a locally observed `timeout` plus a stage such as `connect`, `request`, or `idle`. Edge `5xx`/`52x` statuses are retryable upstream failures but must not be rewritten as a client timeout.

### Size failures

Grok Build has an exact predicate for HTTP `413 Payload Too Large` ([source lines 245–263](https://github.com/xai-org/grok-build/blob/780d1388fff103ff0db0d8c14de65af6225b4860/crates/codegen/xai-grok-sampling-types/src/error.rs#L245-L263)). It also has a connection-reset/broken-pipe heuristic for servers that close an oversized upload without returning `413`, but calls it “likely” rather than definitive ([source lines 265–280](https://github.com/xai-org/grok-build/blob/780d1388fff103ff0db0d8c14de65af6225b4860/crates/codegen/xai-grok-sampling-types/src/error.rs#L265-L280)). V1 should keep the heuristic under a generic transport error.

Provider output truncation is a separate typed condition: a `max_tokens` / length stop becomes `MaxTokensTruncation` ([source lines 425–455](https://github.com/xai-org/grok-build/blob/780d1388fff103ff0db0d8c14de65af6225b4860/crates/codegen/xai-grok-sampler/src/stream/messages.rs#L425-L455), [lines 519–524](https://github.com/xai-org/grok-build/blob/780d1388fff103ff0db0d8c14de65af6225b4860/crates/codegen/xai-grok-sampler/src/stream/messages.rs#L519-L524)). Context-window overflow is less stable: the official source explicitly says backends report it inconsistently with no stable error code and uses message matching ([source lines 532–543](https://github.com/xai-org/grok-build/blob/780d1388fff103ff0db0d8c14de65af6225b4860/crates/codegen/xai-grok-sampling-types/src/error.rs#L532-L543)).

For the issue's “response-size” requirement, the reliable solution is a CLI-owned byte ceiling while streaming. Crossing that ceiling is `output_too_large`, independent of provider error semantics. This is different from `request_too_large`, `context_too_large`, and `output_truncated`.

### Upstream protocol incompatibility and safe bodies

Grok Build accepts two structured error envelopes—OpenAI-style nested `error` and a flat Grok proxy `code`/`error` shape—then truncates any surfaced structured message to 280 characters. Non-JSON bodies become a fixed status-based message rather than raw bytes ([source lines 416–453](https://github.com/xai-org/grok-build/blob/780d1388fff103ff0db0d8c14de65af6225b4860/crates/codegen/xai-grok-sampling-types/src/error.rs#L416-L453), [lines 456–520](https://github.com/xai-org/grok-build/blob/780d1388fff103ff0db0d8c14de65af6225b4860/crates/codegen/xai-grok-sampling-types/src/error.rs#L456-L520)).

For successful Responses API streams, it first handles known xAI extensions and then emits a typed serialization failure if decoding still fails ([source lines 102–129](https://github.com/xai-org/grok-build/blob/780d1388fff103ff0db0d8c14de65af6225b4860/crates/codegen/xai-grok-sampler/src/client.rs#L102-L129)). Chat Completions likewise separates structured stream errors, typed chunk decode failures, and transport interruption ([source lines 1083–1132](https://github.com/xai-org/grok-build/blob/780d1388fff103ff0db0d8c14de65af6225b4860/crates/codegen/xai-grok-sampler/src/client.rs#L1083-L1132)).

V1 can therefore emit `upstream_incompatible` from a typed decode failure without including the offending body. Safe fields are the expected protocol/backend, response phase, HTTP status if present, and a request/correlation ID supplied in headers. Do not include the response sample, parser input, provider message, token, request headers, or query.

## Recommended normalization order

1. Before network I/O, classify local credential state: `auth_missing`, `auth_unreadable`, or `auth_expired`.
2. Apply the CLI's request validation, deadline, and output byte ceilings locally.
3. On a trusted xAI response, classify exact protocol evidence first: credentialed `401`, `403`, `404`, `413`, `429`, then other `4xx`/`5xx`.
4. Treat `Retry-After` and documented structured codes as bounded metadata, never as a reason to forward the provider body.
5. While streaming, distinguish local idle deadline, transport interruption, provider length stop, local output ceiling, and typed protocol decode failure.
6. Default unknown cases to a broad `provider_error` / `transport_error`; never guess a narrower category from message substrings.

This ordering preserves stable machine behavior while allowing xAI to change human messages, proxies, or internal policy reasons without breaking the CLI contract.
