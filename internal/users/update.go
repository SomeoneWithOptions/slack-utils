package users

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/SomeoneWithOptions/slack-utils/internal/applog"
	"github.com/slack-go/slack"
)

// UpdateOptions configures additive updates to an existing user cache.
type UpdateOptions struct {
	Path     string
	Delay    time.Duration
	TeamID   string
	TokenEnv string
	API      *slack.Client
	Log      Logger
	wait     func(context.Context, time.Duration) error
}

// UpdateResult summarizes an additive user cache update.
type UpdateResult struct {
	Added          int
	Total          int
	WorkspaceUsers int
	Pages          int
	MissingEmails  int
}

// Update fetches all workspace users and adds only IDs absent from the existing
// cache. Existing entries are never changed or removed.
func Update(ctx context.Context, opts UpdateOptions) (UpdateResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	path := strings.TrimSpace(opts.Path)
	if path == "" {
		return UpdateResult{}, fmt.Errorf("user cache path must not be empty")
	}
	if opts.Delay < 0 {
		return UpdateResult{}, fmt.Errorf("user list delay must be >= 0")
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
		return UpdateResult{}, err
	}
	if !exists {
		return UpdateResult{}, fmt.Errorf("user cache %s does not exist; run `slack-utils users init -output %q` first", path, path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return UpdateResult{}, fmt.Errorf("read user cache %s: %w", path, err)
	}
	cache := make(map[string]string)
	if err := json.Unmarshal(data, &cache); err != nil {
		return UpdateResult{}, fmt.Errorf("parse user cache %s: %w", path, err)
	}
	if cache == nil {
		cache = make(map[string]string)
	}
	if opts.API == nil {
		return UpdateResult{}, fmt.Errorf("Slack client is required to update %s", path)
	}

	listed, err := listWorkspaceUsers(ctx, workspaceListOptions{
		delay:    opts.Delay,
		teamID:   opts.TeamID,
		tokenEnv: opts.TokenEnv,
		api:      opts.API,
		log:      opts.Log,
		wait:     opts.wait,
	})
	if err != nil {
		return UpdateResult{}, err
	}

	result := UpdateResult{
		WorkspaceUsers: len(listed.users),
		Pages:          listed.pages,
	}
	for userID, identity := range listed.users {
		if _, exists := cache[userID]; exists {
			continue
		}
		cache[userID] = identity
		result.Added++
		if identity == userID {
			result.MissingEmails++
		}
	}
	result.Total = len(cache)

	if result.Added == 0 {
		opts.Log.Logf("user cache %s is already up to date", path)
		return result, nil
	}
	if result.MissingEmails > 0 {
		applog.Warn(fmt.Errorf("%d newly added Slack user profiles did not include an email and were cached by user ID; email access requires both users:read and users:read.email, followed by reinstalling or reauthorizing the Slack app", result.MissingEmails))
	}
	if err := writeCacheReplace(path, cache); err != nil {
		return UpdateResult{}, err
	}
	opts.Log.Logf("updated user cache %s with %d new users", path, result.Added)
	return result, nil
}

func writeCacheReplace(path string, cache map[string]string) (err error) {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary user cache in %s: %w", dir, err)
	}
	tmp := f.Name()
	defer func() {
		_ = f.Close()
		_ = os.Remove(tmp)
	}()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(cache); err != nil {
		return fmt.Errorf("write temporary user cache %s: %w", tmp, err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync temporary user cache %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close temporary user cache %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replace user cache %s: %w", path, err)
	}
	return nil
}
