# Model-override probe

> **PROTOTYPE — not production code.**

## Question

How should `--model <id>` behave for available, unavailable, retired, and newly
introduced Grok models without a local allowlist or silent fallback?

This probe sends the supplied model ID exactly once and only in the Responses
API request body. It never sends `x-grok-model-override`, retries with another
model, invokes Grok Build, refreshes credentials, or writes `auth.json`.

## Run

```bash
./prototypes/model-override/probe.sh web grok-4.5
./prototypes/model-override/probe.sh x grok-4.5
./prototypes/model-override/probe.sh web grok-4.5-build
./prototypes/model-override/probe.sh web grok-search-cli-definitely-not-a-model
```

Output is sanitized JSON containing the requested model, HTTP status, structured
provider code/type when present, response model IDs, search-tool evidence, and
the credential fingerprint result. It never prints the provider message, query,
answer, raw response, or credential.

The primary request is never preflighted against a catalog. Only after the
known-valid Responses route returns `404` does the probe fetch the same
credential-scoped proxy catalog. A successful catalog that omits the requested
ID supports `unsupported_model`; a failed catalog or one that contains the ID
leaves the result as broad `provider_error`.

## Observed result

Validated on 2026-08-04:

| Requested model | Web | X | Provider model / classification |
| --- | --- | --- | --- |
| `grok-4.5` | `200` | `200` | Response identifies `grok-4.5-build`; both searches completed with the requested tool event. |
| `grok-4.5-build` | `404` | not run | Provider code `not-found`; successful catalog omitted it, so `unsupported_model`. |
| `grok-search-cli-invalid-20260804` | `404` | not run | Provider code `not-found`; successful catalog omitted it, so `unsupported_model`. |

The credential-scoped proxy catalog returned only `grok-4.5`. Every request
left the credential store unchanged.

## Recommended V1 contract

- `--model` is an opaque provider ID. Validate only transport safety: valid
  UTF-8, 1–256 bytes, no control or whitespace characters. Do not trim,
  canonicalize, enumerate, or apply a local allowlist.
- The default `grok-4.5` applies only when `--model` is absent. An explicit
  empty/invalid value is a usage error.
- Send the selected ID exactly once in the request body. Never send the model
  override header, retry another model, or silently fall back to the default.
- Do not preflight `/v1/models`; its data may be narrower than provider-side
  aliases or a newly deployed model. This lets new IDs work without a CLI
  release.
- After a `404` from the fixed, known-valid Responses route, fetch the
  credential-scoped catalog once. A successful catalog that omits the requested
  ID yields non-retryable `unsupported_model`; otherwise retain broad
  `provider_error`.
- A search-incapable model is not inferable from its name. Normalize its actual
  behavior: `400`/`409`/`422` as `request_rejected`, or a successful stream with
  no requested tool event as `search_not_performed`.
- Successful JSON keeps `model` as the effective requested ID and adds optional
  `provider_model` when a reliable response model is present and differs. That
  exposes provider alias resolution without treating it as CLI fallback.
- Retries, when otherwise allowed by the transport policy, always reuse the
  exact same model ID.

Technical success is account- and date-specific. It is not evidence of xAI
authorization or a stable third-party contract. The repository owner has chosen
to continue private use at their own risk; public distribution remains blocked.
