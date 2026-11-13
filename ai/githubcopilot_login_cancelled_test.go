package ai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
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

// loginGitHubCopilot (github-copilot.ts:441, 450, 451, 456) awaits startDeviceFlow, pollForGitHubAccessToken, refreshGitHubCopilotAccessToken and
// fetchGitHubCopilotModels without a catch, so the login rejects with the failing step's own error, with no "github-copilot: <step>:" prefix.
func TestLoginGitHubCopilotStepFailuresAreTheStepsOwnErrors(t *testing.T) {
	const copilotAccessToken = "tid=x;proxy-ep=proxy.individual.githubcopilot.com"
	zeroInterval := 0.0
	deviceInfo := &deviceCodeResponse{DeviceCode: "dev-1", UserCode: "CODE-1234", VerificationURI: "https://github.com/login/device", Interval: &zeroInterval, ExpiresIn: 60}
	policy := copilotRetryPolicy{MaxRetries: 2, MaxElapsedMs: 5000}
	steps := []struct {
		name string
		path string
		step func(ctx context.Context) error
	}{
		{"device flow", "/login/device/code", func(ctx context.Context) error { _, err := startDeviceFlow(ctx, "github.com"); return err }},
		{"poll", "/login/oauth/access_token", func(ctx context.Context) error {
			_, err := pollForGitHubAccessToken(ctx, "github.com", deviceInfo)
			return err
		}},
		{"exchange", "/copilot_internal/v2/token", func(ctx context.Context) error { _, err := refreshCopilotAccessToken(ctx, "ghu_test", ""); return err }},
		{"list models", "/models", func(ctx context.Context) error {
			_, err := fetchGitHubCopilotModels(ctx, copilotAccessToken, "", policy)
			return err
		}},
	}
	for _, tc := range steps {
		t.Run(tc.name, func(t *testing.T) {
			withMockCopilotClient(t, func(req *http.Request) (*http.Response, error) {
				if req.URL.Path == tc.path {
					return copilotJSONResp(403, `{"error":"denied","error_description":"denied"}`), nil
				}
				switch req.URL.Path {
				case "/login/device/code":
					return copilotJSONResp(200, `{"device_code":"dev-1","user_code":"CODE-1234","verification_uri":"https://github.com/login/device","interval":0,"expires_in":60}`), nil
				case "/login/oauth/access_token":
					return copilotJSONResp(200, `{"access_token":"ghu_test"}`), nil
				case "/copilot_internal/v2/token":
					return copilotJSONResp(200, fmt.Sprintf(`{"token":%q,"expires_at":4102444800}`, copilotAccessToken)), nil
				}
				return nil, fmt.Errorf("unexpected request: %s", req.URL)
			})
			want := tc.step(t.Context())
			if want == nil {
				t.Fatal("the step succeeded against a 403: the test does not exercise its failure")
			}
			_, err := LoginGitHubCopilot(t.Context(), CopilotLoginCallbacks{OnPrompt: func(context.Context) (string, error) { return "", nil }})
			if err == nil || err.Error() != want.Error() {
				t.Fatalf("login error = %v, want the step's own error %q", err, want)
			}
		})
	}
}

// enableGitHubCopilotModels (github-copilot.ts:468) lets a caller cancellation through; loginGitHubCopilot does not wrap it either.
func TestLoginGitHubCopilotEnableModelsCancellationIsNotPrefixed(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	withMockCopilotClient(t, func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/login/device/code":
			return copilotJSONResp(200, `{"device_code":"dev-1","user_code":"CODE-1234","verification_uri":"https://github.com/login/device","interval":0,"expires_in":60}`), nil
		case "/login/oauth/access_token":
			return copilotJSONResp(200, `{"access_token":"ghu_test"}`), nil
		case "/copilot_internal/v2/token":
			return copilotJSONResp(200, `{"token":"tid=x;proxy-ep=proxy.individual.githubcopilot.com","expires_at":4102444800}`), nil
		case "/models":
			return copilotJSONResp(200, `{"data":[{"id":"gpt-6.1-sol","model_picker_enabled":true,"policy":{"state":"unconfigured"},"capabilities":{"supports":{"tool_calls":true}}}]}`), nil
		case "/models/gpt-6.1-sol/policy":
			cancel()
			return nil, req.Context().Err()
		}
		return nil, fmt.Errorf("unexpected request: %s", req.URL)
	})
	_, err := LoginGitHubCopilot(ctx, CopilotLoginCallbacks{OnPrompt: func(context.Context) (string, error) { return "", nil }})
	if err == nil || !errors.Is(err, context.Canceled) || strings.HasPrefix(err.Error(), "github-copilot:") {
		t.Fatalf("error = %v, want the cancellation itself with no github-copilot: prefix", err)
	}
}
