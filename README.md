# grok-search-cli

Agent-friendly CLI for Grok-powered web and X search.

> [!IMPORTANT]
> This is an unofficial, experimental project. It is not affiliated with or
> endorsed by xAI.

## Status

V1 product and technical design is complete; no executable has been implemented
yet. See the [V1 specification](docs/spec/v1.md).

V1 is approved only for private, repository-owner use at the owner's risk. xAI
has not authorized this integration, and public distribution remains blocked.

## Initial scope

- Expose focused `web` and `x` search commands.
- Return stable, machine-readable JSON for coding agents and scripts.
- Reuse an existing Grok Build login from `~/.grok/auth.json` without copying,
  refreshing, or rewriting credentials in the first version.
- Provide a small skill that teaches supported agents when and how to invoke the
  CLI.

## Non-goals for the first version

- A general Grok chat client.
- An OpenAI-compatible proxy.
- An MCP server.
- A second OAuth login or credential store.
- Automatic mutation or refresh of Grok Build credentials.

## Authentication direction

Users authenticate with the official Grok Build CLI:

```sh
grok login
```

The search CLI will treat Grok Build's credential file as read-only. Missing,
unreadable, locally expired, or provider-rejected authentication will produce
evidence-bounded typed errors. Applicable errors direct the user to `grok login`;
the search CLI never starts that flow itself.

## Security

Never commit or share `~/.grok/auth.json`, OAuth tokens, callback URLs, API keys,
or captured request headers. See [SECURITY.md](SECURITY.md).

## License

[MIT](LICENSE)
