package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/slack-go/slack"
)

type Export struct {
	ChannelID   string          `json:"channel_id"`
	ChannelName string          `json:"channel_name"`
	ExportedAt  time.Time       `json:"exported_at"`
	Messages    []SimpleMessage `json:"messages"`
}

type SimpleMessage struct {
	User    string          `json:"user"`
	Message string          `json:"message"`
	Date    string          `json:"date"`
	Replies []SimpleMessage `json:"replies,omitempty"`
}

const (
	cliName        = "slack-utils"
	exportFilePath = "./export.json"
	userCacheFile  = "./users.json"
	slackTokenEnv  = "SLACK_TOKEN"

	slackMethodConversationsHistory = "conversations.history"
	slackMethodConversationsInfo    = "conversations.info"
	slackMethodConversationsReplies = "conversations.replies"
	slackMethodUsersInfo            = "users.info"
)

var (
	stdoutLog         = log.New(os.Stdout, "", log.LstdFlags)
	quietMode         bool
	slackScopePattern = regexp.MustCompile(`\b[a-zA-Z0-9.-]+:[a-zA-Z0-9.:-]+\b`)
)

func logf(format string, args ...interface{}) {
	if quietMode {
		return
	}
	stdoutLog.Printf(format, args...)
}
func main() {
	runCLI(os.Args[1:])
}

func runCLI(args []string) {
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

type channelExportOptions struct {
	channelID string
	delay     time.Duration
	sinceStr  string
	toStr     string
	out       string
	limit     int
	noReplies bool
}

func runChannelsExport(args []string) {
	var opts channelExportOptions
	fs := flag.NewFlagSet("channels export", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&opts.channelID, "channel", "", "Slack channel ID (e.g., C123...)")
	fs.DurationVar(&opts.delay, "delay", time.Second, "Delay between requests")
	fs.StringVar(&opts.sinceStr, "since", "", "Only include messages on or after this time (RFC3339, YYYY-MM-DD, or relative duration like 7d/24h)")
	fs.StringVar(&opts.toStr, "to", "", "Only include messages on or before this time (RFC3339 or YYYY-MM-DD)")
	fs.StringVar(&opts.out, "o", exportFilePath, "Path to write the export JSON")
	fs.StringVar(&opts.out, "output", exportFilePath, "Path to write the export JSON")
	fs.IntVar(&opts.limit, "limit", 0, "Maximum number of root messages to export (0 = no limit)")
	fs.BoolVar(&opts.noReplies, "no-replies", false, "Skip fetching thread replies (export root messages only)")
	fs.BoolVar(&quietMode, "quiet", false, "Suppress progress logs (errors still go to stderr)")
	fs.BoolVar(&quietMode, "q", false, "Suppress progress logs (errors still go to stderr)")
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
	if opts.channelID == "" {
		fmt.Fprint(os.Stderr, "-channel is required\n\n")
		fs.Usage()
		os.Exit(2)
	}

	runChannelsExportWithOptions(opts)
}

func hasHelpArg(args []string) bool {
	for _, arg := range args {
		if arg == "-h" || arg == "--help" {
			return true
		}
	}
	return len(args) == 1 && args[0] == "help"
}

type slackAPIErrorDetails struct {
	Operation      string
	Method         string
	RequiredScopes []string
	Hints          []string
}

type slackAPIError struct {
	err     error
	details slackAPIErrorDetails
}

func describeSlackAPIError(err error, details slackAPIErrorDetails) error {
	if err == nil {
		return nil
	}
	return &slackAPIError{err: err, details: details}
}

func (e *slackAPIError) Error() string {
	operation := strings.TrimSpace(e.details.Operation)
	if operation == "" {
		operation = "Slack API request"
	}

	var b strings.Builder
	if method := strings.TrimSpace(e.details.Method); method != "" {
		fmt.Fprintf(&b, "%s failed (Slack API method: %s)", operation, method)
	} else {
		fmt.Fprintf(&b, "%s failed", operation)
	}

	code, isSlackErr := slackErrorCode(e.err)
	if isSlackErr && code != "" {
		fmt.Fprintf(&b, ": %s", code)
	} else {
		fmt.Fprintf(&b, ": %v", e.err)
	}

	if isSlackErr && code == "missing_scope" {
		scopes := effectiveMissingScopes(e.err, e.details.RequiredScopes)
		if len(scopes) == 1 {
			fmt.Fprintf(&b, "\nmissing Slack OAuth scope: %s", scopes[0])
		} else if len(scopes) > 1 {
			b.WriteString("\nrequired Slack OAuth scope(s):")
			for _, scope := range scopes {
				fmt.Fprintf(&b, "\n  - %s", scope)
			}
		} else {
			b.WriteString("\nSlack reported a missing OAuth scope, but the response did not include the scope name.")
		}
	}

	metadata := collectSlackErrorMetadata(e.err)
	if len(metadata.messages) > 0 {
		b.WriteString("\nSlack response messages:")
		for _, msg := range metadata.messages {
			fmt.Fprintf(&b, "\n  - %s", msg)
		}
	}
	if len(metadata.warnings) > 0 {
		b.WriteString("\nSlack response warnings:")
		for _, warning := range metadata.warnings {
			fmt.Fprintf(&b, "\n  - %s", warning)
		}
	}
	if len(metadata.responseErrors) > 0 {
		b.WriteString("\nSlack response errors:")
		for _, responseErr := range metadata.responseErrors {
			fmt.Fprintf(&b, "\n  - %s", responseErr)
		}
	}

	hints := e.hints(code, isSlackErr)
	if len(hints) > 0 {
		b.WriteString("\nhow to fix:")
		for _, hint := range hints {
			fmt.Fprintf(&b, "\n  - %s", hint)
		}
	}

	return b.String()
}

func (e *slackAPIError) Unwrap() error {
	return e.err
}

func (e *slackAPIError) hints(code string, isSlackErr bool) []string {
	hints := append([]string{}, e.details.Hints...)

	if !isSlackErr {
		hints = append(hints,
			"Check your network connection and that Slack's API is reachable from this machine.",
			"Rerun with a larger -delay if Slack or a proxy is throttling requests.",
		)
		return uniqueStrings(hints)
	}

	switch code {
	case "missing_scope":
		hints = append(hints,
			"Add the missing scope under your Slack app's OAuth & Permissions page.",
			"Reinstall or reauthorize the Slack app so the token receives the new scope.",
			fmt.Sprintf("Update %s with the new token, then rerun the command.", slackTokenEnv),
		)
	case "invalid_auth", "not_authed", "token_revoked", "account_inactive":
		hints = append(hints,
			fmt.Sprintf("Verify %s is set to a valid, active Slack token.", slackTokenEnv),
			"If the Slack app was reinstalled or rotated, export the new token before rerunning.",
		)
	case "channel_not_found":
		hints = append(hints,
			"Verify -channel is a Slack channel ID such as C..., G..., or D... (not the channel name).",
			"Make sure the token can access the conversation; invite the app/user to the channel when needed.",
		)
	case "not_in_channel":
		hints = append(hints,
			"Invite the Slack app/user represented by the token to the channel, then rerun the command.",
		)
	case "no_permission":
		hints = append(hints,
			"Check that the Slack app has permission to access this workspace and conversation.",
		)
	}

	return uniqueStrings(hints)
}

type slackErrorMetadata struct {
	messages       []string
	warnings       []string
	responseErrors []string
}

func slackErrorCode(err error) (string, bool) {
	resp, ok := slackErrorResponse(err)
	if !ok {
		return "", false
	}
	return resp.Err, true
}

func slackErrorResponse(err error) (slack.SlackErrorResponse, bool) {
	var resp slack.SlackErrorResponse
	if errors.As(err, &resp) {
		return resp, true
	}
	return slack.SlackErrorResponse{}, false
}

func collectSlackErrorMetadata(err error) slackErrorMetadata {
	resp, ok := slackErrorResponse(err)
	if !ok {
		return slackErrorMetadata{}
	}
	metadata := slackErrorMetadata{
		messages: resp.ResponseMetadata.Messages,
		warnings: resp.ResponseMetadata.Warnings,
	}
	for _, responseErr := range resp.Errors {
		metadata.responseErrors = append(metadata.responseErrors, formatSlackResponseError(responseErr))
	}
	metadata.messages = uniqueStrings(metadata.messages)
	metadata.warnings = uniqueStrings(metadata.warnings)
	metadata.responseErrors = uniqueStrings(metadata.responseErrors)
	return metadata
}

func formatSlackResponseError(err slack.SlackResponseErrors) string {
	switch {
	case err.Message != nil:
		return *err.Message
	case err.AppsManifestCreateResponseError != nil:
		appErr := err.AppsManifestCreateResponseError
		parts := []string{}
		if appErr.Code != "" {
			parts = append(parts, appErr.Code)
		}
		if appErr.Pointer != "" {
			parts = append(parts, appErr.Pointer)
		}
		if appErr.Message != "" {
			parts = append(parts, appErr.Message)
		}
		return strings.Join(parts, ": ")
	case err.ConversationsInviteResponseError != nil:
		inviteErr := err.ConversationsInviteResponseError
		if inviteErr.User != "" {
			return fmt.Sprintf("user %s: %s", inviteErr.User, inviteErr.Error)
		}
		return inviteErr.Error
	default:
		return ""
	}
}

func effectiveMissingScopes(err error, fallback []string) []string {
	metadata := collectSlackErrorMetadata(err)
	var scopes []string
	texts := append([]string{}, metadata.messages...)
	texts = append(texts, metadata.warnings...)
	texts = append(texts, metadata.responseErrors...)
	for _, text := range texts {
		scopes = append(scopes, extractSlackScopes(text)...)
	}
	if len(scopes) == 0 {
		scopes = fallback
	}
	return uniqueStrings(scopes)
}

func extractSlackScopes(text string) []string {
	return uniqueStrings(slackScopePattern.FindAllString(text, -1))
}

func conversationScopesFor(channelID string, info *slack.Channel, publicScope, privateScope, imScope, mpimScope string) []string {
	if info != nil {
		switch {
		case info.IsIM:
			return scopeList(imScope)
		case info.IsMpIM:
			return scopeList(mpimScope)
		case info.IsPrivate || info.IsGroup:
			return scopeList(privateScope)
		case info.IsChannel:
			return scopeList(publicScope)
		}
	}

	channelID = strings.ToUpper(strings.TrimSpace(channelID))
	switch {
	case strings.HasPrefix(channelID, "C"):
		return scopeList(publicScope)
	case strings.HasPrefix(channelID, "D"):
		return scopeList(imScope)
	case strings.HasPrefix(channelID, "G"):
		return scopeList(privateScope, mpimScope)
	default:
		return scopeList(publicScope, privateScope, imScope, mpimScope)
	}
}

func conversationScopeHints(channelID, privateScope, mpimScope string) []string {
	channelID = strings.ToUpper(strings.TrimSpace(channelID))
	if !strings.HasPrefix(channelID, "G") {
		return nil
	}
	return []string{fmt.Sprintf("G... conversation IDs can be private channels or multi-person DMs; use %s for private channels or %s for multi-person DMs.", privateScope, mpimScope)}
}

func scopeList(scopes ...string) []string {
	var out []string
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if scope != "" {
			out = append(out, scope)
		}
	}
	return uniqueStrings(out)
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func runChannelsExportWithOptions(opts channelExportOptions) {
	token := strings.TrimSpace(os.Getenv(slackTokenEnv))
	if token == "" {
		fail(fmt.Errorf("environment variable %s must be set to a Slack token with the required OAuth scopes", slackTokenEnv))
	}
	out := strings.TrimSpace(opts.out)
	if out == "" {
		fail(fmt.Errorf("-o/-output path must not be empty"))
	}
	if opts.limit < 0 {
		fail(fmt.Errorf("-limit must be >= 0"))
	}

	oldest, err := parseTimeBound(opts.sinceStr, false)
	if err != nil {
		fail(fmt.Errorf("invalid -since value %q: %w", opts.sinceStr, err))
	}
	latest, err := parseTimeBound(opts.toStr, true)
	if err != nil {
		fail(fmt.Errorf("invalid -to value %q: %w", opts.toStr, err))
	}
	if oldest != "" && latest != "" {
		oldestTime, errOldest := parseSlackTimestamp(oldest)
		latestTime, errLatest := parseSlackTimestamp(latest)
		if errOldest == nil && errLatest == nil && oldestTime.After(latestTime) {
			fail(fmt.Errorf("-since (%s) must be before or equal to -to (%s)", opts.sinceStr, opts.toStr))
		}
	}

	userCachePath := userCacheFile

	api := slack.New(token)

	ctx := context.Background()
	logf("starting export for channel %s with request delay %s", opts.channelID, opts.delay)
	if oldest != "" || latest != "" {
		logf("time range filter: since=%s to=%s", formatBoundForLog(opts.sinceStr, oldest), formatBoundForLog(opts.toStr, latest))
	}
	if opts.limit > 0 {
		logf("message limit: %d root messages", opts.limit)
	}
	if opts.noReplies {
		logf("thread replies: disabled (-no-replies)")
	}
	logf("export destination: %s", out)
	logf("user cache file: %s", userCachePath)
	resolver, err := NewUserResolver(ctx, api, userCachePath, opts.delay)
	if err != nil {
		fail(fmt.Errorf("initialize user resolver using cache %s: %w", userCachePath, err))
	}
	defer func() {
		if err := resolver.Save(); err != nil {
			fail(fmt.Errorf("save user cache %s: %w", userCachePath, err))
		}
	}()

	info, err := api.GetConversationInfoContext(ctx, &slack.GetConversationInfoInput{ChannelID: opts.channelID, IncludeLocale: false})
	channelName := ""
	if err != nil {
		warn(describeSlackAPIError(err, slackAPIErrorDetails{
			Operation:      fmt.Sprintf("resolve channel information for %s", opts.channelID),
			Method:         slackMethodConversationsInfo,
			RequiredScopes: conversationScopesFor(opts.channelID, nil, "channels:read", "groups:read", "im:read", "mpim:read"),
			Hints: append([]string{
				"Channel name lookup is optional; export will continue with only the channel ID.",
			}, conversationScopeHints(opts.channelID, "groups:read", "mpim:read")...),
		}))
	} else if info != nil {
		channelName = info.Name
		if channelName != "" {
			logf("resolved channel name: %s", channelName)
		} else {
			logf("channel information retrieved without a name")
		}
	} else {
		logf("could not retrieve channel info, continuing with ID only")
	}
	historyScopes := conversationScopesFor(opts.channelID, info, "channels:history", "groups:history", "im:history", "mpim:history")

	var all []slack.Message
	cursor := ""
	for {
		pageLimit := 200
		if opts.limit > 0 {
			remaining := opts.limit - countRootMessages(all)
			if remaining <= 0 {
				logf("reached message limit of %d root messages; stopping history fetch", opts.limit)
				break
			}
			if remaining < pageLimit {
				pageLimit = remaining
			}
		}
		logf("requesting conversation history (cursor=%q, limit=%d)", cursor, pageLimit)
		h, err := getHistory(ctx, api, opts.channelID, cursor, oldest, latest, pageLimit)
		if err != nil {
			fail(describeSlackAPIError(err, slackAPIErrorDetails{
				Operation:      fmt.Sprintf("fetch conversation history for %s", opts.channelID),
				Method:         slackMethodConversationsHistory,
				RequiredScopes: historyScopes,
				Hints:          conversationScopeHints(opts.channelID, "groups:history", "mpim:history"),
			}))
		}
		if h == nil {
			fail(fmt.Errorf("fetch conversation history for %s failed: Slack returned an empty response", opts.channelID))
		}
		logf("received %d messages", len(h.Messages))
		all = append(all, h.Messages...)
		if opts.limit > 0 && countRootMessages(all) >= opts.limit {
			all = trimToRootLimit(all, opts.limit)
			logf("reached message limit of %d root messages; collected %d raw messages", opts.limit, len(all))
			break
		}
		if h.ResponseMetaData.NextCursor == "" {
			logf("no more history pages; collected %d messages", len(all))
			break
		}
		cursor = h.ResponseMetaData.NextCursor
		if opts.delay > 0 {
			logf("waiting %s before requesting next history page", opts.delay)
		}
		time.Sleep(opts.delay)
	}

	replyMap := make(map[string][]slack.Message)
	if opts.noReplies {
		logf("skipping thread reply fetch (-no-replies)")
	} else {
		rootMessages := make([]slack.Message, 0, len(all))
		for _, m := range all {
			if m.ThreadTimestamp != "" && m.Timestamp != m.ThreadTimestamp {
				continue
			}
			if m.ReplyCount == 0 && m.ThreadTimestamp == "" {
				continue
			}
			rootMessages = append(rootMessages, m)
		}

		logf("processing replies for %d root messages", len(rootMessages))
		for i, m := range rootMessages {
			ts := threadTimestamp(m)
			logf("fetching replies for thread %s (%d/%d)", ts, i+1, len(rootMessages))
			replies, err := fetchReplies(ctx, api, opts.channelID, ts, opts.delay, historyScopes)
			if err != nil {
				fail(err)
			}
			if len(replies) > 0 {
				logf("retrieved %d replies for thread %s", len(replies), ts)
				replyMap[ts] = replies
				time.Sleep(opts.delay)
				if opts.delay > 0 {
					logf("waiting %s before next thread request", opts.delay)
				}
			} else {
				logf("no replies returned for thread %s", ts)
			}
			logf("Processed main message %d/%d", i+1, len(rootMessages))
		}
	}

	exp := Export{
		ChannelID:   opts.channelID,
		ChannelName: channelName,
		ExportedAt:  time.Now().UTC(),
		Messages:    buildSimpleMessages(all, replyMap, resolver),
	}

	if dir := filepath.Dir(out); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fail(fmt.Errorf("create output directory %s: %w", dir, err))
		}
	}
	f, err := os.Create(out)
	if err != nil {
		fail(fmt.Errorf("create output file %s: %w", out, err))
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	logf("writing %d exported messages to %s", len(exp.Messages), out)
	if err := enc.Encode(exp); err != nil {
		fail(fmt.Errorf("write export JSON to %s: %w", out, err))
	}
	fmt.Println("wrote", out)
}

func getHistory(ctx context.Context, api *slack.Client, channelID, cursor, oldest, latest string, limit int) (*slack.GetConversationHistoryResponse, error) {
	if limit <= 0 {
		limit = 200
	}
	for {
		h, err := api.GetConversationHistoryContext(ctx, &slack.GetConversationHistoryParameters{
			ChannelID:          channelID,
			Cursor:             cursor,
			Oldest:             oldest,
			Latest:             latest,
			Limit:              limit,
			IncludeAllMetadata: true,
			Inclusive:          true,
		})
		if rl := retryAfter(err); rl > 0 {
			logf("rate limited while fetching history; retrying in %d seconds", rl)
			time.Sleep(time.Duration(rl) * time.Second)
			continue
		}
		if err != nil {
			logf("history request returned an error: %v", err)
		}
		return h, err
	}
}

// parseTimeBound converts a CLI time bound into a Slack timestamp string (seconds.nanoseconds).
// endOfDay is used for date-only values so -to 2024-05-01 includes the whole day.
func parseTimeBound(value string, endOfDay bool) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}

	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return formatSlackTimestamp(t.UTC()), nil
	}
	if t, err := time.ParseInLocation("2006-01-02", value, time.UTC); err == nil {
		if endOfDay {
			t = t.Add(24*time.Hour - time.Nanosecond)
		}
		return formatSlackTimestamp(t.UTC()), nil
	}
	if !endOfDay {
		if d, err := parseRelativeDuration(value); err == nil {
			return formatSlackTimestamp(time.Now().UTC().Add(-d)), nil
		}
	}
	return "", fmt.Errorf("use RFC3339, YYYY-MM-DD%s", relativeHint(endOfDay))
}

func relativeHint(endOfDay bool) string {
	if endOfDay {
		return ""
	}
	return ", or a relative duration like 7d/24h"
}

func parseRelativeDuration(value string) (time.Duration, error) {
	if len(value) < 2 {
		return 0, fmt.Errorf("invalid relative duration")
	}
	suffix := value[len(value)-1]
	amountStr := value[:len(value)-1]
	amount, err := strconv.ParseFloat(amountStr, 64)
	if err != nil || amount < 0 {
		return 0, fmt.Errorf("invalid relative duration")
	}
	switch suffix {
	case 's', 'S':
		return time.Duration(amount * float64(time.Second)), nil
	case 'm', 'M':
		return time.Duration(amount * float64(time.Minute)), nil
	case 'h', 'H':
		return time.Duration(amount * float64(time.Hour)), nil
	case 'd', 'D':
		return time.Duration(amount * float64(24*time.Hour)), nil
	case 'w', 'W':
		return time.Duration(amount * float64(7*24*time.Hour)), nil
	default:
		return 0, fmt.Errorf("unsupported duration suffix %q", string(suffix))
	}
}

func formatSlackTimestamp(t time.Time) string {
	secs := t.Unix()
	nsecs := t.Nanosecond()
	if nsecs == 0 {
		return strconv.FormatInt(secs, 10)
	}
	return fmt.Sprintf("%d.%09d", secs, nsecs)
}

func formatBoundForLog(raw, slackTS string) string {
	if raw == "" {
		return "(none)"
	}
	if slackTS == "" {
		return raw
	}
	t, err := parseSlackTimestamp(slackTS)
	if err != nil {
		return raw
	}
	return fmt.Sprintf("%s (%s)", raw, t.UTC().Format(time.RFC3339))
}

func fetchReplies(ctx context.Context, api *slack.Client, channelID, ts string, delay time.Duration, expectedScopes []string) ([]slack.Message, error) {
	var out []slack.Message
	cursor := ""
	for {
		var (
			resp    []slack.Message
			hasMore bool
			next    string
			err     error
		)
		for {
			logf("requesting replies for thread %s (cursor=%q)", ts, cursor)
			resp, hasMore, next, err = api.GetConversationRepliesContext(ctx, &slack.GetConversationRepliesParameters{
				ChannelID:          channelID,
				Timestamp:          ts,
				Cursor:             cursor,
				Limit:              200,
				IncludeAllMetadata: true,
				Inclusive:          true,
			})
			if rl := retryAfter(err); rl > 0 {
				logf("rate limited while fetching replies for thread %s; retrying in %d seconds", ts, rl)
				time.Sleep(time.Duration(rl) * time.Second)
				continue
			}
			if err != nil {
				logf("failed to fetch replies for thread %s: %v", ts, err)
				return out, describeSlackAPIError(err, slackAPIErrorDetails{
					Operation:      fmt.Sprintf("fetch replies for thread %s in channel %s", ts, channelID),
					Method:         slackMethodConversationsReplies,
					RequiredScopes: expectedScopes,
					Hints: append(conversationScopeHints(channelID, "groups:history", "mpim:history"),
						"If you only need root messages, rerun with -no-replies to skip thread reply requests.",
					),
				})
			}
			break
		}
		logf("received %d replies in current page for thread %s (hasMore=%t)", len(resp), ts, hasMore)
		out = append(out, resp...)
		if !hasMore {
			break
		}
		cursor = next
		if delay > 0 {
			logf("waiting %s before requesting next replies page for thread %s", delay, ts)
		}
		time.Sleep(delay)
	}
	logf("completed fetching replies for thread %s; total replies collected: %d", ts, len(out))
	return out, nil
}

func buildSimpleMessages(messages []slack.Message, replyMap map[string][]slack.Message, resolver *UserResolver) []SimpleMessage {
	out := make([]SimpleMessage, 0, len(messages))
	for _, m := range messages {
		if isReply(m) {
			continue
		}
		key := threadTimestamp(m)
		replies := buildSimpleReplies(replyMap[key], key, m.Timestamp, resolver)
		out = append(out, toSimpleMessage(m, replies, resolver))
	}
	return out
}

func buildSimpleReplies(messages []slack.Message, parentKey, parentTimestamp string, resolver *UserResolver) []SimpleMessage {
	if len(messages) == 0 {
		return nil
	}
	replies := make([]SimpleMessage, 0, len(messages))
	for _, msg := range messages {
		if msg.Timestamp == "" {
			continue
		}
		if msg.Timestamp == parentKey || msg.Timestamp == parentTimestamp {
			// skip the parent message which is often returned as the first element
			continue
		}
		replies = append(replies, toSimpleMessage(msg, nil, resolver))
	}
	if len(replies) == 0 {
		return nil
	}
	return replies
}

func toSimpleMessage(msg slack.Message, replies []SimpleMessage, resolver *UserResolver) SimpleMessage {
	if len(replies) == 0 {
		replies = nil
	}
	return SimpleMessage{
		User:    resolveSender(msg, resolver),
		Message: msg.Text,
		Date:    formatTimestamp(msg.Timestamp),
		Replies: replies,
	}
}

func resolveSender(msg slack.Message, resolver *UserResolver) string {
	if msg.User != "" {
		return resolver.Lookup(msg.User)
	}
	if msg.Username != "" {
		return msg.Username
	}
	return msg.BotID
}

type UserResolver struct {
	ctx               context.Context
	api               *slack.Client
	delay             time.Duration
	path              string
	cache             map[string]string
	dirty             bool
	seen              map[string]bool
	logEvents         map[string]bool
	fetchAttempts     int
	emailWarningShown bool
}

func NewUserResolver(ctx context.Context, api *slack.Client, path string, delay time.Duration) (*UserResolver, error) {
	cache := make(map[string]string)
	if path != "" {
		if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
			if err := json.Unmarshal(data, &cache); err != nil {
				return nil, fmt.Errorf("parse user cache %s: %w", path, err)
			}
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("read user cache %s: %w", path, err)
		}
	}
	return &UserResolver{
		ctx:       ctx,
		api:       api,
		delay:     delay,
		path:      path,
		cache:     cache,
		seen:      make(map[string]bool),
		logEvents: make(map[string]bool),
	}, nil
}

func (r *UserResolver) Lookup(userID string) string {
	if userID == "" {
		return userID
	}
	if email, ok := r.cache[userID]; ok && email != "" {
		r.logOnce("cache", userID, "resolved user %s from cache as %s", userID, email)
		return email
	}
	if !isResolvableUserID(userID) {
		r.logOnce("skip", userID, "skipping lookup for non-resolvable user identifier %s", userID)
		return userID
	}
	r.fetchAttempts++
	attempt := r.fetchAttempts
	logf("fetching profile for user %s from Slack (attempt %d)", userID, attempt)
	email, err := r.fetchFromSlack(userID)
	if err != nil {
		if attempt == 1 {
			fail(err)
		}
		logf("failed to resolve user %s via Slack: %v", userID, err)
		r.warnOnce(userID, err)
		r.cache[userID] = userID
		r.dirty = true
		return userID
	}
	if email == "" {
		logf("Slack profile for user %s did not include an email; defaulting to ID", userID)
		r.warnMissingEmailScopeOnce(userID)
		email = userID
	} else {
		logf("resolved user %s to email %s", userID, email)
	}
	logf("caching resolved identity for user %s", userID)
	r.cache[userID] = email
	r.dirty = true
	return email
}

func (r *UserResolver) Save() error {
	if !r.dirty || r.path == "" {
		return nil
	}
	logf("persisting user cache to %s (%d entries)", r.path, len(r.cache))
	if dir := filepath.Dir(r.path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create user cache directory %s: %w", dir, err)
		}
	}
	tmp := r.path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("create temporary user cache %s: %w", tmp, err)
	}
	enc := json.NewEncoder(f)
	if err := enc.Encode(r.cache); err != nil {
		f.Close()
		return fmt.Errorf("write temporary user cache %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close temporary user cache %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, r.path); err != nil {
		return fmt.Errorf("replace user cache %s with %s: %w", r.path, tmp, err)
	}
	logf("user cache persisted to %s", r.path)
	return nil
}

func (r *UserResolver) fetchFromSlack(userID string) (string, error) {
	for {
		user, err := r.api.GetUserInfoContext(r.ctx, userID)
		if rl := retryAfter(err); rl > 0 {
			logf("rate limited while resolving user %s; retrying in %d seconds", userID, rl)
			time.Sleep(time.Duration(rl) * time.Second)
			continue
		}
		if err != nil {
			logf("Slack returned an error while resolving user %s: %v", userID, err)
			return "", describeSlackAPIError(err, slackAPIErrorDetails{
				Operation:      fmt.Sprintf("resolve Slack user profile for %s", userID),
				Method:         slackMethodUsersInfo,
				RequiredScopes: []string{"users:read"},
				Hints: []string{
					"To include email addresses in exports, also add the users:read.email scope.",
				},
			})
		}
		if r.delay > 0 {
			logf("waiting %s before next user info request", r.delay)
			time.Sleep(r.delay)
		}
		if user != nil && user.Profile.Email != "" {
			return user.Profile.Email, nil
		}
		return "", nil
	}
}

func isResolvableUserID(userID string) bool {
	return strings.HasPrefix(userID, "U") || strings.HasPrefix(userID, "W")
}

func (r *UserResolver) logOnce(event, userID string, format string, args ...interface{}) {
	if r.logEvents == nil {
		r.logEvents = make(map[string]bool)
	}
	key := event + ":" + userID
	if r.logEvents[key] {
		return
	}
	r.logEvents[key] = true
	logf(format, args...)
}

func (r *UserResolver) warnOnce(userID string, err error) {
	if r.seen == nil {
		r.seen = make(map[string]bool)
	}
	if r.seen[userID] {
		return
	}
	warn(fmt.Errorf("could not resolve Slack user %s to an email: %w", userID, err))
	r.seen[userID] = true
}

func (r *UserResolver) warnMissingEmailScopeOnce(userID string) {
	if r.emailWarningShown {
		return
	}
	warn(fmt.Errorf("Slack profile for user %s did not include an email; exported users will fall back to Slack IDs. If you need email addresses, add the users:read.email scope, reinstall/reauthorize the Slack app, delete stale %s entries if needed, and rerun", userID, r.path))
	r.emailWarningShown = true
}

func formatTimestamp(ts string) string {
	t, err := parseSlackTimestamp(ts)
	if err != nil {
		return ts
	}
	return t.UTC().Format(time.RFC3339)
}

func parseSlackTimestamp(ts string) (time.Time, error) {
	parts := strings.SplitN(ts, ".", 2)
	if len(parts) == 0 || parts[0] == "" {
		return time.Time{}, errors.New("invalid slack timestamp")
	}
	secs, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	var nsecs int64
	if len(parts) > 1 {
		fractional := parts[1]
		if len(fractional) > 9 {
			fractional = fractional[:9]
		}
		for len(fractional) < 9 {
			fractional += "0"
		}
		nsecs, err = strconv.ParseInt(fractional, 10, 64)
		if err != nil {
			return time.Time{}, err
		}
	}
	return time.Unix(secs, nsecs).UTC(), nil
}

func isReply(msg slack.Message) bool {
	return msg.ThreadTimestamp != "" && msg.Timestamp != msg.ThreadTimestamp
}

func isRootMessage(msg slack.Message) bool {
	return !isReply(msg)
}

func countRootMessages(messages []slack.Message) int {
	count := 0
	for _, m := range messages {
		if isRootMessage(m) {
			count++
		}
	}
	return count
}

// trimToRootLimit keeps only the first N root messages (and any non-root
// messages that appear before that cutoff in the API response order).
func trimToRootLimit(messages []slack.Message, limit int) []slack.Message {
	if limit <= 0 {
		return messages
	}
	out := make([]slack.Message, 0, len(messages))
	roots := 0
	for _, m := range messages {
		if isRootMessage(m) {
			if roots >= limit {
				break
			}
			roots++
		}
		out = append(out, m)
	}
	return out
}

func threadTimestamp(msg slack.Message) string {
	if msg.ThreadTimestamp != "" {
		return msg.ThreadTimestamp
	}
	return msg.Timestamp
}

func retryAfter(err error) int {
	if err == nil {
		return 0
	}
	var slackRateLimited *slack.RateLimitedError
	if errors.As(err, &slackRateLimited) && slackRateLimited.RetryAfter > 0 {
		return int((slackRateLimited.RetryAfter + time.Second - 1) / time.Second)
	}
	var rle interface{ RetryAfter() int }
	if errors.As(err, &rle) {
		return rle.RetryAfter()
	}
	if strings.Contains(err.Error(), "rate_limited") || strings.Contains(err.Error(), "429") {
		return 30
	}
	return 0
}

func warn(err error) {
	if err == nil {
		return
	}
	printDiagnostic(os.Stderr, "warning: ", err.Error())
}

func fail(err error) {
	if err == nil {
		return
	}
	printDiagnostic(os.Stderr, "error: ", err.Error())
	os.Exit(1)
}

func printDiagnostic(out *os.File, prefix, message string) {
	message = strings.TrimRight(message, "\n")
	if message == "" {
		fmt.Fprintln(out, prefix)
		return
	}
	for _, line := range strings.Split(message, "\n") {
		fmt.Fprintln(out, prefix+line)
	}
}
