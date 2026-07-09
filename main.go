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
)

var (
	stdoutLog = log.New(os.Stdout, "", log.LstdFlags)
	quietMode bool
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

func runChannelsExportWithOptions(opts channelExportOptions) {
	token := strings.TrimSpace(os.Getenv(slackTokenEnv))
	if token == "" {
		fail(fmt.Errorf("environment variable %s must be set", slackTokenEnv))
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
		fail(err)
	}
	defer func() {
		if err := resolver.Save(); err != nil {
			fail(err)
		}
	}()

	info, _ := api.GetConversationInfoContext(ctx, &slack.GetConversationInfoInput{ChannelID: opts.channelID, IncludeLocale: false})
	channelName := ""
	if info != nil {
		channelName = info.Name
		if channelName != "" {
			logf("resolved channel name: %s", channelName)
		} else {
			logf("channel information retrieved without a name")
		}
	} else {
		logf("could not retrieve channel info, continuing with ID only")
	}

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
			fail(err)
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
			replies := fetchReplies(ctx, api, opts.channelID, ts, opts.delay)
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
		fail(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	logf("writing %d exported messages to %s", len(exp.Messages), out)
	if err := enc.Encode(exp); err != nil {
		fail(err)
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

func fetchReplies(ctx context.Context, api *slack.Client, channelID, ts string, delay time.Duration) []slack.Message {
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
				return out
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
	return out
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
	ctx           context.Context
	api           *slack.Client
	delay         time.Duration
	path          string
	cache         map[string]string
	dirty         bool
	seen          map[string]bool
	logEvents     map[string]bool
	fetchAttempts int
}

func NewUserResolver(ctx context.Context, api *slack.Client, path string, delay time.Duration) (*UserResolver, error) {
	cache := make(map[string]string)
	if path != "" {
		if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
			if err := json.Unmarshal(data, &cache); err != nil {
				return nil, err
			}
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
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
			fail(fmt.Errorf("failed to resolve first Slack user %s: %w", userID, err))
		}
		logf("failed to resolve user %s via Slack: %v", userID, err)
		r.warnOnce(userID, err)
		r.cache[userID] = userID
		r.dirty = true
		return userID
	}
	if email == "" {
		logf("Slack profile for user %s did not include an email; defaulting to ID", userID)
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
			return err
		}
	}
	tmp := r.path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	if err := enc.Encode(r.cache); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, r.path); err != nil {
		return err
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
			return "", err
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
	fmt.Fprintf(os.Stderr, "warning: could not resolve Slack user %s to an email: %v\n", userID, err)
	r.seen[userID] = true
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
	var rle interface{ RetryAfter() int }
	if errors.As(err, &rle) {
		return rle.RetryAfter()
	}
	if strings.Contains(err.Error(), "rate_limited") || strings.Contains(err.Error(), "429") {
		return 30
	}
	return 0
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
