# slack-utils

`slack-utils` is a small command-line tool for exporting Slack conversation history to JSON. It can export public channels, private channels, direct messages, and group direct messages, with thread replies nested beneath their root messages.

## Install

macOS/Linux:

```bash
curl -fsSL https://github.com/SomeoneWithOptions/slack-utils/releases/latest/download/install.sh | sh
```

Windows PowerShell:

```powershell
iwr -useb https://github.com/SomeoneWithOptions/slack-utils/releases/latest/download/install.ps1 | iex
```

## Quick start

Set a Slack API token that can access the conversation:

```bash
export SLACK_TOKEN="xoxb-your-token"
```

Export a channel to `export.json`:

```bash
slack-utils channels export -channel C1234567890
```

A few common variations:

```bash
# Export the last seven days
slack-utils channels export -channel C1234567890 -since 7d

# Export a date range to a custom file
slack-utils channels export \
  -channel C1234567890 \
  -since 2024-05-01 \
  -to 2024-05-31 \
  -o may-2024.json

# Export root messages without thread replies
slack-utils channels export -channel C1234567890 -no-replies
```

Optionally initialize the workspace user cache before exporting:

```bash
slack-utils users init
```

This creates `users.json`, which the exporter uses to resolve Slack user IDs. Later, add newly joined workspace users without changing existing entries:

```bash
slack-utils users update
```

## Documentation

See [docs/usage.md](docs/usage.md) for token scopes, all command flags, time filters, user-cache behavior, output details, building from source, and release instructions.

Command-specific help is also available from the CLI:

```bash
slack-utils channels export -h
slack-utils users init -h
slack-utils users update -h
```
