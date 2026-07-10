package users

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/SomeoneWithOptions/slack-utils/internal/applog"
	"github.com/SomeoneWithOptions/slack-utils/internal/slackerr"
	"github.com/slack-go/slack"
)

const (
	// DefaultCachePath is the default local user cache location.
	DefaultCachePath = "./users.json"
	// DefaultTokenEnv is the environment variable containing the Slack token.
	DefaultTokenEnv = "SLACK_TOKEN"
	// DefaultInitDelay conservatively keeps users.list at Slack's documented
	// Tier 2 baseline of at least 20 requests per minute.
	DefaultInitDelay = 3 * time.Second
	userPageLimit    = 200
)

// InitOptions configures first-time population of a user cache.
type InitOptions struct {
	Path     string
	Delay    time.Duration
	TeamID   string
	TokenEnv string
	API      *slack.Client
	Log      Logger
	wait     func(context.Context, time.Duration) error
}

// InitResult summarizes a user cache initialization.
type InitResult struct {
	AlreadyExists bool
	Users         int
	Pages         int
	MissingEmails int
}

// CacheExists reports whether path is already occupied by a file. Directories
// are rejected so a mistaken output path does not look like a successful init.
func CacheExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if err == nil {
		if info.IsDir() {
			return false, fmt.Errorf("user cache path %s is a directory", path)
		}
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("check user cache %s: %w", path, err)
}

// Initialize fetches every users.list page and creates the cache at Path. It
// never replaces an existing destination and publishes only a complete file.
func Initialize(ctx context.Context, opts InitOptions) (InitResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	path := strings.TrimSpace(opts.Path)
	if path == "" {
		return InitResult{}, fmt.Errorf("user cache path must not be empty")
	}
	if opts.Delay < 0 {
		return InitResult{}, fmt.Errorf("user list delay must be >= 0")
	}
	if opts.Log == nil {
		opts.Log = applog.New()
	}
	if opts.TokenEnv == "" {
		opts.TokenEnv = DefaultTokenEnv
	}
	if opts.wait == nil {
		opts.wait = waitForContext
	}

	exists, err := CacheExists(path)
	if err != nil {
		return InitResult{}, err
	}
	if exists {
		opts.Log.Logf("user cache %s already exists; nothing to do", path)
		return InitResult{AlreadyExists: true}, nil
	}
	if opts.API == nil {
		return InitResult{}, fmt.Errorf("Slack client is required to initialize %s", path)
	}

	cache := make(map[string]string)
	result := InitResult{}
	paginationOptions := []slack.GetUsersOption{slack.GetUsersOptionLimit(userPageLimit)}
	if teamID := strings.TrimSpace(opts.TeamID); teamID != "" {
		paginationOptions = append(paginationOptions, slack.GetUsersOptionTeamID(teamID))
	}
	page := opts.API.GetUsersPaginated(paginationOptions...)

	for {
		opts.Log.Logf("requesting Slack users page %d (cursor=%q, limit=%d)", result.Pages+1, page.Cursor, userPageLimit)
		next, err := page.Next(ctx)
		if retryAfter := slackerr.RetryAfterSeconds(err); retryAfter > 0 {
			delay := time.Duration(retryAfter) * time.Second
			opts.Log.Logf("rate limited by users.list; retrying the same page in %s", delay)
			if err := opts.wait(ctx, delay); err != nil {
				return InitResult{}, fmt.Errorf("wait to retry Slack users list: %w", err)
			}
			continue
		}
		if err != nil {
			return InitResult{}, slackerr.Describe(err, slackerr.Details{
				Operation:      "list Slack workspace users",
				Method:         slackerr.MethodUsersList,
				RequiredScopes: []string{"users:read"},
				TokenEnv:       opts.TokenEnv,
				Hints: []string{
					"To cache email addresses, also add the users:read.email scope.",
					"When using an Enterprise Grid org token, pass -team with the workspace team ID.",
				},
			})
		}

		page = next
		result.Pages++
		for _, user := range page.Users {
			if user.ID == "" {
				continue
			}
			identity := user.Profile.Email
			if identity == "" {
				identity = user.ID
				result.MissingEmails++
			}
			cache[user.ID] = identity
		}
		opts.Log.Logf("received %d users on page %d (%d unique users total)", len(page.Users), result.Pages, len(cache))

		if page.Cursor == "" {
			break
		}
		if opts.Delay > 0 {
			opts.Log.Logf("waiting %s before the next users.list request", opts.Delay)
			if err := opts.wait(ctx, opts.Delay); err != nil {
				return InitResult{}, fmt.Errorf("wait before next Slack users page: %w", err)
			}
		}
	}

	result.Users = len(cache)
	if result.MissingEmails > 0 {
		applog.Warn(fmt.Errorf("%d Slack user profiles did not include an email and were cached by user ID; email access requires both users:read and users:read.email, followed by reinstalling or reauthorizing the Slack app", result.MissingEmails))
	}

	created, err := writeCacheCreateOnly(path, cache)
	if err != nil {
		return InitResult{}, err
	}
	if !created {
		opts.Log.Logf("user cache %s was created by another process; left it unchanged", path)
		result.AlreadyExists = true
		return result, nil
	}
	opts.Log.Logf("initialized user cache %s with %d users from %d page(s)", path, result.Users, result.Pages)
	return result, nil
}

func writeCacheCreateOnly(path string, cache map[string]string) (created bool, err error) {
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return false, fmt.Errorf("create user cache directory %s: %w", dir, err)
		}
	}

	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return false, fmt.Errorf("create temporary user cache %s: %w", tmp, err)
	}
	defer func() {
		_ = f.Close()
		_ = os.Remove(tmp)
	}()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(cache); err != nil {
		return false, fmt.Errorf("write temporary user cache %s: %w", tmp, err)
	}
	if err := f.Sync(); err != nil {
		return false, fmt.Errorf("sync temporary user cache %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		return false, fmt.Errorf("close temporary user cache %s: %w", tmp, err)
	}

	// A hard link publishes the fully-written file atomically and, unlike
	// os.Rename, fails rather than replacing a destination created concurrently.
	if err := os.Link(tmp, path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return false, nil
		}
		return false, fmt.Errorf("publish user cache %s: %w", path, err)
	}
	return true, nil
}

func waitForContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
