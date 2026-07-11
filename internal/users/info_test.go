package users

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/slack-go/slack"
)

func TestInfo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if userID := r.Form.Get("user"); userID != "U1234567890" {
			t.Fatalf("user = %q", userID)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true,"user":{"id":"U1234567890","name":"person","profile":{"email":"person@example.com"}}}`)
	}))
	defer server.Close()

	api := slack.New("test-token", slack.OptionAPIURL(server.URL+"/"))
	got, err := Info(context.Background(), api, "U1234567890", DefaultTokenEnv)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "U1234567890" || got.Name != "person" || got.Profile.Email != "person@example.com" {
		t.Fatalf("Info() = %#v", got)
	}
}
