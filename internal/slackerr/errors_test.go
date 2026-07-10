package slackerr

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/slack-go/slack"
)

func TestDescribeMissingScope(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		want      string
		doNotWant string
	}{
		{
			name: "uses caller fallback",
			err:  slack.SlackErrorResponse{Err: "missing_scope"},
			want: "missing Slack OAuth scope: channels:history",
		},
		{
			name: "Slack metadata takes precedence",
			err: slack.SlackErrorResponse{Err: "missing_scope", ResponseMetadata: slack.ResponseMetadata{
				Messages: []string{"[ERROR] missing_scope:groups:history"},
			}},
			want:      "missing Slack OAuth scope: groups:history",
			doNotWant: "missing Slack OAuth scope: channels:history",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Describe(tt.err, Details{
				Operation:      "fetch history",
				Method:         MethodConversationsHistory,
				RequiredScopes: []string{"channels:history"},
			}).Error()
			if !strings.Contains(got, tt.want) || (tt.doNotWant != "" && strings.Contains(got, tt.doNotWant)) {
				t.Fatalf("Describe() = %q, want %q and not %q", got, tt.want, tt.doNotWant)
			}
		})
	}
}

func TestConversationScopesFor(t *testing.T) {
	mpim := &slack.Channel{GroupConversation: slack.GroupConversation{Conversation: slack.Conversation{IsMpIM: true}}}
	tests := []struct {
		id   string
		info *slack.Channel
		want []string
	}{
		{id: "C123", want: []string{"channels:history"}},
		{id: "D123", want: []string{"im:history"}},
		{id: "G123", want: []string{"groups:history", "mpim:history"}},
		{id: "G123", info: mpim, want: []string{"mpim:history"}},
		{id: "X123", want: []string{"channels:history", "groups:history", "im:history", "mpim:history"}},
	}

	for _, tt := range tests {
		got := ConversationScopesFor(tt.id, tt.info, "channels:history", "groups:history", "im:history", "mpim:history")
		if !reflect.DeepEqual(got, tt.want) {
			t.Fatalf("ConversationScopesFor(%q) = %#v, want %#v", tt.id, got, tt.want)
		}
	}
}

func TestRetryAfterSeconds(t *testing.T) {
	tests := []struct {
		err  error
		want int
	}{
		{err: &slack.RateLimitedError{RetryAfter: 1500 * time.Millisecond}, want: 2},
		{err: errors.New("Slack returned 429"), want: 30},
	}
	for _, tt := range tests {
		if got := RetryAfterSeconds(tt.err); got != tt.want {
			t.Fatalf("RetryAfterSeconds(%v) = %d, want %d", tt.err, got, tt.want)
		}
	}
}
