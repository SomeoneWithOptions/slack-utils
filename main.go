package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/slack-go/slack"
)

type Thread struct {
	Parent  slack.Message   `json:"parent"`
	Replies []slack.Message `json:"replies"`
}

type Export struct {
	ChannelID   string          `json:"channel_id"`
	ChannelName string          `json:"channel_name"`
	ExportedAt  time.Time       `json:"exported_at"`
	Messages    []slack.Message `json:"messages"`
	Threads     []Thread        `json:"threads"`
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

	var threads []Thread
	for _, m := range all {
		if m.ThreadTimestamp != "" && m.Timestamp != m.ThreadTimestamp {
			continue
		}
		if m.ReplyCount == 0 && m.ThreadTimestamp == "" {
			continue
		}
		ts := m.Timestamp
		if m.ThreadTimestamp != "" {
			ts = m.ThreadTimestamp
		}
		replies := fetchReplies(ctx, api, channelID, ts, delay)
		if len(replies) > 0 {
			threads = append(threads, Thread{Parent: m, Replies: replies})
			time.Sleep(delay)
		}
	}

	exp := Export{
		ChannelID:   channelID,
		ChannelName: channelName,
		ExportedAt:  time.Now().UTC(),
		Messages:    all,
		Threads:     threads,
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
