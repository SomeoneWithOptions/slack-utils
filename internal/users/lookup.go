package users

import (
	"context"
	"fmt"
	"net/mail"
	"strings"

	"github.com/SomeoneWithOptions/slack-utils/internal/slackerr"
	"github.com/slack-go/slack"
)

// LookupByEmail returns the Slack user ID associated with email.
func LookupByEmail(ctx context.Context, api *slack.Client, email, tokenEnv string) (string, error) {
	email = strings.TrimSpace(email)
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email {
		return "", fmt.Errorf("invalid email address %q", email)
	}
	if api == nil {
		return "", fmt.Errorf("Slack client is required")
	}

	user, err := api.GetUserByEmailContext(ctx, email)
	if err != nil {
		return "", slackerr.Describe(err, slackerr.Details{
			Operation:      fmt.Sprintf("look up Slack user by email %s", email),
			Method:         slackerr.MethodUsersLookupByEmail,
			RequiredScopes: []string{"users:read.email"},
			TokenEnv:       tokenEnv,
		})
	}
	if user == nil || user.ID == "" {
		return "", fmt.Errorf("Slack returned no user ID for %s", email)
	}
	return user.ID, nil
}
