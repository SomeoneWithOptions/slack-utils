package export

import (
	"reflect"
	"testing"

	"github.com/slack-go/slack"
)

func TestCollectResolvableUserIDs(t *testing.T) {
	messages := []slack.Message{
		{Msg: slack.Msg{User: "U111"}},
		{Msg: slack.Msg{User: "U222"}},
		{Msg: slack.Msg{User: "U111"}},
		{Msg: slack.Msg{User: "B001"}},
		{Msg: slack.Msg{User: ""}},
	}
	replyMap := map[string][]slack.Message{
		"1.0": {
			{Msg: slack.Msg{User: "U333"}},
			{Msg: slack.Msg{User: "W444"}},
			{Msg: slack.Msg{User: "U222"}},
		},
	}

	got := collectResolvableUserIDs(messages, replyMap)
	want := []string{"U111", "U222", "U333", "W444"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("collectResolvableUserIDs() = %#v, want %#v", got, want)
	}
}
