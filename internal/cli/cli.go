// Package cli implements the slack-utils command-line interface.
package cli

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/SomeoneWithOptions/slack-utils/internal/applog"
	"github.com/SomeoneWithOptions/slack-utils/internal/export"
)

const cliName = "slack-utils"

// Run dispatches the CLI with the provided args (excluding the program name).
func Run(args []string) {
	if len(args) == 0 {
		printRootUsage(os.Stderr)
		os.Exit(2)
	}

	switch args[0] {
	case "-h", "--help", "help":
		printRootUsage(os.Stdout)
	case "channels", "channel":
		runChannelsCommand(args[1:])
	default:
		if strings.HasPrefix(args[0], "-") {
			fmt.Fprintf(os.Stderr, "root-level export flags are deprecated; use `%s channels export` instead.\n\n", cliName)
			runChannelsExport(args)
			return
		}
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", args[0])
		printRootUsage(os.Stderr)
		os.Exit(2)
	}
}

func runChannelsCommand(args []string) {
	if len(args) == 0 {
		printChannelsUsage(os.Stderr)
		os.Exit(2)
	}

	switch args[0] {
	case "-h", "--help", "help":
		printChannelsUsage(os.Stdout)
	case "export":
		runChannelsExport(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown channels action %q\n\n", args[0])
		printChannelsUsage(os.Stderr)
		os.Exit(2)
	}
}

func printRootUsage(out *os.File) {
	fmt.Fprintf(out, `Slack utilities.

Usage:
  %[1]s <resource> <action> [flags]

Commands:
  %[1]s channels export   Export a channel's message history to JSON

Run "%[1]s <resource> <action> -h" for command-specific flags.
`, cliName)
}

func printChannelsUsage(out *os.File) {
	fmt.Fprintf(out, `Slack channel utilities.

Usage:
  %[1]s channels <action> [flags]

Actions:
  export   Export a channel's message history to JSON

Run "%[1]s channels export -h" for export flags.
`, cliName)
}

func runChannelsExport(args []string) {
	var (
		opts  export.Options
		quiet bool
	)
	fs := flag.NewFlagSet("channels export", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&opts.ChannelID, "channel", "", "Slack channel ID (e.g., C123...)")
	fs.DurationVar(&opts.Delay, "delay", time.Second, "Delay between requests")
	fs.StringVar(&opts.Since, "since", "", "Only include messages on or after this time (RFC3339, YYYY-MM-DD, or relative duration like 7d/24h)")
	fs.StringVar(&opts.To, "to", "", "Only include messages on or before this time (RFC3339 or YYYY-MM-DD)")
	fs.StringVar(&opts.Output, "o", export.DefaultOutputPath, "Path to write the export JSON")
	fs.StringVar(&opts.Output, "output", export.DefaultOutputPath, "Path to write the export JSON")
	fs.IntVar(&opts.Limit, "limit", 0, "Maximum number of root messages to export (0 = no limit)")
	fs.BoolVar(&opts.NoReplies, "no-replies", false, "Skip fetching thread replies (export root messages only)")
	fs.BoolVar(&quiet, "quiet", false, "Suppress progress logs (errors still go to stderr)")
	fs.BoolVar(&quiet, "q", false, "Suppress progress logs (errors still go to stderr)")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage:\n  %s channels export -channel C123 [flags]\n\nFlags:\n", cliName)
		fs.PrintDefaults()
	}

	if hasHelpArg(args) {
		fs.SetOutput(os.Stdout)
		fs.Usage()
		return
	}
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "unexpected argument %q\n\n", fs.Arg(0))
		fs.Usage()
		os.Exit(2)
	}
	if opts.ChannelID == "" {
		fmt.Fprint(os.Stderr, "-channel is required\n\n")
		fs.Usage()
		os.Exit(2)
	}

	logger := applog.New()
	logger.Quiet = quiet
	opts.Log = logger
	opts.UserCachePath = export.DefaultUserCachePath
	opts.TokenEnv = export.DefaultTokenEnv

	export.Run(opts)
}

func hasHelpArg(args []string) bool {
	for _, arg := range args {
		if arg == "-h" || arg == "--help" {
			return true
		}
	}
	return len(args) == 1 && args[0] == "help"
}
