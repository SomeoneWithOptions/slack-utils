package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
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
	exportFilePath = "./export"
	userCacheFile  = "./users.json"
	slackTokenEnv  = "SLACK_TOKEN"
)

func main() {
	var channelID string
	var delay time.Duration
	flag.StringVar(&channelID, "channel", "", "Slack channel ID (e.g., C123...)")
	flag.DurationVar(&delay, "delay", time.Second, "Delay between requests")
	flag.Parse()

	token := strings.TrimSpace(os.Getenv(slackTokenEnv))
	if channelID == "" {
		fmt.Fprintln(os.Stderr, "usage: slack-export -channel C123 [-delay 1s]")
		os.Exit(2)
	}
	if token == "" {
		fail(fmt.Errorf("environment variable %s must be set", slackTokenEnv))
	}

	out := exportFilePath
	userCachePath := userCacheFile

	api := slack.New(token)

	ctx := context.Background()
	resolver, err := NewUserResolver(ctx, api, userCachePath, delay)
	if err != nil {
		fail(err)
	}
	defer func() {
		if err := resolver.Save(); err != nil {
			fail(err)
		}
	}()

	info, _ := api.GetConversationInfoContext(ctx, &slack.GetConversationInfoInput{ChannelID: channelID, IncludeLocale: false})
	channelName := ""
	if info != nil {
		channelName = info.Name
	}

	var all []slack.Message
	cursor := ""
	for {
		h, err := getHistory(ctx, api, channelID, cursor)
		if err != nil {
			fail(err)
		}
		all = append(all, h.Messages...)
		if h.ResponseMetaData.NextCursor == "" {
			break
		}
		cursor = h.ResponseMetaData.NextCursor
		time.Sleep(delay)
	}

	replyMap := make(map[string][]slack.Message)
	for _, m := range all {
		if m.ThreadTimestamp != "" && m.Timestamp != m.ThreadTimestamp {
			continue
		}
		if m.ReplyCount == 0 && m.ThreadTimestamp == "" {
			continue
		}
		ts := threadTimestamp(m)
		replies := fetchReplies(ctx, api, channelID, ts, delay)
		if len(replies) > 0 {
			replyMap[ts] = replies
			time.Sleep(delay)
		}
	}

	exp := Export{
		ChannelID:   channelID,
		ChannelName: channelName,
		ExportedAt:  time.Now().UTC(),
		Messages:    buildSimpleMessages(all, replyMap, resolver),
	}

	f, err := os.Create(out)
	if err != nil {
		fail(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(exp); err != nil {
		fail(err)
	}
	fmt.Println("wrote", out)
}

func getHistory(ctx context.Context, api *slack.Client, channelID, cursor string) (*slack.GetConversationHistoryResponse, error) {
	for {
		h, err := api.GetConversationHistoryContext(ctx, &slack.GetConversationHistoryParameters{
			ChannelID:          channelID,
			Cursor:             cursor,
			Limit:              200,
			IncludeAllMetadata: true,
			Inclusive:          true,
		})
		if rl := retryAfter(err); rl > 0 {
			time.Sleep(time.Duration(rl) * time.Second)
			continue
		}
		return h, err
	}
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
			resp, hasMore, next, err = api.GetConversationRepliesContext(ctx, &slack.GetConversationRepliesParameters{
				ChannelID:          channelID,
				Timestamp:          ts,
				Cursor:             cursor,
				Limit:              200,
				IncludeAllMetadata: true,
				Inclusive:          true,
			})
			if rl := retryAfter(err); rl > 0 {
				time.Sleep(time.Duration(rl) * time.Second)
				continue
			}
			if err != nil {
				return out
			}
			break
		}
		out = append(out, resp...)
		if !hasMore {
			break
		}
		cursor = next
		time.Sleep(delay)
	}
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
		ctx:   ctx,
		api:   api,
		delay: delay,
		path:  path,
		cache: cache,
		seen:  make(map[string]bool),
	}, nil
}

func (r *UserResolver) Lookup(userID string) string {
	if userID == "" {
		return userID
	}
	if email, ok := r.cache[userID]; ok && email != "" {
		return email
	}
	if !isResolvableUserID(userID) {
		return userID
	}
	r.fetchAttempts++
	attempt := r.fetchAttempts
	email, err := r.fetchFromSlack(userID)
	if err != nil {
		if attempt == 1 {
			fail(fmt.Errorf("failed to resolve first Slack user %s: %w", userID, err))
		}
		r.warnOnce(userID, err)
		r.cache[userID] = userID
		r.dirty = true
		return userID
	}
	if email == "" {
		email = userID
	}
	r.cache[userID] = email
	r.dirty = true
	return email
}

func (r *UserResolver) Save() error {
	if !r.dirty || r.path == "" {
		return nil
	}
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
	return os.Rename(tmp, r.path)
}

func (r *UserResolver) fetchFromSlack(userID string) (string, error) {
	for {
		user, err := r.api.GetUserInfoContext(r.ctx, userID)
		if rl := retryAfter(err); rl > 0 {
			time.Sleep(time.Duration(rl) * time.Second)
			continue
		}
		if err != nil {
			return "", err
		}
		if r.delay > 0 {
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
