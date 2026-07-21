// Package cli implements the slack-utils command-line interface.
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/SomeoneWithOptions/slack-utils/internal/applog"
	"github.com/SomeoneWithOptions/slack-utils/internal/export"
	"github.com/SomeoneWithOptions/slack-utils/internal/timestamp"
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
	case "conversations":
		runConversationsCommand(args[1:])
	case "users", "user":
		runUsersCommand(args[1:])
	default:
		if strings.HasPrefix(args[0], "-") {
			fmt.Fprintf(os.Stderr, "root-level export flags are deprecated; use `%s conversations export` instead.\n\n", cliName)
			runConversationsExport(args)
			return
		}
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", args[0])
		printRootUsage(os.Stderr)
		os.Exit(2)
	}
}

func runConversationsCommand(args []string) {
	if len(args) == 0 {
		printConversationsUsage(os.Stderr)
		os.Exit(2)
	}

	switch args[0] {
	case "-h", "--help", "help":
		printConversationsUsage(os.Stdout)
	case "export":
		runConversationsExport(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown conversations action %q\n\n", args[0])
		printConversationsUsage(os.Stderr)
		os.Exit(2)
	}
}

func printRootUsage(out *os.File) {
	fmt.Fprintf(out, `Slack utilities.

Usage:
  %[1]s <command> [subcommand...] [flags]

Commands:
  %[1]s conversations export  Export a conversation's message history to JSON
  %[1]s users lookup          Find a Slack user ID by email address
  %[1]s users info            Get all available information for a Slack user ID
  %[1]s users cache init      Initialize users.json with all workspace users
  %[1]s users cache update    Add missing workspace users to users.json

Run "%[1]s <command> [subcommand...] -h" for command-specific help.
`, cliName)
}

func printConversationsUsage(out *os.File) {
	fmt.Fprintf(out, `Slack conversation utilities.

Usage:
  %[1]s conversations <action> [flags]

Actions:
  export   Export one conversation's message history and thread replies to simplified JSON

Run "%[1]s conversations export -h" for export flags.
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
	case "lookup":
		runUsersLookup(args[1:])
	case "info":
		runUsersInfo(args[1:])
	case "cache":
		runUsersCacheCommand(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown users command %q\n\n", args[0])
		printUsersUsage(os.Stderr)
		os.Exit(2)
	}
}

func printUsersUsage(out *os.File) {
	fmt.Fprintf(out, `Slack user utilities.

Usage:
  %[1]s users <command> [flags]

Commands:
  lookup   Find a Slack user ID by email address
  info     Get all available information for a Slack user ID
  cache    Manage the local workspace user cache

Run "%[1]s users <command> -h" for command-specific help.
`, cliName)
}

func runUsersLookup(args []string) {
	var email string
	fs := flag.NewFlagSet("users lookup", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&email, "email", "", "Email address of the Slack user")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage:\n  %s users lookup -email person@example.com\n\nFlags:\n", cliName)
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
	if strings.TrimSpace(email) == "" {
		fmt.Fprint(os.Stderr, "-email is required\n\n")
		fs.Usage()
		os.Exit(2)
	}

	token := strings.TrimSpace(os.Getenv(users.DefaultTokenEnv))
	if token == "" {
		applog.Fail(fmt.Errorf("environment variable %s must be set to a Slack token with users:read.email", users.DefaultTokenEnv))
	}
	retryConfig := slack.DefaultRetryConfig()
	retryConfig.MaxRetries = 3
	retryConfig.Handlers = append(slack.ConnectionOnlyRetryHandlers(), slack.NewServerErrorRetryHandler(retryConfig))
	api := slack.New(token, slack.OptionRetryConfig(retryConfig))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	userID, err := users.LookupByEmail(ctx, api, email, users.DefaultTokenEnv)
	if err != nil {
		applog.Fail(err)
	}
	fmt.Println(userID)
}

func runUsersInfo(args []string) {
	var userID string
	fs := flag.NewFlagSet("users info", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&userID, "id", "", "Slack user ID (e.g., U123... or W123...)")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage:\n  %s users info -id U1234567890\n\nFlags:\n", cliName)
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
	if strings.TrimSpace(userID) == "" {
		fmt.Fprint(os.Stderr, "-id is required\n\n")
		fs.Usage()
		os.Exit(2)
	}

	token := strings.TrimSpace(os.Getenv(users.DefaultTokenEnv))
	if token == "" {
		applog.Fail(fmt.Errorf("environment variable %s must be set to a Slack token with users:read", users.DefaultTokenEnv))
	}
	retryConfig := slack.DefaultRetryConfig()
	retryConfig.MaxRetries = 3
	retryConfig.Handlers = append(slack.ConnectionOnlyRetryHandlers(), slack.NewServerErrorRetryHandler(retryConfig))
	api := slack.New(token, slack.OptionRetryConfig(retryConfig))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	user, err := users.Info(ctx, api, userID, users.DefaultTokenEnv)
	if err != nil {
		applog.Fail(err)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(user); err != nil {
		applog.Fail(fmt.Errorf("write user info: %w", err))
	}
}

func runUsersCacheCommand(args []string) {
	if len(args) == 0 {
		printUsersCacheUsage(os.Stderr)
		os.Exit(2)
	}

	switch args[0] {
	case "-h", "--help", "help":
		printUsersCacheUsage(os.Stdout)
	case "init":
		runUsersInit(args[1:])
	case "update":
		runUsersUpdate(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown users cache action %q\n\n", args[0])
		printUsersCacheUsage(os.Stderr)
		os.Exit(2)
	}
}

func printUsersCacheUsage(out *os.File) {
	fmt.Fprintf(out, `Slack user cache utilities.

Usage:
  %[1]s users cache <action> [flags]

Actions:
  init     Initialize users.json with all workspace users (never overwrites)
  update   Add workspace users missing from an existing users.json

Run "%[1]s users cache <action> -h" for action-specific flags.
`, cliName)
}

func runUsersInit(args []string) {
	var (
		path   string
		delay  time.Duration
		teamID string
		quiet  bool
	)
	fs := flag.NewFlagSet("users cache init", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&path, "o", users.DefaultCachePath, "Path to create for the user cache")
	fs.StringVar(&path, "output", users.DefaultCachePath, "Path to create for the user cache")
	fs.DurationVar(&delay, "delay", users.DefaultInitDelay, "Delay between users.list pages")
	fs.StringVar(&teamID, "team", "", "Workspace team ID (required only for Enterprise Grid org tokens)")
	fs.BoolVar(&quiet, "quiet", false, "Suppress progress logs (errors still go to stderr)")
	fs.BoolVar(&quiet, "q", false, "Suppress progress logs (errors still go to stderr)")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage:\n  %s users cache init [flags]\n\nFlags:\n", cliName)
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
	fs := flag.NewFlagSet("users cache update", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&path, "o", users.DefaultCachePath, "Path to the existing user cache")
	fs.StringVar(&path, "output", users.DefaultCachePath, "Path to the existing user cache")
	fs.DurationVar(&delay, "delay", users.DefaultInitDelay, "Delay between users.list pages")
	fs.StringVar(&teamID, "team", "", "Workspace team ID (required only for Enterprise Grid org tokens)")
	fs.BoolVar(&quiet, "quiet", false, "Suppress progress logs (errors still go to stderr)")
	fs.BoolVar(&quiet, "q", false, "Suppress progress logs (errors still go to stderr)")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage:\n  %s users cache update [flags]\n\nFlags:\n", cliName)
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
		applog.Fail(fmt.Errorf("user cache %s does not exist; run `%s users cache init -output %q` first", path, cliName, path))
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

func runConversationsExport(args []string) {
	var (
		opts      export.Options
		threadURL string
		quiet     bool
	)
	fs := flag.NewFlagSet("conversations export", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&opts.ConversationID, "channel", "", "Slack conversation ID (e.g., C123..., G123..., or D123...)")
	fs.StringVar(&threadURL, "url", "", "Slack thread URL to export instead of a whole conversation")
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
		fmt.Fprintf(fs.Output(), "Usage:\n  %s conversations export (-channel C123 | -url https://workspace.slack.com/archives/C123/p...) [flags]\n\nFlags:\n", cliName)
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
	threadURL = strings.TrimSpace(threadURL)
	if threadURL != "" {
		if opts.ConversationID != "" || opts.Since != "" || opts.To != "" {
			applog.Fail(fmt.Errorf("-url cannot be combined with -channel, -since, or -to"))
		}
		var err error
		opts.ConversationID, opts.ThreadTimestamp, err = parseThreadURL(threadURL)
		if err != nil {
			applog.Fail(err)
		}
	}
	if opts.ConversationID == "" {
		fmt.Fprint(os.Stderr, "-channel or -url is required\n\n")
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

func parseThreadURL(raw string) (string, string, error) {
	u, err := url.ParseRequestURI(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", "", fmt.Errorf("invalid Slack thread URL %q", raw)
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 3 || parts[0] != "archives" || parts[1] == "" || len(parts[2]) < 2 || parts[2][0] != 'p' {
		return "", "", fmt.Errorf("invalid Slack thread URL %q: expected /archives/<conversation>/p<timestamp>", raw)
	}

	compact := parts[2][1:]
	if len(compact) <= 6 || strings.Trim(compact, "0123456789") != "" {
		return "", "", fmt.Errorf("invalid Slack message timestamp in URL %q", raw)
	}
	threadTS := u.Query().Get("thread_ts")
	if threadTS == "" {
		threadTS = compact[:len(compact)-6] + "." + compact[len(compact)-6:]
	}
	if _, err := timestamp.ParseSlack(threadTS); err != nil {
		return "", "", fmt.Errorf("invalid Slack thread timestamp in URL %q: %w", raw, err)
	}
	return parts[1], threadTS, nil
}

func hasHelpArg(args []string) bool {
	for _, arg := range args {
		if arg == "-h" || arg == "--help" {
			return true
		}
	}
	return len(args) == 1 && args[0] == "help"
}
