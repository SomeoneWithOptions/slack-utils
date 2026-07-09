# slack-utils

A small CLI for exporting Slack conversation history to JSON.

## Setup

Set a Slack API token with access to the conversation you want to export:

```bash
export SLACK_TOKEN="xoxb-your-token"
```

Required Slack OAuth history scope depends on the conversation ID:

- `C...` public channel: `channels:history`
- `G...` private channel: `groups:history`
- `D...` direct message: `im:history`
- `G...` multi-person DM: `mpim:history`

Optional scopes:

- `users:read` / `users:read.email` to resolve users
- `channels:read`, `groups:read`, `im:read`, or `mpim:read` to resolve conversation names

## Run

From this repo:

```bash
go run . channels export -channel C1234567890 -o export.json
```

Or build and run the binary:

```bash
go build -o slack-utils .
./slack-utils channels export -channel C1234567890 -o export.json
```

## Usage

```bash
slack-utils channels export -channel <conversation-id> [flags]
```

Examples:

```bash
# Export everything, including thread replies, to ./export.json
slack-utils channels export -channel C1234567890

# Export the last 7 days
slack-utils channels export -channel C1234567890 -since 7d

# Export a date range
slack-utils channels export -channel C1234567890 -since 2024-05-01 -to 2024-05-31

# Export only root messages, no thread replies
slack-utils channels export -channel C1234567890 -no-replies

# Limit root messages and write to a custom file
slack-utils channels export -channel C1234567890 -limit 50 -o /tmp/export.json
```

Useful flags:

| Flag | Description |
| --- | --- |
| `-channel` | Slack conversation ID to export. Required. |
| `-o`, `-output` | Output JSON path. Default: `./export.json`. |
| `-since` | Start time: RFC3339, `YYYY-MM-DD`, or relative duration like `7d` / `24h`. |
| `-to` | End time: RFC3339 or `YYYY-MM-DD`. |
| `-limit` | Maximum root messages to export. `0` means no limit. |
| `-no-replies` | Skip thread replies. |
| `-delay` | Delay between Slack API requests. Default: `1s`. |
| `-q`, `-quiet` | Suppress progress logs. |

The export is written as JSON with top-level conversation metadata and a `messages` array. Thread replies are nested under each root message in `replies`.

The CLI may also create `users.json` as a local user cache.
