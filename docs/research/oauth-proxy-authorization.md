# Independent OAuth proxy authorization

Accessed: 2026-08-03

## Answer

No first-party xAI source found authorizes an unnamed, independently distributed CLI to read Grok Build's cached OAuth credential and call `cli-chat-proxy.grok.com` directly.

xAI does authorize two adjacent but narrower patterns:

1. Third-party products may integrate through the documented xAI API at `api.x.ai` using an xAI API key and the applicable enterprise terms.
2. xAI has individually announced subscription OAuth support for named third-party products such as OpenCode, OpenClaw, Hermes Agent, and Kilo Code. Those announcements direct the user through an xAI OAuth or device-code sign-in inside the named product; they do not publish a general program or permission to reuse Grok Build's `auth.json`.

The proposed proxy call also falls within the AUP's express prohibition on bot-based and automated/non-human access unless xAI provides a specific authorization that overrides or qualifies that prohibition for this client. Reading a same-user local cache is not itself expressly addressed, but it does not supply authorization to use the credential from another client.

**Decision:** do not implement or distribute the cached-OAuth/proxy path without specific written xAI authorization. Use the documented `api.x.ai` + `XAI_API_KEY` path instead, or obtain an xAI-approved OAuth integration for this project.

## Classification

### Explicitly allowed

- **Documented API integrations.** The [Enterprise Terms, section 1.2](https://x.ai/legal/terms-of-service-enterprise) grant customers a limited right to use xAI's APIs to integrate the services with their products, subject to the agreement and applicable purchase terms. The [Inference API reference](https://docs.x.ai/developers/rest-api-reference/inference) specifies `https://api.x.ai` and `Authorization: Bearer <your xAI API key>`. The [Community Integrations page](https://docs.x.ai/developers/community) applies this pattern to third-party SDKs and proxies such as LiteLLM and Vercel AI SDK, both using `XAI_API_KEY`.
- **Named subscription OAuth integrations.** xAI explicitly says users may use their subscription in [OpenCode](https://x.ai/news/grok-opencode), [OpenClaw](https://x.ai/news/grok-openclaw), [Hermes Agent](https://x.ai/news/grok-hermes), and [Kilo Code](https://x.ai/news/grok-kilocode). The setup instructions require an OAuth browser login or device-code flow in the named product. These are direct, product-specific xAI authorizations.
- **Use and modification of the Grok Build source code.** The official [Grok Build repository](https://github.com/xai-org/grok-build) is published under the [Apache License 2.0](https://github.com/xai-org/grok-build/blob/780d1388fff103ff0db0d8c14de65af6225b4860/LICENSE). That license authorizes use, modification, and distribution of the code under its terms. It does not grant access rights to xAI's hosted services.

### Explicitly prohibited

- **Automated access without a specific exception.** The [xAI AUP](https://x.ai/legal/acceptable-use-policy) applies to consumers, developers, and businesses. Under conduct detrimental to the service, it prohibits using bots to access the service and bypassing systems or protective measures; it separately prohibits accessing the services through automated or non-human means. An independent CLI's direct subscription-proxy calls are within that language on its face. xAI's named OAuth integrations show that xAI can specifically authorize third-party clients; they do not turn the specific announcements into a blanket exception.
- **Credential sharing with another person.** The [Consumer Terms, Registration](https://x.ai/legal/terms-of-service) say users may not share account credentials or make the account available to anyone else. A purely local, same-user read is not clearly the sharing described here, but any design that copies, uploads, logs, or transfers the cached credential to another person or service would cross this line.
- **Circumvention and unexpressed enterprise use.** The [Enterprise Terms, section 2](https://x.ai/legal/terms-of-service-enterprise) prohibit circumventing or exceeding service account limits and using the services except as expressly permitted by the agreement. This matters if the project is distributed as a developer/business integration rather than used solely as a consumer tool.

### Not stated or not authorized by the cited source

- **Another process reading Grok Build's cache.** The [Grok Build authentication guide](https://github.com/xai-org/grok-build/blob/780d1388fff103ff0db0d8c14de65af6225b4860/crates/codegen/xai-grok-pager/docs/user-guide/02-authentication.md#credential-storage) documents `~/.grok/auth.json`, says anyone with filesystem access can use the tokens, requires owner-only permissions, and warns against copying the file. This is a security warning and implementation fact, not permission for another client to consume it.
- **A third party merely possessing Grok credentials.** The [xAI consumer FAQ](https://docs.x.ai/grok/faq) tells users who reach the retired Studio through a third-party app using their Grok credentials to revoke and change those credentials for safety. The warning is context-specific rather than a universal ban, but it is not an authorization signal for credential reuse.
- **A general subscription-OAuth developer program.** No first-party documentation found exposes client registration, approval criteria, redirect-URI registration, allowed scopes, or distribution terms for an arbitrary subscription OAuth integration. The named announcements repeatedly say more integrations are “coming soon,” which supports a rollout of selected integrations rather than an open authorization.
- **Credential reuse by the named integrations.** The xAI announcements tell users to connect or log in from each product. They do not say those products read Grok Build's existing cache.
- **Reuse of Grok Build's proxy identity.** Official source gives xAI OAuth tokens the proxy-specific `grok-cli:access` scope in [`config.rs`](https://github.com/xai-org/grok-build/blob/780d1388fff103ff0db0d8c14de65af6225b4860/crates/codegen/xai-grok-shell/src/auth/config.rs#L13-L27), and official requests add `X-XAI-Token-Auth: xai-grok-cli` in [`grok_auth_credentials.rs`](https://github.com/xai-org/grok-build/blob/780d1388fff103ff0db0d8c14de65af6225b4860/crates/codegen/xai-grok-shell/src/util/grok_auth_credentials.rs#L116-L125). The source describes how the official client identifies itself; it does not authorize an independent client to claim that identity.
- **Proxy access from the source license.** Apache 2.0 governs copyrighted source. It does not amend the AUP, Consumer Terms, Enterprise Terms, OAuth grant, subscription entitlement, or proxy access policy.

## What authorization is still required

Before implementing or distributing the proposed path, obtain written xAI authorization that identifies this project and expressly covers all of the following:

- use of a Grok/SuperGrok/X Premium subscription from this third-party CLI;
- an approved OAuth client and flow, including client ID, redirect/device flow, scopes, and token refresh behavior;
- whether reading Grok Build's existing `~/.grok/auth.json` is permitted, or whether an independent sign-in and credential store is required;
- direct access to `cli-chat-proxy.grok.com`, permitted routes/models/tools, and the required client-identification header;
- quota, rate-limit, entitlement, data-handling, redistribution, and public-release conditions; and
- an explicit exception or clarification for the AUP's automated-access restriction.

The authorization should be a published xAI integration document, an applicable purchase term, or written approval from xAI that can be retained with the project decision record. A generic successful OAuth response, public client ID, accepted proxy request, open-source code, or user consent is insufficient because none changes the governing service terms.

The official escalation routes found are [xAI Contact](https://x.ai/contact), [Contact Sales](https://x.ai/contact-sales), and `sales@x.ai` in the [xAI developer FAQ](https://docs.x.ai/developers/faq/general). Ask specifically for subscription OAuth integration approval, not general API access.

## Recommended resolution

Close the research ticket as answered with the implementation gate unresolved:

- **Safe V1:** require a user-supplied xAI API key and call the documented `api.x.ai` API.
- **Subscription proxy:** keep blocked until xAI explicitly approves this project and supplies the required OAuth/integration terms. If approval requires a fresh project-specific OAuth flow, do not reuse Grok Build's cache.

This is a product authorization finding, not legal advice.

## Source register

All sources below are first-party xAI publications or the official xAI GitHub organization. All were accessed on 2026-08-03.

| Source | Version/date observed | Direct URL |
| --- | --- | --- |
| xAI Acceptable Use Policy | Effective 2026-06-26 | <https://x.ai/legal/acceptable-use-policy> |
| Consumer Terms of Service | Effective 2026-06-26 | <https://x.ai/legal/terms-of-service> |
| Enterprise Terms of Service | Last updated 2026-05-12 | <https://x.ai/legal/terms-of-service-enterprise> |
| Grok Build authentication guide | Official source at commit `780d1388fff103ff0db0d8c14de65af6225b4860` | <https://github.com/xai-org/grok-build/blob/780d1388fff103ff0db0d8c14de65af6225b4860/crates/codegen/xai-grok-pager/docs/user-guide/02-authentication.md> |
| Grok Build OAuth configuration source | Same commit | <https://github.com/xai-org/grok-build/blob/780d1388fff103ff0db0d8c14de65af6225b4860/crates/codegen/xai-grok-shell/src/auth/config.rs> |
| Grok Build request-auth source | Same commit | <https://github.com/xai-org/grok-build/blob/780d1388fff103ff0db0d8c14de65af6225b4860/crates/codegen/xai-grok-shell/src/util/grok_auth_credentials.rs> |
| Grok Build Enterprise Deployments | Last updated 2026-07-21 | <https://docs.x.ai/build/enterprise> |
| xAI Inference API reference | Last updated 2026-03-06 | <https://docs.x.ai/developers/rest-api-reference/inference> |
| xAI Community Integrations | Last updated 2026-05-12 | <https://docs.x.ai/developers/community> |
| xAI consumer FAQ | Last updated 2026-07-06 | <https://docs.x.ai/grok/faq> |
| Use Grok in OpenCode | Published 2026-05-21 | <https://x.ai/news/grok-opencode> |
| Use Grok in OpenClaw | Published 2026-05-19 | <https://x.ai/news/grok-openclaw> |
| Connect Grok to Hermes Agent | Published 2026-05-15 | <https://x.ai/news/grok-hermes> |
| Use Grok in Kilo Code | Published 2026-05-27 | <https://x.ai/news/grok-kilocode> |
| xAI developer FAQ | Last updated 2026-03-19 | <https://docs.x.ai/developers/faq/general> |
