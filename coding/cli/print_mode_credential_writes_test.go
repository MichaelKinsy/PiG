package cli

// Pi main b2cb62923 (#11692): print and JSON mode dispose the runtime in the finally of runPrintMode, which waits for a started OAuth refresh to be saved.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
)

// A prompt-time or background refresh started before the run ended is saved before print mode returns, in text and JSON mode. The fake token endpoint (the extension provider's RefreshToken) has already spent "old-refresh" when its request is in flight; red without the wait: print mode returns at once and auth.json keeps the spent token.
func TestPrintModeWaitsForAStartedOAuthRefreshToBeSavedBeforeReturning(t *testing.T) {
	for _, mode := range []string{"text", "json"} {
		t.Run(mode, func(t *testing.T) {
			provider := ai.NewFauxProvider(ai.FauxConfig{})
			provider.SetResponses(fauxSteps(fauxTextResponse("done")))
			host := printModeTestHost(t, provider)
			services := host.Services
			if err := services.Auth().Set("rotating-oauth", ai.Credential{Type: ai.CredentialOAuth, Access: "old-access", Refresh: "old-refresh", Expires: time.Now().Add(-time.Second).UnixMilli()}); err != nil {
				t.Fatal(err)
			}
			started, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			defer releaseOnce.Do(func() { close(release) })
			model := &ai.Model{ID: "model", DisplayName: "model", Input: []string{"text"}, Capabilities: ai.ModelCapabilities{ContextWindow: 10000, MaxOutputTokens: 1000}}
			if err := services.ModelRuntime().RegisterProvider("rotating-oauth", coding.ProviderConfigInput{Name: "Rotating OAuth", BaseURL: "https://example.test/v1", API: ai.APIOpenAICompletions, Models: []ai.AnyModel{model}, OAuth: &coding.ExtensionOAuthConfig{Name: "Rotating subscription", Login: func(context.Context, ai.OAuthLoginCallbacks) (ai.Credential, error) {
				return ai.Credential{}, errors.New("unused")
			}, RefreshToken: func(_ context.Context, _ ai.Credential) (ai.Credential, error) {
				close(started)
				<-release
				return ai.Credential{Type: ai.CredentialOAuth, Access: "new-access", Refresh: "new-refresh", Expires: time.Now().Add(time.Hour).UnixMilli()}, nil
			}, GetAPIKey: func(value ai.Credential) string { return value.Access }}}); err != nil {
				t.Fatal(err)
			}
			go func() {
				_, _ = services.Registry().NativeModels().GetAuth(t.Context(), "rotating-oauth", ai.AuthResolutionOverrides{})
			}()
			<-started

			done := make(chan printModeTestResult, 1)
			go func() { done <- runPrintModeForTest(t, host, printModeOptions{Mode: mode, InitialMessage: "go"}) }()
			select {
			case result := <-done:
				t.Fatalf("print mode returned (%v) while the token request was in flight", result.err)
			case <-time.After(300 * time.Millisecond):
			}
			releaseOnce.Do(func() { close(release) })
			result := <-done
			if result.err != nil || result.stderr != "" {
				t.Fatalf("run = %v, stderr %q", result.err, result.stderr)
			}
			credential, err := services.Registry().ReadCredential(t.Context(), "rotating-oauth")
			if err != nil || credential == nil || credential.Access != "new-access" || credential.Refresh != "new-refresh" {
				t.Fatalf("auth.json after print mode returned = %+v, %v; want the rotated token", credential, err)
			}
		})
	}
}
