package users

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/slack-go/slack"
)

func TestLookupByEmail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if email := r.Form.Get("email"); email != "person@example.com" {
			t.Fatalf("email = %q", email)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true,"user":{"id":"U1234567890"}}`)
	}))
	defer server.Close()

	api := slack.New("test-token", slack.OptionAPIURL(server.URL+"/"))
	got, err := LookupByEmail(context.Background(), api, "person@example.com", DefaultTokenEnv)
	if err != nil {
		t.Fatal(err)
	}
	if got != "U1234567890" {
		t.Fatalf("LookupByEmail() = %q", got)
	}
}
