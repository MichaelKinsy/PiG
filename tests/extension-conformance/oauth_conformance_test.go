package extensionconformance

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// oauthRecording is the normalized cross-transport observation for the OAuth
// extension bridge. Every field is deterministic in the fixture, so the three
// subprocess SDKs must produce byte-identical recordings.
type oauthRecording struct {
	ProviderID         string   `json:"provider_id"`
	ProviderName       string   `json:"provider_name"`
	IsSubscription     bool     `json:"is_subscription"`
	DeviceCode         string   `json:"device_code"`
	Progress           []string `json:"progress"`
	PromptMessage      string   `json:"prompt_message"`
	LoginAccess        string   `json:"login_access"`
	LoginRefresh       string   `json:"login_refresh"`
	LoginExpires       int64    `json:"login_expires"`
	APIKey             string   `json:"api_key"`
	APIKeyFailed       bool     `json:"api_key_failed"`
	RefreshAccess      string   `json:"refresh_access"`
	RefreshExpires     int64    `json:"refresh_expires"`
	CredStatusPresent  bool     `json:"cred_status_present"`
	CredStatusAuthType string   `json:"cred_status_auth_type"`
	CredStatusSource   string   `json:"cred_status_source"`
	StorePath          string   `json:"store_path"`
	Deleted            bool     `json:"deleted"`
}

// TestConformance_OAuthTransportsMatch drives the canonical OAuth provider each
// subprocess fixture registers through the shared ai OAuth registry, then
// asserts the Go/Rust/Python recordings are identical. The OAuth bridge does
// not exist in-process (the inproc runner has no provider registry path), so
// unlike TestConformance_TransportsMatch this suite is subprocess-only: the
// three shipped SDKs are compared to each other, which is where wire/dispatch
// drift can appear.
func TestConformance_OAuthTransportsMatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping OAuth conformance suite in short mode (builds subprocess fixtures)")
	}

	cases := []struct {
		name string
		make func(*testing.T) *harness
	}{
		{"subprocess-go", makeSubprocessGoHarness},
		{"subprocess-rust", makeSubprocessRustHarness},
		{"subprocess-python", makeSubprocessPythonHarness},
		{"fused-go", makeFusedGoHarness},
	}

	var baseline oauthRecording
	var baselineName string
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			got := captureOAuthRecording(t)
			if i == 0 {
				baseline = got
				baselineName = tc.name
				return
			}
			if baselineName == "" {
				t.Skip("baseline subtest was filtered out by -run; run the full suite")
			}
			if !reflect.DeepEqual(got, baseline) {
				gb, _ := json.MarshalIndent(got, "", "  ")
				wb, _ := json.MarshalIndent(baseline, "", "  ")
				t.Fatalf("transport %q OAuth diverged from %q baseline\n--- baseline (%s) ---\n%s\n--- got (%s) ---\n%s",
					tc.name, baselineName, baselineName, wb, tc.name, gb)
			}
		})
	}
}

// captureOAuthRecording exercises the OAuth provider the loaded fixture just
// registered: a login flow (device code + progress + value-returning prompt),
// key resolution, token refresh, and the credential store.
func captureOAuthRecording(t *testing.T) oauthRecording {
	t.Helper()

	provider, ok := ai.GetOAuthProvider("conformance-oauth")
	if !ok {
		t.Fatal("conformance-oauth provider not registered by fixture")
	}

	rec := oauthRecording{ProviderID: provider.ID(), ProviderName: provider.Name(), IsSubscription: ai.IsOAuthSubscriptionProvider(provider.ID())}
	// Pi requires explicit true. A lost field must fail, not match another SDK's false default.
	if !rec.IsSubscription {
		t.Fatal("extension OAuth isSubscription=true was lost during registration")
	}

	cb := ai.OAuthLoginCallbacks{
		OnDeviceCode: func(info ai.OAuthDeviceCodeInfo) {
			rec.DeviceCode = info.UserCode + "|" + info.VerificationURI
		},
		OnProgress: func(message string) {
			rec.Progress = append(rec.Progress, message)
		},
		OnPrompt: func(prompt ai.OAuthPrompt) (string, error) {
			rec.PromptMessage = prompt.Message
			return "typed-CONF", nil
		},
	}
	creds, err := provider.Login(cb)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	rec.LoginAccess, rec.LoginRefresh, rec.LoginExpires = creds.Access, creds.Refresh, creds.Expires

	rec.APIKey = provider.GetAPIKey(ai.OAuthCredentials{Access: "abc"})
	rec.APIKeyFailed = oauthAPIKeyError(t) != nil
	if !rec.APIKeyFailed {
		t.Fatal("a getApiKey that throws resolved without an error")
	}

	refreshed, err := provider.RefreshToken(ai.OAuthCredentials{Refresh: "seed-refresh"})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	rec.RefreshAccess, rec.RefreshExpires = refreshed.Access, refreshed.Expires

	store, ok := provider.(ai.OAuthCredentialStore)
	if !ok {
		t.Fatal("provider does not expose a credential store")
	}
	status, present := store.OAuthCredentialStatus()
	rec.CredStatusPresent = present
	rec.CredStatusAuthType, rec.CredStatusSource = status.AuthType, status.Source

	path, err := store.StoreOAuthCredentials(ai.OAuthCredentials{Access: "x"})
	if err != nil {
		t.Fatalf("store credentials: %v", err)
	}
	rec.StorePath = path

	deleted, err := store.DeleteOAuthCredentials()
	if err != nil {
		t.Fatalf("delete credentials: %v", err)
	}
	rec.Deleted = deleted
	return rec
}

// oauthAPIKeyError resolves a key for credentials whose getApiKey throws in
// every fixture, through the same path a model call uses. Upstream lets that
// exception reach the model call instead of sending an empty key.
func oauthAPIKeyError(t *testing.T) error {
	t.Helper()
	credentials := map[string]ai.OAuthCredentials{"conformance-oauth": {Access: "boom", Expires: time.Now().Add(time.Hour).UnixMilli()}}
	_, key, err := ai.GetOAuthAPIKeyContext(t.Context(), "conformance-oauth", credentials)
	if err == nil && key != "" {
		t.Fatalf("failing getApiKey resolved key %q", key)
	}
	return err
}
