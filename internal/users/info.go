package users

import (
	"context"
	"fmt"
	"strings"

	"github.com/SomeoneWithOptions/slack-utils/internal/slackerr"
	"github.com/slack-go/slack"
)

// Info returns the Slack profile for userID.
func Info(ctx context.Context, api *slack.Client, userID, tokenEnv string) (*slack.User, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, fmt.Errorf("Slack user ID is required")
	}
	if api == nil {
		return nil, fmt.Errorf("Slack client is required")
	}

	user, err := api.GetUserInfoContext(ctx, userID)
	if err != nil {
		return nil, slackerr.Describe(err, slackerr.Details{
			Operation:      fmt.Sprintf("get Slack user info for %s", userID),
			Method:         slackerr.MethodUsersInfo,
			RequiredScopes: []string{"users:read"},
			TokenEnv:       tokenEnv,
			Hints:          []string{"To include the user's email address, also add the users:read.email scope."},
		})
	}
	if user == nil || user.ID == "" {
		return nil, fmt.Errorf("Slack returned no user info for %s", userID)
	}
	return user, nil
}
