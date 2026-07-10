// Package export fetches Slack conversation history and writes simplified JSON.
package export

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/SomeoneWithOptions/slack-utils/internal/applog"
	"github.com/SomeoneWithOptions/slack-utils/internal/slackerr"
	"github.com/SomeoneWithOptions/slack-utils/internal/timestamp"
	"github.com/SomeoneWithOptions/slack-utils/internal/users"
	"github.com/slack-go/slack"
)

const (
	// DefaultOutputPath is the default export JSON location.
	DefaultOutputPath = "./export.json"
	// DefaultUserCachePath is the default local user cache.
	DefaultUserCachePath = "./users.json"
	// DefaultTokenEnv is the environment variable for the Slack token.
	DefaultTokenEnv = "SLACK_TOKEN"
)

// Logger is the progress logger used during export.
type Logger interface {
	Logf(format string, args ...any)
}

// Options configures a conversation history export.
type Options struct {
	ConversationID string
	Delay          time.Duration
	Since          string
	To             string
	Output         string
	Limit          int
	NoReplies      bool
	UserCachePath  string
	TokenEnv       string
	Log            Logger
}

// Result is the on-disk export schema. The channel_* JSON keys are retained
// for compatibility with existing exports.
type Result struct {
	ConversationID   string    `json:"channel_id"`
	ConversationName string    `json:"channel_name"`
	ExportedAt       time.Time `json:"exported_at"`
	Messages         []Message `json:"messages"`
}

// Message is a simplified Slack message with optional nested replies.
type Message struct {
	User    string    `json:"user"`
	Message string    `json:"message"`
	Date    string    `json:"date"`
	Replies []Message `json:"replies,omitempty"`
}

// SenderResolver maps Slack user IDs onto human-readable identities.
type SenderResolver interface {
	Lookup(userID string) string
}

// Run exports conversation history according to opts and writes the result JSON.
// On fatal failure it prints a diagnostic and exits the process, matching CLI
// behavior of the original single-file implementation.
func Run(opts Options) {
	if opts.Log == nil {
		opts.Log = applog.New()
	}
	if opts.TokenEnv == "" {
		opts.TokenEnv = DefaultTokenEnv
	}
	if opts.UserCachePath == "" {
		opts.UserCachePath = DefaultUserCachePath
	}

	token := strings.TrimSpace(os.Getenv(opts.TokenEnv))
	if token == "" {
		applog.Fail(fmt.Errorf("environment variable %s must be set to a Slack token with the required OAuth scopes", opts.TokenEnv))
	}
	out := strings.TrimSpace(opts.Output)
	if out == "" {
		applog.Fail(fmt.Errorf("-o/-output path must not be empty"))
	}
	if opts.Limit < 0 {
		applog.Fail(fmt.Errorf("-limit must be >= 0"))
	}

	oldest, err := timestamp.ParseBound(opts.Since, false)
	if err != nil {
		applog.Fail(fmt.Errorf("invalid -since value %q: %w", opts.Since, err))
	}
	latest, err := timestamp.ParseBound(opts.To, true)
	if err != nil {
		applog.Fail(fmt.Errorf("invalid -to value %q: %w", opts.To, err))
	}
	if oldest != "" && latest != "" {
		oldestTime, errOldest := timestamp.ParseSlack(oldest)
		latestTime, errLatest := timestamp.ParseSlack(latest)
		if errOldest == nil && errLatest == nil && oldestTime.After(latestTime) {
			applog.Fail(fmt.Errorf("-since (%s) must be before or equal to -to (%s)", opts.Since, opts.To))
		}
	}

	api := newSlackClient(token)
	ctx := context.Background()
	log := opts.Log

	log.Logf("starting export for conversation %s with request delay %s", opts.ConversationID, opts.Delay)
	if oldest != "" || latest != "" {
		log.Logf("time range filter: since=%s to=%s", timestamp.FormatBoundForLog(opts.Since, oldest), timestamp.FormatBoundForLog(opts.To, latest))
	}
	if opts.Limit > 0 {
		log.Logf("message limit: %d root messages", opts.Limit)
	}
	if opts.NoReplies {
		log.Logf("thread replies: disabled (-no-replies)")
	}
	log.Logf("export destination: %s", out)
	log.Logf("user cache file: %s", opts.UserCachePath)

	resolver, err := users.NewResolver(ctx, api, opts.UserCachePath, opts.Delay, log, opts.TokenEnv)
	if err != nil {
		applog.Fail(fmt.Errorf("initialize user resolver using cache %s: %w", opts.UserCachePath, err))
	}
	defer func() {
		if err := resolver.Save(); err != nil {
			applog.Fail(fmt.Errorf("save user cache %s: %w", opts.UserCachePath, err))
		}
	}()

	info, err := api.GetConversationInfoContext(ctx, &slack.GetConversationInfoInput{ChannelID: opts.ConversationID, IncludeLocale: false})
	conversationName := ""
	if err != nil {
		applog.Warn(slackerr.Describe(err, slackerr.Details{
			Operation:      fmt.Sprintf("resolve conversation information for %s", opts.ConversationID),
			Method:         slackerr.MethodConversationsInfo,
			RequiredScopes: slackerr.ConversationScopesFor(opts.ConversationID, nil, "channels:read", "groups:read", "im:read", "mpim:read"),
			TokenEnv:       opts.TokenEnv,
			Hints: append([]string{
				"Conversation name lookup is optional; export will continue with only the conversation ID.",
			}, slackerr.ConversationScopeHints(opts.ConversationID, "groups:read", "mpim:read")...),
		}))
	} else if info != nil {
		conversationName = info.Name
		if conversationName != "" {
			log.Logf("resolved conversation name: %s", conversationName)
		} else {
			log.Logf("conversation information retrieved without a name")
		}
	} else {
		log.Logf("could not retrieve conversation info, continuing with ID only")
	}
	historyScopes := slackerr.ConversationScopesFor(opts.ConversationID, info, "channels:history", "groups:history", "im:history", "mpim:history")

	var all []slack.Message
	cursor := ""
	for {
		pageLimit := 200
		if opts.Limit > 0 {
			remaining := opts.Limit - countRootMessages(all)
			if remaining <= 0 {
				log.Logf("reached message limit of %d root messages; stopping history fetch", opts.Limit)
				break
			}
			if remaining < pageLimit {
				pageLimit = remaining
			}
		}
		log.Logf("requesting conversation history (cursor=%q, limit=%d)", cursor, pageLimit)
		h, err := getHistory(ctx, api, opts.ConversationID, cursor, oldest, latest, pageLimit, log)
		if err != nil {
			applog.Fail(slackerr.Describe(err, slackerr.Details{
				Operation:      fmt.Sprintf("fetch conversation history for %s", opts.ConversationID),
				Method:         slackerr.MethodConversationsHistory,
				RequiredScopes: historyScopes,
				TokenEnv:       opts.TokenEnv,
				Hints:          slackerr.ConversationScopeHints(opts.ConversationID, "groups:history", "mpim:history"),
			}))
		}
		if h == nil {
			applog.Fail(fmt.Errorf("fetch conversation history for %s failed: Slack returned an empty response", opts.ConversationID))
		}
		log.Logf("received %d messages", len(h.Messages))
		all = append(all, h.Messages...)
		if opts.Limit > 0 && countRootMessages(all) >= opts.Limit {
			all = trimToRootLimit(all, opts.Limit)
			log.Logf("reached message limit of %d root messages; collected %d raw messages", opts.Limit, len(all))
			break
		}
		if h.ResponseMetaData.NextCursor == "" {
			log.Logf("no more history pages; collected %d messages", len(all))
			break
		}
		cursor = h.ResponseMetaData.NextCursor
		if opts.Delay > 0 {
			log.Logf("waiting %s before requesting next history page", opts.Delay)
		}
		time.Sleep(opts.Delay)
	}

	replyMap := make(map[string][]slack.Message)
	if opts.NoReplies {
		log.Logf("skipping thread reply fetch (-no-replies)")
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

		log.Logf("processing replies for %d root messages", len(rootMessages))
		for i, m := range rootMessages {
			ts := threadTimestamp(m)
			log.Logf("fetching replies for thread %s (%d/%d)", ts, i+1, len(rootMessages))
			replies, err := fetchReplies(ctx, api, opts.ConversationID, ts, opts.Delay, historyScopes, opts.TokenEnv, log)
			if err != nil {
				applog.Fail(err)
			}
			if len(replies) > 0 {
				log.Logf("retrieved %d replies for thread %s", len(replies), ts)
				replyMap[ts] = replies
				time.Sleep(opts.Delay)
				if opts.Delay > 0 {
					log.Logf("waiting %s before next thread request", opts.Delay)
				}
			} else {
				log.Logf("no replies returned for thread %s", ts)
			}
			log.Logf("Processed main message %d/%d", i+1, len(rootMessages))
		}
	}

	if err := prefetchMessageUsers(ctx, all, replyMap, resolver, log); err != nil {
		applog.Fail(err)
	}

	exp := Result{
		ConversationID:   opts.ConversationID,
		ConversationName: conversationName,
		ExportedAt:       time.Now().UTC(),
		Messages:         buildSimpleMessages(all, replyMap, resolver),
	}

	if dir := filepath.Dir(out); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			applog.Fail(fmt.Errorf("create output directory %s: %w", dir, err))
		}
	}
	f, err := os.Create(out)
	if err != nil {
		applog.Fail(fmt.Errorf("create output file %s: %w", out, err))
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	log.Logf("writing %d exported messages to %s", len(exp.Messages), out)
	if err := enc.Encode(exp); err != nil {
		applog.Fail(fmt.Errorf("write export JSON to %s: %w", out, err))
	}
	fmt.Println("wrote", out)
}

func newSlackClient(token string) *slack.Client {
	cfg := slack.DefaultRetryConfig()
	cfg.MaxRetries = 3
	cfg.Handlers = slack.AllBuiltinRetryHandlers(cfg)
	return slack.New(token, slack.OptionRetryConfig(cfg))
}

func prefetchMessageUsers(ctx context.Context, messages []slack.Message, replyMap map[string][]slack.Message, resolver *users.Resolver, log Logger) error {
	ids := collectResolvableUserIDs(messages, replyMap)
	if len(ids) == 0 {
		return nil
	}
	log.Logf("prefetching profiles for %d unique users", len(ids))
	return resolver.Prefetch(ctx, ids)
}

func collectResolvableUserIDs(messages []slack.Message, replyMap map[string][]slack.Message) []string {
	seen := make(map[string]struct{})
	var ids []string
	add := func(id string) {
		if !users.IsResolvableUserID(id) {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	for _, msg := range messages {
		add(msg.User)
	}
	for _, replies := range replyMap {
		for _, msg := range replies {
			add(msg.User)
		}
	}
	return ids
}

func getHistory(ctx context.Context, api *slack.Client, conversationID, cursor, oldest, latest string, limit int, log Logger) (*slack.GetConversationHistoryResponse, error) {
	if limit <= 0 {
		limit = 200
	}
	for {
		h, err := api.GetConversationHistoryContext(ctx, &slack.GetConversationHistoryParameters{
			ChannelID:          conversationID,
			Cursor:             cursor,
			Oldest:             oldest,
			Latest:             latest,
			Limit:              limit,
			IncludeAllMetadata: true,
			Inclusive:          true,
		})
		if rl := slackerr.RetryAfterSeconds(err); rl > 0 {
			log.Logf("rate limited while fetching history; retrying in %d seconds", rl)
			time.Sleep(time.Duration(rl) * time.Second)
			continue
		}
		if err != nil {
			log.Logf("history request returned an error: %v", err)
		}
		return h, err
	}
}

func fetchReplies(ctx context.Context, api *slack.Client, conversationID, ts string, delay time.Duration, expectedScopes []string, tokenEnv string, log Logger) ([]slack.Message, error) {
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
			log.Logf("requesting replies for thread %s (cursor=%q)", ts, cursor)
			resp, hasMore, next, err = api.GetConversationRepliesContext(ctx, &slack.GetConversationRepliesParameters{
				ChannelID:          conversationID,
				Timestamp:          ts,
				Cursor:             cursor,
				Limit:              200,
				IncludeAllMetadata: true,
				Inclusive:          true,
			})
			if rl := slackerr.RetryAfterSeconds(err); rl > 0 {
				log.Logf("rate limited while fetching replies for thread %s; retrying in %d seconds", ts, rl)
				time.Sleep(time.Duration(rl) * time.Second)
				continue
			}
			if err != nil {
				log.Logf("failed to fetch replies for thread %s: %v", ts, err)
				return out, slackerr.Describe(err, slackerr.Details{
					Operation:      fmt.Sprintf("fetch replies for thread %s in conversation %s", ts, conversationID),
					Method:         slackerr.MethodConversationsReplies,
					RequiredScopes: expectedScopes,
					TokenEnv:       tokenEnv,
					Hints: append(slackerr.ConversationScopeHints(conversationID, "groups:history", "mpim:history"),
						"If you only need root messages, rerun with -no-replies to skip thread reply requests.",
					),
				})
			}
			break
		}
		log.Logf("received %d replies in current page for thread %s (hasMore=%t)", len(resp), ts, hasMore)
		out = append(out, resp...)
		if !hasMore {
			break
		}
		cursor = next
		if delay > 0 {
			log.Logf("waiting %s before requesting next replies page for thread %s", delay, ts)
		}
		time.Sleep(delay)
	}
	log.Logf("completed fetching replies for thread %s; total replies collected: %d", ts, len(out))
	return out, nil
}

func buildSimpleMessages(messages []slack.Message, replyMap map[string][]slack.Message, resolver SenderResolver) []Message {
	out := make([]Message, 0, len(messages))
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

func buildSimpleReplies(messages []slack.Message, parentKey, parentTimestamp string, resolver SenderResolver) []Message {
	if len(messages) == 0 {
		return nil
	}
	replies := make([]Message, 0, len(messages))
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

func toSimpleMessage(msg slack.Message, replies []Message, resolver SenderResolver) Message {
	if len(replies) == 0 {
		replies = nil
	}
	return Message{
		User:    resolveSender(msg, resolver),
		Message: msg.Text,
		Date:    timestamp.FormatMessage(msg.Timestamp),
		Replies: replies,
	}
}

func resolveSender(msg slack.Message, resolver SenderResolver) string {
	if msg.User != "" {
		return resolver.Lookup(msg.User)
	}
	if msg.Username != "" {
		return msg.Username
	}
	return msg.BotID
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
