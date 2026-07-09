package slackerr

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/slack-go/slack"
)

func TestDescribeMissingScopeUsesFallbackScope(t *testing.T) {
	err := slack.SlackErrorResponse{Err: "missing_scope"}

	got := Describe(err, Details{
		Operation:      "fetch conversation history for C123",
		Method:         MethodConversationsHistory,
		RequiredScopes: []string{"channels:history"},
	}).Error()

	assertContains(t, got, "fetch conversation history for C123 failed")
	assertContains(t, got, "Slack API method: conversations.history")
	assertContains(t, got, "missing Slack OAuth scope: channels:history")
	assertContains(t, got, "Reinstall or reauthorize")
}

func TestDescribeMissingScopeUsesSlackMetadata(t *testing.T) {
	err := slack.SlackErrorResponse{
		Err: "missing_scope",
		ResponseMetadata: slack.ResponseMetadata{
			Messages: []string{"[ERROR] missing_scope:groups:history"},
		},
	}

	got := Describe(err, Details{
		Operation:      "fetch conversation history for G123",
		Method:         MethodConversationsHistory,
		RequiredScopes: []string{"channels:history"},
	}).Error()

	assertContains(t, got, "missing Slack OAuth scope: groups:history")
	if strings.Contains(got, "missing Slack OAuth scope: channels:history") {
		t.Fatalf("expected Slack metadata scope to override fallback scope, got:\n%s", got)
	}
}

func TestConversationScopesFor(t *testing.T) {
	tests := []struct {
		name      string
		channelID string
		info      *slack.Channel
		want      []string
	}{
		{
			name:      "public channel ID",
			channelID: "C123",
			want:      []string{"channels:history"},
		},
		{
			name:      "direct message ID",
			channelID: "D123",
			want:      []string{"im:history"},
		},
		{
			name:      "private channel or mpim fallback",
			channelID: "G123",
			want:      []string{"groups:history", "mpim:history"},
		},
		{
			name:      "mpim info beats G fallback",
			channelID: "G123",
			info: &slack.Channel{GroupConversation: slack.GroupConversation{Conversation: slack.Conversation{
				IsMpIM: true,
			}}},
			want: []string{"mpim:history"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ConversationScopesFor(tt.channelID, tt.info, "channels:history", "groups:history", "im:history", "mpim:history")
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ConversationScopesFor() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestRetryAfterSecondsSlackRateLimitedErrorRoundsUp(t *testing.T) {
	got := RetryAfterSeconds(&slack.RateLimitedError{RetryAfter: 1500 * time.Millisecond})
	if got != 2 {
		t.Fatalf("RetryAfterSeconds() = %d, want 2", got)
	}
}

func assertContains(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("expected output to contain %q, got:\n%s", want, got)
	}
}
