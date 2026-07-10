// Package cli implements the slack-utils command-line interface.
package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/SomeoneWithOptions/slack-utils/internal/applog"
	"github.com/SomeoneWithOptions/slack-utils/internal/export"
	"github.com/SomeoneWithOptions/slack-utils/internal/users"
	"github.com/slack-go/slack"
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
	case "users", "user":
		runUsersCommand(args[1:])
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
  %[1]s users init        Initialize users.json with all workspace users
  %[1]s users update      Add missing workspace users to users.json

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

func runUsersCommand(args []string) {
	if len(args) == 0 {
		printUsersUsage(os.Stderr)
		os.Exit(2)
	}

	switch args[0] {
	case "-h", "--help", "help":
		printUsersUsage(os.Stdout)
	case "init":
		runUsersInit(args[1:])
	case "update":
		runUsersUpdate(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown users action %q\n\n", args[0])
		printUsersUsage(os.Stderr)
		os.Exit(2)
	}
}

func printUsersUsage(out *os.File) {
	fmt.Fprintf(out, `Slack user utilities.

Usage:
  %[1]s users <action> [flags]

Actions:
  init     Initialize users.json with all workspace users (never overwrites)
  update   Add workspace users missing from an existing users.json

Run "%[1]s users <action> -h" for action-specific flags.
`, cliName)
}

func runUsersInit(args []string) {
	var (
		path   string
		delay  time.Duration
		teamID string
		quiet  bool
	)
	fs := flag.NewFlagSet("users init", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&path, "o", users.DefaultCachePath, "Path to create for the user cache")
	fs.StringVar(&path, "output", users.DefaultCachePath, "Path to create for the user cache")
	fs.DurationVar(&delay, "delay", users.DefaultInitDelay, "Delay between users.list pages")
	fs.StringVar(&teamID, "team", "", "Workspace team ID (required only for Enterprise Grid org tokens)")
	fs.BoolVar(&quiet, "quiet", false, "Suppress progress logs (errors still go to stderr)")
	fs.BoolVar(&quiet, "q", false, "Suppress progress logs (errors still go to stderr)")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage:\n  %s users init [flags]\n\nFlags:\n", cliName)
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
	path = strings.TrimSpace(path)
	if path == "" {
		applog.Fail(fmt.Errorf("-o/-output path must not be empty"))
	}
	if delay < 0 {
		applog.Fail(fmt.Errorf("-delay must be >= 0"))
	}

	logger := applog.New()
	logger.Quiet = quiet
	exists, err := users.CacheExists(path)
	if err != nil {
		applog.Fail(err)
	}
	if exists {
		logger.Logf("user cache %s already exists; nothing to do", path)
		return
	}

	token := strings.TrimSpace(os.Getenv(users.DefaultTokenEnv))
	if token == "" {
		applog.Fail(fmt.Errorf("environment variable %s must be set to a Slack token with users:read (and users:read.email for emails)", users.DefaultTokenEnv))
	}

	retryConfig := slack.DefaultRetryConfig()
	retryConfig.MaxRetries = 3
	retryConfig.Handlers = append(slack.ConnectionOnlyRetryHandlers(), slack.NewServerErrorRetryHandler(retryConfig))
	api := slack.New(token, slack.OptionRetryConfig(retryConfig))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	result, err := users.Initialize(ctx, users.InitOptions{
		Path:     path,
		Delay:    delay,
		TeamID:   teamID,
		TokenEnv: users.DefaultTokenEnv,
		API:      api,
		Log:      logger,
	})
	if err != nil {
		applog.Fail(err)
	}
	if !result.AlreadyExists {
		fmt.Printf("wrote %s (%d users)\n", path, result.Users)
	}
}

func runUsersUpdate(args []string) {
	var (
		path   string
		delay  time.Duration
		teamID string
		quiet  bool
	)
	fs := flag.NewFlagSet("users update", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&path, "o", users.DefaultCachePath, "Path to the existing user cache")
	fs.StringVar(&path, "output", users.DefaultCachePath, "Path to the existing user cache")
	fs.DurationVar(&delay, "delay", users.DefaultInitDelay, "Delay between users.list pages")
	fs.StringVar(&teamID, "team", "", "Workspace team ID (required only for Enterprise Grid org tokens)")
	fs.BoolVar(&quiet, "quiet", false, "Suppress progress logs (errors still go to stderr)")
	fs.BoolVar(&quiet, "q", false, "Suppress progress logs (errors still go to stderr)")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage:\n  %s users update [flags]\n\nFlags:\n", cliName)
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
	path = strings.TrimSpace(path)
	if path == "" {
		applog.Fail(fmt.Errorf("-o/-output path must not be empty"))
	}
	if delay < 0 {
		applog.Fail(fmt.Errorf("-delay must be >= 0"))
	}

	logger := applog.New()
	logger.Quiet = quiet
	exists, err := users.CacheExists(path)
	if err != nil {
		applog.Fail(err)
	}
	if !exists {
		applog.Fail(fmt.Errorf("user cache %s does not exist; run `%s users init -output %q` first", path, cliName, path))
	}

	token := strings.TrimSpace(os.Getenv(users.DefaultTokenEnv))
	if token == "" {
		applog.Fail(fmt.Errorf("environment variable %s must be set to a Slack token with users:read (and users:read.email for emails)", users.DefaultTokenEnv))
	}

	retryConfig := slack.DefaultRetryConfig()
	retryConfig.MaxRetries = 3
	retryConfig.Handlers = append(slack.ConnectionOnlyRetryHandlers(), slack.NewServerErrorRetryHandler(retryConfig))
	api := slack.New(token, slack.OptionRetryConfig(retryConfig))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	result, err := users.Update(ctx, users.UpdateOptions{
		Path:     path,
		Delay:    delay,
		TeamID:   teamID,
		TokenEnv: users.DefaultTokenEnv,
		API:      api,
		Log:      logger,
	})
	if err != nil {
		applog.Fail(err)
	}
	if result.Added == 0 {
		fmt.Printf("user cache %s is already up to date (%d users)\n", path, result.Total)
		return
	}
	fmt.Printf("updated %s (%d users added, %d total)\n", path, result.Added, result.Total)
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
