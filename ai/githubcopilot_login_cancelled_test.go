package ai

import (
	"context"
	"errors"
	"testing"
)

// loginGitHubCopilot (github-copilot.ts:428-441) rejects with the prompt's own error, with exactly "Login cancelled" when the signal aborted while the
// prompt was open, and with exactly "Invalid GitHub Enterprise URL/domain" for a domain that does not normalize. No request is made in any of the three.
func TestLoginGitHubCopilotPromptOutcomesMatchPi(t *testing.T) {
	promptFailure := errors.New("the prompt failed")
	for _, tc := range []struct {
		name   string
		prompt func(cancel context.CancelFunc) (string, error)
		want   string
	}{
		{"cancelled while the prompt was open", func(cancel context.CancelFunc) (string, error) { cancel(); return "", nil }, "Login cancelled"},
		{"cancelled prompt that also answered a domain", func(cancel context.CancelFunc) (string, error) { cancel(); return "company.ghe.com", nil }, "Login cancelled"},
		{"invalid domain", func(context.CancelFunc) (string, error) { return "not a domain!!", nil }, "Invalid GitHub Enterprise URL/domain"},
		{"prompt rejection", func(context.CancelFunc) (string, error) { return "", promptFailure }, "the prompt failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			_, err := LoginGitHubCopilot(ctx, CopilotLoginCallbacks{OnPrompt: func(context.Context) (string, error) { return tc.prompt(cancel) }})
			if err == nil || err.Error() != tc.want {
				t.Fatalf("error = %v, want exactly %q", err, tc.want)
			}
		})
	}
}
