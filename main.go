package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
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

func main() {
	var token, channelID, out string
	var delay time.Duration
	flag.StringVar(&token, "token", os.Getenv("SLACK_TOKEN"), "Slack bot token (or set SLACK_TOKEN)")
	flag.StringVar(&channelID, "channel", "", "Slack channel ID (e.g., C123...)")
	flag.StringVar(&out, "out", "slack_export.json", "Output file")
	flag.DurationVar(&delay, "delay", time.Second, "Delay between requests")
	flag.Parse()

	if token == "" || channelID == "" {
		fmt.Fprintln(os.Stderr, "usage: slack-export -channel C123 [-token xoxb-...] [-out file] [-delay 1s]")
		os.Exit(2)
	}

	api := slack.New(token)

	ctx := context.Background()
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
		Messages:    buildSimpleMessages(all, replyMap),
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

func buildSimpleMessages(messages []slack.Message, replyMap map[string][]slack.Message) []SimpleMessage {
	out := make([]SimpleMessage, 0, len(messages))
	for _, m := range messages {
		if isReply(m) {
			continue
		}
		key := threadTimestamp(m)
		replies := buildSimpleReplies(replyMap[key], key, m.Timestamp)
		out = append(out, toSimpleMessage(m, replies))
	}
	return out
}

func buildSimpleReplies(messages []slack.Message, parentKey, parentTimestamp string) []SimpleMessage {
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
		replies = append(replies, toSimpleMessage(msg, nil))
	}
	if len(replies) == 0 {
		return nil
	}
	return replies
}

func toSimpleMessage(msg slack.Message, replies []SimpleMessage) SimpleMessage {
	if len(replies) == 0 {
		replies = nil
	}
	return SimpleMessage{
		User:    sender(msg),
		Message: msg.Text,
		Date:    formatTimestamp(msg.Timestamp),
		Replies: replies,
	}
}

func sender(msg slack.Message) string {
	if msg.User != "" {
		return msg.User
	}
	if msg.Username != "" {
		return msg.Username
	}
	return msg.BotID
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
