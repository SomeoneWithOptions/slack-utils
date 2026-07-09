// Package users resolves Slack user IDs to emails with a local JSON cache.
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

// Logger is the progress logger used by the resolver.
type Logger interface {
	Logf(format string, args ...any)
}

// Resolver looks up Slack users, caching results on disk.
type Resolver struct {
	ctx               context.Context
	api               *slack.Client
	delay             time.Duration
	path              string
	cache             map[string]string
	dirty             bool
	seen              map[string]bool
	logEvents         map[string]bool
	fetchAttempts     int
	emailWarningShown bool
	log               Logger
	tokenEnv          string
}

// NewResolver loads an existing user cache from path when present.
func NewResolver(ctx context.Context, api *slack.Client, path string, delay time.Duration, log Logger, tokenEnv string) (*Resolver, error) {
	if log == nil {
		log = applog.New()
	}
	if tokenEnv == "" {
		tokenEnv = "SLACK_TOKEN"
	}

	cache := make(map[string]string)
	if path != "" {
		if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
			if err := json.Unmarshal(data, &cache); err != nil {
				return nil, fmt.Errorf("parse user cache %s: %w", path, err)
			}
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("read user cache %s: %w", path, err)
		}
	}
	return &Resolver{
		ctx:       ctx,
		api:       api,
		delay:     delay,
		path:      path,
		cache:     cache,
		seen:      make(map[string]bool),
		logEvents: make(map[string]bool),
		log:       log,
		tokenEnv:  tokenEnv,
	}, nil
}

// Prefetch bulk-loads profiles for the provided user IDs into the cache.
// Unknown or non-resolvable IDs are ignored. Already-cached IDs are skipped.
func (r *Resolver) Prefetch(ctx context.Context, userIDs []string) error {
	if r.api == nil || len(userIDs) == 0 {
		return nil
	}
	if ctx == nil {
		ctx = r.ctx
	}

	pending := make([]string, 0, len(userIDs))
	seen := make(map[string]struct{}, len(userIDs))
	for _, userID := range userIDs {
		if !IsResolvableUserID(userID) {
			continue
		}
		if email, ok := r.cache[userID]; ok && email != "" {
			continue
		}
		if _, ok := seen[userID]; ok {
			continue
		}
		seen[userID] = struct{}{}
		pending = append(pending, userID)
	}
	if len(pending) == 0 {
		return nil
	}

	const batchSize = 30
	for start := 0; start < len(pending); start += batchSize {
		end := start + batchSize
		if end > len(pending) {
			end = len(pending)
		}
		batch := pending[start:end]
		r.log.Logf("prefetching profiles for %d users from Slack (batch %d-%d of %d)", len(batch), start+1, end, len(pending))

		usersInfo, err := r.fetchUsersInfo(ctx, batch)
		if err != nil {
			return err
		}

		found := make(map[string]struct{}, len(usersInfo))
		for _, user := range usersInfo {
			found[user.ID] = struct{}{}
			r.storeUser(&user)
		}
		for _, userID := range batch {
			if _, ok := found[userID]; ok {
				continue
			}
			// Keep unresolved members as IDs so Lookup does not retry them.
			r.cache[userID] = userID
			r.dirty = true
			r.warnMissingEmailScopeOnce(userID)
		}

		if end < len(pending) && r.delay > 0 {
			r.log.Logf("waiting %s before next user prefetch batch", r.delay)
			time.Sleep(r.delay)
		}
	}
	return nil
}

// Lookup returns an email or other identity string for userID.
func (r *Resolver) Lookup(userID string) string {
	if userID == "" {
		return userID
	}
	if email, ok := r.cache[userID]; ok && email != "" {
		r.logOnce("cache", userID, "resolved user %s from cache as %s", userID, email)
		return email
	}
	if !IsResolvableUserID(userID) {
		r.logOnce("skip", userID, "skipping lookup for non-resolvable user identifier %s", userID)
		return userID
	}
	r.fetchAttempts++
	attempt := r.fetchAttempts
	r.log.Logf("fetching profile for user %s from Slack (attempt %d)", userID, attempt)
	user, err := r.fetchUser(r.ctx, userID)
	if err != nil {
		if attempt == 1 {
			applog.Fail(err)
		}
		r.log.Logf("failed to resolve user %s via Slack: %v", userID, err)
		r.warnOnce(userID, err)
		r.cache[userID] = userID
		r.dirty = true
		return userID
	}
	email := r.storeUser(user)
	if email == userID {
		r.log.Logf("Slack profile for user %s did not include an email; defaulting to ID", userID)
	} else {
		r.log.Logf("resolved user %s to email %s", userID, email)
	}
	r.log.Logf("caching resolved identity for user %s", userID)
	return email
}

// Save persists dirty cache entries to the configured path.
func (r *Resolver) Save() error {
	if !r.dirty || r.path == "" {
		return nil
	}
	r.log.Logf("persisting user cache to %s (%d entries)", r.path, len(r.cache))
	if dir := filepath.Dir(r.path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create user cache directory %s: %w", dir, err)
		}
	}
	tmp := r.path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("create temporary user cache %s: %w", tmp, err)
	}
	enc := json.NewEncoder(f)
	if err := enc.Encode(r.cache); err != nil {
		f.Close()
		return fmt.Errorf("write temporary user cache %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close temporary user cache %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, r.path); err != nil {
		return fmt.Errorf("replace user cache %s with %s: %w", r.path, tmp, err)
	}
	r.log.Logf("user cache persisted to %s", r.path)
	return nil
}

func (r *Resolver) fetchUser(ctx context.Context, userID string) (*slack.User, error) {
	for {
		user, err := r.api.GetUserInfoContext(ctx, userID)
		if rl := slackerr.RetryAfterSeconds(err); rl > 0 {
			r.log.Logf("rate limited while resolving user %s; retrying in %d seconds", userID, rl)
			time.Sleep(time.Duration(rl) * time.Second)
			continue
		}
		if err != nil {
			r.log.Logf("Slack returned an error while resolving user %s: %v", userID, err)
			return nil, r.userInfoError(err, userID)
		}
		if r.delay > 0 {
			r.log.Logf("waiting %s before next user info request", r.delay)
			time.Sleep(r.delay)
		}
		return user, nil
	}
}

func (r *Resolver) fetchUsersInfo(ctx context.Context, userIDs []string) ([]slack.User, error) {
	for {
		usersPtr, err := r.api.GetUsersInfoContext(ctx, userIDs...)
		if rl := slackerr.RetryAfterSeconds(err); rl > 0 {
			r.log.Logf("rate limited while prefetching %d users; retrying in %d seconds", len(userIDs), rl)
			time.Sleep(time.Duration(rl) * time.Second)
			continue
		}
		if err != nil {
			r.log.Logf("Slack returned an error while prefetching users: %v", err)
			return nil, r.userInfoError(err, strings.Join(userIDs, ", "))
		}
		if usersPtr == nil {
			return nil, nil
		}
		return *usersPtr, nil
	}
}

func (r *Resolver) storeUser(user *slack.User) string {
	if user == nil || user.ID == "" {
		return ""
	}
	email := user.ID
	if user.Profile.Email != "" {
		email = user.Profile.Email
	} else {
		r.warnMissingEmailScopeOnce(user.ID)
	}
	r.cache[user.ID] = email
	r.dirty = true
	return email
}

func (r *Resolver) userInfoError(err error, subject string) error {
	return slackerr.Describe(err, slackerr.Details{
		Operation:      fmt.Sprintf("resolve Slack user profile for %s", subject),
		Method:         slackerr.MethodUsersInfo,
		RequiredScopes: []string{"users:read"},
		TokenEnv:       r.tokenEnv,
		Hints: []string{
			"To include email addresses in exports, also add the users:read.email scope.",
		},
	})
}

// IsResolvableUserID reports whether id looks like a workspace member/app user ID.
func IsResolvableUserID(userID string) bool {
	return strings.HasPrefix(userID, "U") || strings.HasPrefix(userID, "W")
}

func (r *Resolver) logOnce(event, userID string, format string, args ...any) {
	if r.logEvents == nil {
		r.logEvents = make(map[string]bool)
	}
	key := event + ":" + userID
	if r.logEvents[key] {
		return
	}
	r.logEvents[key] = true
	r.log.Logf(format, args...)
}

func (r *Resolver) warnOnce(userID string, err error) {
	if r.seen == nil {
		r.seen = make(map[string]bool)
	}
	if r.seen[userID] {
		return
	}
	applog.Warn(fmt.Errorf("could not resolve Slack user %s to an email: %w", userID, err))
	r.seen[userID] = true
}

func (r *Resolver) warnMissingEmailScopeOnce(userID string) {
	if r.emailWarningShown {
		return
	}
	applog.Warn(fmt.Errorf("Slack profile for user %s did not include an email; exported users will fall back to Slack IDs. If you need email addresses, add the users:read.email scope, reinstall/reauthorize the Slack app, delete stale %s entries if needed, and rerun", userID, r.path))
	r.emailWarningShown = true
}
