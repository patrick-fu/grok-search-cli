# Explicit live smoke

This procedure is off by default and is not part of CI. It exercises the live
subscription proxy only when the user explicitly chooses to do so. Passing it
demonstrates a narrow technical compatibility check; it is not xAI
authorization.

## Prepare a local artifact

Build local archives; this command neither uploads nor publishes:

```sh
VERSION=0.1.0-private.1 SOURCE_DATE_EPOCH=1700000000 scripts/build-private-artifacts.sh
```

Choose the matching archive under `dist/private/`, verify its sidecar checksum,
and extract it. V1 supports macOS and Linux on amd64 and arm64. `METADATA.json`
inside each archive records the target, version, and compatibility floor
`0.2.117`.

```sh
shasum -a 256 -c dist/private/grok-search_0.1.0-private.1_darwin_arm64.tar.gz.sha256
tar -xzf dist/private/grok-search_0.1.0-private.1_darwin_arm64.tar.gz
./grok-search version
```

On Linux, use `sha256sum -c` instead of `shasum -a 256 -c`. An installed
release binary from `install.sh` or `grok-search update` is also valid.

## Run the smoke explicitly

First use the official Grok Build CLI yourself if a login is needed. This
project never starts, refreshes, or changes that login. An agent must ask for
explicit permission before it runs `grok login` or this script.

The script sends one benign Web request and one benign X request. It redirects
the CLI JSON to a temporary directory, prints only the type, exit code, source
count, warning count, or normalized error code, and removes the temporary files
on exit. Before and after both requests it compares the selected credential
file's SHA-256, mode, size, modification time, and inode.

```sh
scripts/manual-smoke.sh --confirm-live-smoke \
  --binary ./grok-search \
  --auth-file "${GROK_HOME:-$HOME/.grok}/auth.json"
```

Omit `--auth-file` to use the same default path. The confirmation flag is
required on every invocation. Do not add `--verbose`: smoke output is intended
to remain a small sanitized summary.

If either request fails or the credential snapshot changes, stop and investigate
locally. Do not retry by refreshing credentials, changing headers, or removing
filters. A successful run remains unofficial, user-risk technical evidence; it
does not establish authorization.
