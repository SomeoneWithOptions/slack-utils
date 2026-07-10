package users

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/SomeoneWithOptions/slack-utils/internal/slackerr"
	"github.com/slack-go/slack"
)

const userPageLimit = 200

type workspaceListOptions struct {
	delay    time.Duration
	teamID   string
	tokenEnv string
	api      *slack.Client
	log      Logger
	wait     func(context.Context, time.Duration) error
}

type workspaceListResult struct {
	users         map[string]string
	pages         int
	missingEmails int
}

func listWorkspaceUsers(ctx context.Context, opts workspaceListOptions) (workspaceListResult, error) {
	result := workspaceListResult{users: make(map[string]string)}
	paginationOptions := []slack.GetUsersOption{slack.GetUsersOptionLimit(userPageLimit)}
	if teamID := strings.TrimSpace(opts.teamID); teamID != "" {
		paginationOptions = append(paginationOptions, slack.GetUsersOptionTeamID(teamID))
	}
	page := opts.api.GetUsersPaginated(paginationOptions...)

	for {
		opts.log.Logf("requesting Slack users page %d (cursor=%q, limit=%d)", result.pages+1, page.Cursor, userPageLimit)
		next, err := page.Next(ctx)
		if retryAfter := slackerr.RetryAfterSeconds(err); retryAfter > 0 {
			delay := time.Duration(retryAfter) * time.Second
			opts.log.Logf("rate limited by users.list; retrying the same page in %s", delay)
			if err := opts.wait(ctx, delay); err != nil {
				return workspaceListResult{}, fmt.Errorf("wait to retry Slack users list: %w", err)
			}
			continue
		}
		if err != nil {
			return workspaceListResult{}, slackerr.Describe(err, slackerr.Details{
				Operation:      "list Slack workspace users",
				Method:         slackerr.MethodUsersList,
				RequiredScopes: []string{"users:read"},
				TokenEnv:       opts.tokenEnv,
				Hints: []string{
					"To cache email addresses, also add the users:read.email scope.",
					"When using an Enterprise Grid org token, pass -team with the workspace team ID.",
				},
			})
		}

		page = next
		result.pages++
		for _, user := range page.Users {
			if user.ID == "" {
				continue
			}
			identity := user.Profile.Email
			if identity == "" {
				identity = user.ID
				result.missingEmails++
			}
			result.users[user.ID] = identity
		}
		opts.log.Logf("received %d users on page %d (%d unique users total)", len(page.Users), result.pages, len(result.users))

		if page.Cursor == "" {
			break
		}
		if opts.delay > 0 {
			opts.log.Logf("waiting %s before the next users.list request", opts.delay)
			if err := opts.wait(ctx, opts.delay); err != nil {
				return workspaceListResult{}, fmt.Errorf("wait before next Slack users page: %w", err)
			}
		}
	}

	return result, nil
}
