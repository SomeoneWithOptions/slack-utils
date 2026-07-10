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

func TestUpdateRequiresExistingCache(t *testing.T) {
	path := filepath.Join(t.TempDir(), "users.json")
	_, err := Update(context.Background(), UpdateOptions{Path: path, Log: discardLogger{}})
	if err == nil || !strings.Contains(err.Error(), "slack-utils users cache init") {
		t.Fatalf("Update() error = %v, want users cache init guidance", err)
	}
}

func TestUpdateAddsMissingUsersWithoutChangingExistingEntries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true,"members":[{"id":"U1","profile":{"email":"new@example.com"}},{"id":"U2","profile":{"email":"two@example.com"}}],"response_metadata":{"next_cursor":""}}`)
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "users.json")
	if err := os.WriteFile(path, []byte(`{"U1":"keep@example.com"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	api := slack.New("test-token", slack.OptionAPIURL(server.URL+"/"))
	result, err := Update(context.Background(), UpdateOptions{Path: path, API: api, Log: discardLogger{}})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if result.Added != 1 || result.Total != 2 || result.WorkspaceUsers != 2 {
		t.Fatalf("Update() result = %+v", result)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cache map[string]string
	if err := json.Unmarshal(data, &cache); err != nil {
		t.Fatal(err)
	}
	if cache["U1"] != "keep@example.com" || cache["U2"] != "two@example.com" {
		t.Fatalf("cache = %#v", cache)
	}
}

func TestUpdateAPIFailureLeavesCacheUnchanged(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":false,"error":"missing_scope","needed":"users:read"}`)
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "users.json")
	original := []byte("{\n  \"U1\": \"one@example.com\"\n}\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	api := slack.New("test-token", slack.OptionAPIURL(server.URL+"/"))
	if _, err := Update(context.Background(), UpdateOptions{Path: path, API: api, Log: discardLogger{}}); err == nil {
		t.Fatal("Update() error = nil, want Slack API error")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("cache changed after API failure: %q", got)
	}
}
