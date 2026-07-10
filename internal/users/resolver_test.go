package users

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/slack-go/slack"
)

func TestPrefetchCachesOnlyMissingResolvableUsers(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/users.info" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		requested := r.Form.Get("users")
		if !strings.Contains(requested, "U2") || !strings.Contains(requested, "U3") ||
			strings.Contains(requested, "U1") || strings.Contains(requested, "B99") {
			t.Fatalf("users = %q, want only U2 and U3", requested)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true,"users":[{"id":"U2","profile":{"email":"two@example.com"}}]}`)
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "users.json")
	if err := os.WriteFile(path, []byte(`{"U1":"one@example.com"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	api := slack.New("test-token", slack.OptionAPIURL(server.URL+"/"))
	resolver, err := NewResolver(context.Background(), api, path, 0, discardLogger{}, "SLACK_TOKEN")
	if err != nil {
		t.Fatal(err)
	}
	if err := resolver.Prefetch(context.Background(), []string{"U1", "B99", "U2", "U3", "U2"}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("API calls = %d, want one batch", calls)
	}
	if got := resolver.Lookup("U2"); got != "two@example.com" {
		t.Fatalf("U2 = %q", got)
	}
	if got := resolver.Lookup("U3"); got != "U3" {
		t.Fatalf("missing U3 = %q, want ID fallback", got)
	}
	if err := resolver.Save(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cache map[string]string
	if err := json.Unmarshal(data, &cache); err != nil {
		t.Fatal(err)
	}
	if cache["U1"] != "one@example.com" || cache["U2"] != "two@example.com" || cache["U3"] != "U3" {
		t.Fatalf("saved cache = %#v", cache)
	}
}

func TestNewResolverRejectsCorruptCache(t *testing.T) {
	path := filepath.Join(t.TempDir(), "users.json")
	if err := os.WriteFile(path, []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := NewResolver(context.Background(), nil, path, 0, discardLogger{}, "SLACK_TOKEN")
	if err == nil || !strings.Contains(err.Error(), "parse user cache") {
		t.Fatalf("NewResolver() error = %v", err)
	}
}
