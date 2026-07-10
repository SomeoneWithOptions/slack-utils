# Usage reference

## Installation details

The macOS/Linux and Windows installers download the latest GitHub Release asset for the current OS and architecture and verify it against `checksums.txt` before installation.

On macOS/Linux, set `INSTALL_DIR` to choose a different destination:

```bash
curl -fsSL https://github.com/SomeoneWithOptions/slack-utils/releases/latest/download/install.sh \
  | INSTALL_DIR=/path/to/bin sh
```

## Authentication and Slack scopes

Set a Slack API token in `SLACK_TOKEN`:

```bash
export SLACK_TOKEN="xoxb-your-token"
```

The required history scope depends on the conversation being exported:

| Conversation ID | Conversation type | Required scope |
| --- | --- | --- |
| `C...` | Public channel | `channels:history` |
| `G...` | Private channel | `groups:history` |
| `D...` | Direct message | `im:history` |
| `G...` | Multi-person direct message | `mpim:history` |

Optional scopes improve the exported metadata:

- `users:read` and `users:read.email` resolve user IDs to email addresses.
- `channels:read`, `groups:read`, `im:read`, or `mpim:read` resolve conversation names.

After adding scopes to a Slack app, reinstall or reauthorize it for the changes to take effect.

## User cache

Initialize `users.json` with all users returned by Slack:

```bash
slack-utils users init
```

The command uses cursor pagination, requests at most 200 users per page, and waits three seconds between pages by default. Slack's `users.list` response includes invited and deactivated users, which helps resolve authors in historical exports.

`users init` never overwrites an existing cache. If the destination already exists, it exits successfully without making a Slack API request.

The token requires `users:read`. Add `users:read.email` to store email addresses; when an email is unavailable, the user's Slack ID is stored instead. With an Enterprise Grid organization token, use `-team` to select a workspace.

Examples:

```bash
# Write the cache elsewhere
slack-utils users init -output /path/to/users.json

# Use a more conservative delay between pages
slack-utils users init -delay 5s

# Select a workspace when using an Enterprise Grid organization token
slack-utils users init -team T1234567890
```

Update an existing cache with workspace users whose IDs are not already present:

```bash
slack-utils users update
```

`users update` requires the cache to exist and directs you to `users init` otherwise. It preserves every existing entry and never removes users. It accepts the same `-output`, `-delay`, `-team`, and `-quiet` flags as `users init`.

### `users init` flags

| Flag | Default | Description |
| --- | --- | --- |
| `-o`, `-output` | `./users.json` | Path at which to create the user cache. |
| `-delay` | `3s` | Delay between `users.list` pages. |
| `-team` | — | Workspace team ID for an Enterprise Grid organization token. |
| `-q`, `-quiet` | `false` | Suppress progress logs; errors are still written to stderr. |

## Exporting a conversation

```bash
slack-utils channels export -channel <conversation-id> [flags]
```

By default, the command exports all available root messages and thread replies to `./export.json`.

Examples:

```bash
# Export all available history, including thread replies
slack-utils channels export -channel C1234567890

# Export the last seven days
slack-utils channels export -channel C1234567890 -since 7d

# Export messages on one UTC calendar date
slack-utils channels export \
  -channel C1234567890 \
  -since 2024-05-01 \
  -to 2024-05-01

# Export a precise time range
slack-utils channels export \
  -channel C1234567890 \
  -since 2024-05-01T09:00:00Z \
  -to 2024-05-01T17:00:00Z

# Export only root messages
slack-utils channels export -channel C1234567890 -no-replies

# Limit the number of root messages and choose an output file
slack-utils channels export \
  -channel C1234567890 \
  -limit 50 \
  -o /tmp/export.json
```

### `channels export` flags

| Flag | Default | Description |
| --- | --- | --- |
| `-channel` | — | Slack conversation ID to export. Required. |
| `-o`, `-output` | `./export.json` | Path at which to write the export JSON. |
| `-since` | — | Include messages on or after this time. Accepts RFC 3339, `YYYY-MM-DD`, or a relative duration such as `7d` or `24h`. |
| `-to` | — | Include messages on or before this time. Accepts RFC 3339 or `YYYY-MM-DD`. |
| `-limit` | `0` | Maximum number of root messages to export. `0` means no limit. |
| `-no-replies` | `false` | Skip thread replies and export root messages only. |
| `-delay` | `1s` | Delay between Slack API requests. |
| `-q`, `-quiet` | `false` | Suppress progress logs; errors are still written to stderr. |

### Time filters

`-since` supports:

- RFC 3339 timestamps, such as `2024-05-01T09:00:00Z`
- UTC dates, such as `2024-05-01`
- Relative durations, such as `7d` or `24h`, measured backward from the time the command runs

`-to` supports RFC 3339 timestamps and UTC dates. A date passed to `-to` includes that entire UTC calendar day.

### User resolution

Exports use `./users.json` as a local user cache. Run `slack-utils users init` first to populate the complete workspace cache.

During an export, users missing from the cache are requested from Slack and added to it. If a profile email is unavailable, the user's Slack ID is used.

### Output format

The output is JSON containing conversation metadata and a `messages` array. Replies are nested under their root message:

```json
{
  "channel_id": "C1234567890",
  "channel_name": "general",
  "exported_at": "2024-05-02T12:00:00Z",
  "messages": [
    {
      "user": "person@example.com",
      "message": "Hello",
      "date": "2024-05-01T10:00:00Z",
      "replies": [
        {
          "user": "another@example.com",
          "message": "Hi!",
          "date": "2024-05-01T10:01:00Z"
        }
      ]
    }
  ]
}
```

## Building from source

Run directly from the repository:

```bash
go run . channels export -channel C1234567890 -o export.json
```

Or build a local binary:

```bash
go build -o slack-utils .
./slack-utils channels export -channel C1234567890 -o export.json
```

## Releasing

Releases are published from the `release` branch only:

```bash
git checkout release
git merge main
git tag v0.1.0
git push origin release v0.1.0
```

Pushing a `v*` tag that points at the current `release` branch HEAD starts the GitHub Actions release workflow. It builds macOS, Linux, and Windows binaries for `amd64` and `arm64`, creates checksums, and attaches the artifacts to the GitHub Release.
