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
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/slack-go/slack"
)

type discardLogger struct{}

func (discardLogger) Logf(string, ...any) {}

func TestInitializeFetchesEveryPageAndCreatesCache(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/users.list" {
			t.Fatalf("request path = %q, want /users.list", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm() error = %v", err)
		}
		if got := r.Form.Get("limit"); got != "200" {
			t.Fatalf("limit = %q, want 200", got)
		}
		w.Header().Set("Content-Type", "application/json")
		switch calls.Add(1) {
		case 1:
			if got := r.Form.Get("cursor"); got != "" {
				t.Fatalf("first cursor = %q, want empty", got)
			}
			io.WriteString(w, `{"ok":true,"members":[{"id":"U1","profile":{"email":"one@example.com"}}],"response_metadata":{"next_cursor":"next-page"}}`)
		case 2:
			if got := r.Form.Get("cursor"); got != "next-page" {
				t.Fatalf("second cursor = %q, want next-page", got)
			}
			io.WriteString(w, `{"ok":true,"members":[{"id":"U2","deleted":true,"profile":{}}],"response_metadata":{"next_cursor":""}}`)
		default:
			t.Fatalf("unexpected users.list call %d", calls.Load())
		}
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "nested", "users.json")
	api := slack.New("test-token", slack.OptionAPIURL(server.URL+"/"))
	result, err := Initialize(context.Background(), InitOptions{
		Path: path,
		API:  api,
		Log:  discardLogger{},
	})
	if err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if result.AlreadyExists || result.Users != 2 || result.Pages != 2 || result.MissingEmails != 1 {
		t.Fatalf("Initialize() result = %+v", result)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var cache map[string]string
	if err := json.Unmarshal(data, &cache); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if cache["U1"] != "one@example.com" || cache["U2"] != "U2" || len(cache) != 2 {
		t.Fatalf("cache = %#v", cache)
	}
	if _, err := os.Stat(path + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary cache remains after success: %v", err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("cache permissions = %o, want 600", got)
		}
	}
}

func TestInitializeExistingCacheIsNoOpWithoutClient(t *testing.T) {
	path := filepath.Join(t.TempDir(), "users.json")
	original := []byte("existing cache\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := Initialize(context.Background(), InitOptions{Path: path, Log: discardLogger{}})
	if err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if !result.AlreadyExists {
		t.Fatalf("AlreadyExists = false, want true")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("existing cache changed: %q", got)
	}
}

func TestInitializeRateLimitRetriesSameCursor(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true,"members":[{"id":"U1","profile":{"email":"one@example.com"}}],"response_metadata":{"next_cursor":""}}`)
	}))
	defer server.Close()

	var waited time.Duration
	api := slack.New("test-token", slack.OptionAPIURL(server.URL+"/"))
	result, err := Initialize(context.Background(), InitOptions{
		Path: filepath.Join(t.TempDir(), "users.json"),
		API:  api,
		Log:  discardLogger{},
		wait: func(_ context.Context, delay time.Duration) error {
			waited = delay
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if calls.Load() != 2 || waited != 2*time.Second || result.Users != 1 {
		t.Fatalf("calls=%d waited=%s result=%+v", calls.Load(), waited, result)
	}
}

func TestInitializeAPIFailureDoesNotCreateCache(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":false,"error":"missing_scope","needed":"users:read"}`)
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "users.json")
	api := slack.New("test-token", slack.OptionAPIURL(server.URL+"/"))
	_, err := Initialize(context.Background(), InitOptions{Path: path, API: api, Log: discardLogger{}})
	if err == nil || !strings.Contains(err.Error(), "users.list") || !strings.Contains(err.Error(), "users:read") {
		t.Fatalf("Initialize() error = %v", err)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("cache exists after API failure: %v", statErr)
	}
	if _, statErr := os.Stat(path + ".tmp"); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("temporary cache exists after API failure: %v", statErr)
	}
}

func TestWaitForContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitForContext(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("waitForContext() error = %v, want context.Canceled", err)
	}
}
