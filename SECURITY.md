# Security policy

## Reporting a vulnerability

Report vulnerabilities privately through GitHub Security Advisories. Do not
include access tokens, refresh tokens, API keys, callback URLs, credential
files, or unredacted request headers in an issue or discussion.

## Credential-handling boundary

The first version of this project must treat the Grok Build credential store as
read-only. It must never:

- print or return credentials;
- copy credentials into another store;
- refresh or rotate credentials;
- send OAuth credentials to a configurable or non-xAI origin;
- include credentials in logs, errors, diagnostics, or test fixtures.

Authentication failures should return a typed error that directs the user to
the official `grok login` flow.
