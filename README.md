# slackChannelScrap

Tooling around the `slack-exporter` image to pull message history for a single
Slack channel and save it locally as JSON.


## Build the Docker image

If you want to build the container locally (instead of pulling a pre-built image),
run the following from the repository root:

```bash
docker build -t slack-exporter .
```

This uses the multi-stage `Dockerfile` in the repo to compile the Go binary and
package it into a minimal runtime image tagged as `slack-exporter`.

## Usage

1. Set the target channel ID and a Slack API token that has access to it:

   ```bash
   export TOKEN=your-slack-token
   export CHANNEL=C1234567890
   ```

2. Create the output file (or make sure it exists) so Docker can bind-mount it:

   ```bash
   touch /tmp/export.json
   ```

3. Run the exporter container to dump the conversation history into that file:

   ```bash
   docker run --rm \
     -e SLACK_TOKEN="$TOKEN" \
     -v /tmp/export.json:/app/export.json \
     slack-exporter \
     -channel "$CHANNEL"
   ```
