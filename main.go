// Command slack-utils is a small CLI for Slack utilities such as channel export.
package main

import (
	"os"

	"github.com/SomeoneWithOptions/slack-utils/internal/cli"
)

func main() {
	cli.Run(os.Args[1:])
}
