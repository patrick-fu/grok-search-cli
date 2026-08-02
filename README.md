# grok-search-cli

Agent-friendly CLI for Grok-powered web and X search.

> [!IMPORTANT]
> This is an unofficial, experimental project. It is not affiliated with or
> endorsed by xAI.

## Status

Repository bootstrap and product design. No executable has been implemented yet.

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
expired, revoked, or unreadable credentials will produce typed errors with an
actionable `grok login` or `grok login --device-code` remediation.

## Security

Never commit or share `~/.grok/auth.json`, OAuth tokens, callback URLs, API keys,
or captured request headers. See [SECURITY.md](SECURITY.md).

## License

[MIT](LICENSE)
