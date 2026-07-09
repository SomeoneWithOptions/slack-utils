// Package slackerr formats Slack API errors with actionable OAuth-scope guidance.
package slackerr

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/slack-go/slack"
)

// Slack API methods referenced in user-facing error messages.
const (
	MethodConversationsHistory = "conversations.history"
	MethodConversationsInfo    = "conversations.info"
	MethodConversationsReplies = "conversations.replies"
	MethodUsersInfo            = "users.info"
)

var scopePattern = regexp.MustCompile(`\b[a-zA-Z0-9.-]+:[a-zA-Z0-9.:-]+\b`)

// Details captions a failed Slack call for friendlier diagnostics.
type Details struct {
	Operation      string
	Method         string
	RequiredScopes []string
	Hints          []string
	TokenEnv       string
}

type apiError struct {
	err     error
	details Details
}

// Describe wraps err with contextual guidance when non-nil.
func Describe(err error, details Details) error {
	if err == nil {
		return nil
	}
	return &apiError{err: err, details: details}
}

func (e *apiError) Error() string {
	operation := strings.TrimSpace(e.details.Operation)
	if operation == "" {
		operation = "Slack API request"
	}

	var b strings.Builder
	if method := strings.TrimSpace(e.details.Method); method != "" {
		fmt.Fprintf(&b, "%s failed (Slack API method: %s)", operation, method)
	} else {
		fmt.Fprintf(&b, "%s failed", operation)
	}

	code, isSlackErr := ErrorCode(e.err)
	if isSlackErr && code != "" {
		fmt.Fprintf(&b, ": %s", code)
	} else {
		fmt.Fprintf(&b, ": %v", e.err)
	}

	if isSlackErr && code == "missing_scope" {
		scopes := EffectiveMissingScopes(e.err, e.details.RequiredScopes)
		if len(scopes) == 1 {
			fmt.Fprintf(&b, "\nmissing Slack OAuth scope: %s", scopes[0])
		} else if len(scopes) > 1 {
			b.WriteString("\nrequired Slack OAuth scope(s):")
			for _, scope := range scopes {
				fmt.Fprintf(&b, "\n  - %s", scope)
			}
		} else {
			b.WriteString("\nSlack reported a missing OAuth scope, but the response did not include the scope name.")
		}
	}

	metadata := collectMetadata(e.err)
	if len(metadata.messages) > 0 {
		b.WriteString("\nSlack response messages:")
		for _, msg := range metadata.messages {
			fmt.Fprintf(&b, "\n  - %s", msg)
		}
	}
	if len(metadata.warnings) > 0 {
		b.WriteString("\nSlack response warnings:")
		for _, warning := range metadata.warnings {
			fmt.Fprintf(&b, "\n  - %s", warning)
		}
	}
	if len(metadata.responseErrors) > 0 {
		b.WriteString("\nSlack response errors:")
		for _, responseErr := range metadata.responseErrors {
			fmt.Fprintf(&b, "\n  - %s", responseErr)
		}
	}

	hints := e.hints(code, isSlackErr)
	if len(hints) > 0 {
		b.WriteString("\nhow to fix:")
		for _, hint := range hints {
			fmt.Fprintf(&b, "\n  - %s", hint)
		}
	}

	return b.String()
}

func (e *apiError) Unwrap() error {
	return e.err
}

func (e *apiError) hints(code string, isSlackErr bool) []string {
	hints := append([]string{}, e.details.Hints...)
	tokenEnv := e.details.TokenEnv
	if tokenEnv == "" {
		tokenEnv = "SLACK_TOKEN"
	}

	if !isSlackErr {
		hints = append(hints,
			"Check your network connection and that Slack's API is reachable from this machine.",
			"Rerun with a larger -delay if Slack or a proxy is throttling requests.",
		)
		return UniqueStrings(hints)
	}

	switch code {
	case "missing_scope":
		hints = append(hints,
			"Add the missing scope under your Slack app's OAuth & Permissions page.",
			"Reinstall or reauthorize the Slack app so the token receives the new scope.",
			fmt.Sprintf("Update %s with the new token, then rerun the command.", tokenEnv),
		)
	case "invalid_auth", "not_authed", "token_revoked", "account_inactive":
		hints = append(hints,
			fmt.Sprintf("Verify %s is set to a valid, active Slack token.", tokenEnv),
			"If the Slack app was reinstalled or rotated, export the new token before rerunning.",
		)
	case "channel_not_found":
		hints = append(hints,
			"Verify -channel is a Slack channel ID such as C..., G..., or D... (not the channel name).",
			"Make sure the token can access the conversation; invite the app/user to the channel when needed.",
		)
	case "not_in_channel":
		hints = append(hints,
			"Invite the Slack app/user represented by the token to the channel, then rerun the command.",
		)
	case "no_permission":
		hints = append(hints,
			"Check that the Slack app has permission to access this workspace and conversation.",
		)
	}

	return UniqueStrings(hints)
}

type errorMetadata struct {
	messages       []string
	warnings       []string
	responseErrors []string
}

// ErrorCode returns the Slack error code when err is a SlackErrorResponse.
func ErrorCode(err error) (string, bool) {
	resp, ok := errorResponse(err)
	if !ok {
		return "", false
	}
	return resp.Err, true
}

func errorResponse(err error) (slack.SlackErrorResponse, bool) {
	var resp slack.SlackErrorResponse
	if errors.As(err, &resp) {
		return resp, true
	}
	return slack.SlackErrorResponse{}, false
}

func collectMetadata(err error) errorMetadata {
	resp, ok := errorResponse(err)
	if !ok {
		return errorMetadata{}
	}
	metadata := errorMetadata{
		messages: resp.ResponseMetadata.Messages,
		warnings: resp.ResponseMetadata.Warnings,
	}
	for _, responseErr := range resp.Errors {
		metadata.responseErrors = append(metadata.responseErrors, formatResponseError(responseErr))
	}
	metadata.messages = UniqueStrings(metadata.messages)
	metadata.warnings = UniqueStrings(metadata.warnings)
	metadata.responseErrors = UniqueStrings(metadata.responseErrors)
	return metadata
}

func formatResponseError(err slack.SlackResponseErrors) string {
	switch {
	case err.Message != nil:
		return *err.Message
	case err.AppsManifestCreateResponseError != nil:
		appErr := err.AppsManifestCreateResponseError
		parts := []string{}
		if appErr.Code != "" {
			parts = append(parts, appErr.Code)
		}
		if appErr.Pointer != "" {
			parts = append(parts, appErr.Pointer)
		}
		if appErr.Message != "" {
			parts = append(parts, appErr.Message)
		}
		return strings.Join(parts, ": ")
	case err.ConversationsInviteResponseError != nil:
		inviteErr := err.ConversationsInviteResponseError
		if inviteErr.User != "" {
			return fmt.Sprintf("user %s: %s", inviteErr.User, inviteErr.Error)
		}
		return inviteErr.Error
	default:
		return ""
	}
}

// EffectiveMissingScopes extracts missing scopes from Slack metadata, falling back to defaults.
func EffectiveMissingScopes(err error, fallback []string) []string {
	metadata := collectMetadata(err)
	var scopes []string
	texts := append([]string{}, metadata.messages...)
	texts = append(texts, metadata.warnings...)
	texts = append(texts, metadata.responseErrors...)
	for _, text := range texts {
		scopes = append(scopes, extractScopes(text)...)
	}
	if len(scopes) == 0 {
		scopes = fallback
	}
	return UniqueStrings(scopes)
}

func extractScopes(text string) []string {
	return UniqueStrings(scopePattern.FindAllString(text, -1))
}

// ConversationScopesFor picks the OAuth scope(s) needed for a conversation kind.
func ConversationScopesFor(channelID string, info *slack.Channel, publicScope, privateScope, imScope, mpimScope string) []string {
	if info != nil {
		switch {
		case info.IsIM:
			return ScopeList(imScope)
		case info.IsMpIM:
			return ScopeList(mpimScope)
		case info.IsPrivate || info.IsGroup:
			return ScopeList(privateScope)
		case info.IsChannel:
			return ScopeList(publicScope)
		}
	}

	channelID = strings.ToUpper(strings.TrimSpace(channelID))
	switch {
	case strings.HasPrefix(channelID, "C"):
		return ScopeList(publicScope)
	case strings.HasPrefix(channelID, "D"):
		return ScopeList(imScope)
	case strings.HasPrefix(channelID, "G"):
		return ScopeList(privateScope, mpimScope)
	default:
		return ScopeList(publicScope, privateScope, imScope, mpimScope)
	}
}

// ConversationScopeHints returns extra guidance for ambiguous conversation IDs.
func ConversationScopeHints(channelID, privateScope, mpimScope string) []string {
	channelID = strings.ToUpper(strings.TrimSpace(channelID))
	if !strings.HasPrefix(channelID, "G") {
		return nil
	}
	return []string{fmt.Sprintf("G... conversation IDs can be private channels or multi-person DMs; use %s for private channels or %s for multi-person DMs.", privateScope, mpimScope)}
}

// ScopeList filters empty values and deduplicates scopes.
func ScopeList(scopes ...string) []string {
	var out []string
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if scope != "" {
			out = append(out, scope)
		}
	}
	return UniqueStrings(out)
}

// UniqueStrings returns unique non-empty trimmed strings in first-seen order.
func UniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

// RetryAfterSeconds returns the number of seconds to wait before retrying a rate-limited call.
func RetryAfterSeconds(err error) int {
	if err == nil {
		return 0
	}
	var slackRateLimited *slack.RateLimitedError
	if errors.As(err, &slackRateLimited) && slackRateLimited.RetryAfter > 0 {
		return int((slackRateLimited.RetryAfter + time.Second - 1) / time.Second)
	}
	var rle interface{ RetryAfter() int }
	if errors.As(err, &rle) {
		return rle.RetryAfter()
	}
	if strings.Contains(err.Error(), "rate_limited") || strings.Contains(err.Error(), "429") {
		return 30
	}
	return 0
}
