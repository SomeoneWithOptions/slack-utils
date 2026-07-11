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

Export a conversation to `export.json`:

```bash
slack-utils conversations export -channel C1234567890
```

A few common variations:

```bash
# Export the last seven days
slack-utils conversations export -channel C1234567890 -since 7d

# Export a date range to a custom file
slack-utils conversations export \
  -channel C1234567890 \
  -since 2024-05-01 \
  -to 2024-05-31 \
  -o may-2024.json

# Export root messages without thread replies
slack-utils conversations export -channel C1234567890 -no-replies
```

Look up a Slack user ID by email address, or get all available information for an ID:

```bash
slack-utils users lookup -email person@example.com
slack-utils users info -id U1234567890
```

Optionally initialize the workspace user cache before exporting:

```bash
slack-utils users cache init
```

This creates `users.json`, which the exporter uses to resolve Slack user IDs. Later, add newly joined workspace users without changing existing entries:

```bash
slack-utils users cache update
```

## Documentation

See [docs/usage.md](docs/usage.md) for token scopes, all command flags, time filters, user-cache behavior, output details, building from source, and release instructions.

Command-specific help is also available from the CLI:

```bash
slack-utils conversations export -h
slack-utils users lookup -h
slack-utils users info -h
slack-utils users cache init -h
slack-utils users cache update -h
```
