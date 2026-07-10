package export

import (
	"reflect"
	"testing"

	"github.com/slack-go/slack"
)

type fixedResolver map[string]string

func (r fixedResolver) Lookup(userID string) string {
	if identity, ok := r[userID]; ok {
		return identity
	}
	return userID
}

func TestBuildSimpleMessagesNestsReplies(t *testing.T) {
	messages := []slack.Message{
		{Msg: slack.Msg{User: "U1", Text: "root", Timestamp: "100", ThreadTimestamp: "100"}},
		{Msg: slack.Msg{Username: "deploy-bot", Text: "standalone", Timestamp: "101"}},
		// Replies returned in history must not also appear at the top level.
		{Msg: slack.Msg{User: "U2", Text: "loose reply", Timestamp: "102", ThreadTimestamp: "100"}},
	}
	replies := map[string][]slack.Message{
		"100": {
			// conversations.replies commonly echoes the parent first.
			{Msg: slack.Msg{User: "U1", Text: "root", Timestamp: "100", ThreadTimestamp: "100"}},
			{Msg: slack.Msg{User: "U2", Text: "reply", Timestamp: "102", ThreadTimestamp: "100"}},
		},
	}

	got := buildSimpleMessages(messages, replies, fixedResolver{"U1": "one@example.com"})
	want := []Message{
		{
			User:    "one@example.com",
			Message: "root",
			Date:    "1970-01-01T00:01:40Z",
			Replies: []Message{{User: "U2", Message: "reply", Date: "1970-01-01T00:01:42Z"}},
		},
		{User: "deploy-bot", Message: "standalone", Date: "1970-01-01T00:01:41Z"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildSimpleMessages() = %#v, want %#v", got, want)
	}
}

func TestTrimToRootLimit(t *testing.T) {
	messages := []slack.Message{
		{Msg: slack.Msg{Timestamp: "1", ThreadTimestamp: "1"}},
		{Msg: slack.Msg{Timestamp: "1.1", ThreadTimestamp: "1"}},
		{Msg: slack.Msg{Timestamp: "2"}},
		{Msg: slack.Msg{Timestamp: "3"}},
	}

	// A reply encountered before the second root remains in API order.
	if got, want := trimToRootLimit(messages, 2), messages[:3]; !reflect.DeepEqual(got, want) {
		t.Fatalf("trimToRootLimit() = %#v, want %#v", got, want)
	}
}
