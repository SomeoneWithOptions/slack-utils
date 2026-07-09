# slack-utils

General Slack utility CLI. Today it supports exporting message history for a single Slack channel; the command layout is ready for more Slack utilities under resources like `channels` and `users`.

## Sensitive/generated files

The exporter can create local files containing private Slack data:

- `export.json` contains exported channel messages.
- `users.json` is a local user cache that can contain Slack user IDs and emails.

These files are intentionally ignored by git and excluded from Docker build contexts. Do not commit real exports, caches, or Slack tokens.

## Build the CLI

From the repository root, use the build helper so the binary is compiled with the latest stable Go toolchain published by go.dev:

```bash
./scripts/build-binary
```

The helper requires Docker and curl, then pulls `golang:<latest>-alpine` before building. To override the output path or pin a version for debugging:

```bash
./scripts/build-binary /tmp/slack-utils
GO_VERSION=1.24.5 ./scripts/build-binary
```

## Build the Docker image

If you want to build the container locally, run the following from the repository root:

```bash
docker build --pull -t slack-utils .
```

This uses the multi-stage `Dockerfile` in the repo to compile the Go binary with the latest `golang:alpine` base image and package it into a minimal runtime image tagged as `slack-utils`. You can pass `--build-arg GO_IMAGE=golang:<version>-alpine` when a specific compiler image is required.

## Usage

The CLI uses a resource/action structure:

```bash
slack-utils <resource> <action> [flags]
```

Current command:

```bash
slack-utils channels export -channel C1234567890
```

### Export a channel

1. Set a Slack API token that has access to the target channel:

   ```bash
   export SLACK_TOKEN=your-slack-token
   export CHANNEL=C1234567890
   ```

2. Run the export:

   ```bash
   slack-utils channels export \
     -channel "$CHANNEL" \
     -o /tmp/export.json
   ```

Useful flags:

```bash
slack-utils channels export \
  -channel "$CHANNEL" \
  -since 7d \
  -to 2024-05-01 \
  -limit 50 \
  -no-replies \
  -q \
  -o /tmp/export.json
```

### Run with Docker

Create the output file (or make sure it exists) so Docker can bind-mount it:

```bash
touch /tmp/export.json
```

Then run:

```bash
docker run --rm \
  -e SLACK_TOKEN="$SLACK_TOKEN" \
  -v /tmp/export.json:/app/export.json \
  slack-utils \
  channels export -channel "$CHANNEL"
```

## Sample export.json output

When the exporter finishes, `/tmp/export.json` will contain a JSON document similar to:

```json
{
  "channel_id": "C1234567890",
  "channel_name": "project-updates",
  "exported_at": "2024-05-18T12:34:56Z",
  "messages": [
    {
      "user": "alice",
      "message": "Heads up on today's deploy.",
      "date": "2024-05-17T16:22:45Z",
      "replies": [
        {
          "user": "bob",
          "message": "Thanks for the update!",
          "date": "2024-05-17T16:40:03Z"
        }
      ]
    },
    {
      "user": "carol",
      "message": "Reminder: stand-up moves to 11am tomorrow.",
      "date": "2024-05-17T18:05:12Z"
    }
  ]
}
```

The top-level `messages` array includes root messages. Replies are nested inside their parent message under the `replies` field.
