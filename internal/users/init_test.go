package users

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/slack-go/slack"
)

type discardLogger struct{}

func (discardLogger) Logf(string, ...any) {}

func TestInitializeFetchesAllPagesBeforeCreatingCache(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		calls++
		switch calls {
		case 1:
			if cursor := r.Form.Get("cursor"); cursor != "" {
				t.Fatalf("first cursor = %q", cursor)
			}
			_, _ = io.WriteString(w, `{"ok":true,"members":[{"id":"U1","profile":{"email":"one@example.com"}}],"response_metadata":{"next_cursor":"next"}}`)
		case 2:
			if cursor := r.Form.Get("cursor"); cursor != "next" {
				t.Fatalf("second cursor = %q", cursor)
			}
			_, _ = io.WriteString(w, `{"ok":true,"members":[{"id":"U2","profile":{}}],"response_metadata":{"next_cursor":""}}`)
		default:
			t.Fatalf("unexpected users.list call %d", calls)
		}
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "nested", "users.json")
	api := slack.New("test-token", slack.OptionAPIURL(server.URL+"/"))
	result, err := Initialize(context.Background(), InitOptions{Path: path, API: api, Log: discardLogger{}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Users != 2 || result.Pages != 2 || result.MissingEmails != 1 {
		t.Fatalf("Initialize() result = %+v", result)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cache map[string]string
	if err := json.Unmarshal(data, &cache); err != nil {
		t.Fatal(err)
	}
	if cache["U1"] != "one@example.com" || cache["U2"] != "U2" || len(cache) != 2 {
		t.Fatalf("cache = %#v", cache)
	}
	if _, err := os.Stat(path + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary cache remains: %v", err)
	}
}

func TestInitializeNeverOverwritesExistingCache(t *testing.T) {
	path := filepath.Join(t.TempDir(), "users.json")
	original := []byte("existing cache\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := Initialize(context.Background(), InitOptions{Path: path, Log: discardLogger{}})
	if err != nil || !result.AlreadyExists {
		t.Fatalf("Initialize() = %+v, %v", result, err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("existing cache changed: %q", got)
	}
}

func TestInitializeAPIFailureDoesNotCreateCache(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":false,"error":"missing_scope","needed":"users:read"}`)
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "users.json")
	api := slack.New("test-token", slack.OptionAPIURL(server.URL+"/"))
	_, err := Initialize(context.Background(), InitOptions{Path: path, API: api, Log: discardLogger{}})
	if err == nil || !strings.Contains(err.Error(), "users.list") {
		t.Fatalf("Initialize() error = %v", err)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("cache exists after API failure: %v", statErr)
	}
}
