package sdk

import (
	"encoding/json"
	"testing"
	"time"
)

type fakeStore struct {
	stored OAuthCredentials
}

func (f *fakeStore) CredentialStatus() OAuthCredentialStatus {
	return OAuthCredentialStatus{Present: f.stored.Access != "", AuthType: "oauth", Source: "fake"}
}
func (f *fakeStore) StoreCredentials(creds OAuthCredentials) (string, error) {
	f.stored = creds
	return "/fake/path", nil
}
func (f *fakeStore) DeleteCredentials() (bool, error) {
	f.stored = OAuthCredentials{}
	return true, nil
}

// The register payload advertises OAuth capability flags (never closures), and
// the host's oauth_* requests dispatch to the provider: login relays callbacks
// to the host and round-trips a value-returning prompt; refresh, getApiKey, and
// the credential store all resolve.
func TestExtension_OAuthProviderBridge(t *testing.T) {
	host := newMockHost(t)
	defer host.close()

	store := &fakeStore{}
	ext := New("test-ext")
	ext.RegisterProvider("example-provider", ProviderConfig{
		"baseUrl": "https://example.com",
		"oauth": &OAuthProvider{
			IsSubscription: true,
			Login: func(cb *OAuthLoginCallbacks) (OAuthCredentials, error) {
				cb.OnDeviceCode(OAuthDeviceCodeInfo{UserCode: "WXYZ", VerificationURI: "https://verify"})
				got, err := cb.OnPrompt(OAuthPrompt{Message: "URL"})
				if err != nil {
					return OAuthCredentials{}, err
				}
				return OAuthCredentials{Access: "tok:" + got, Expires: 999}, nil
			},
			RefreshToken: func(creds OAuthCredentials) (OAuthCredentials, error) {
				return OAuthCredentials{Access: "fresh", Refresh: "r2", Expires: 42}, nil
			},
			GetAPIKey: func(creds OAuthCredentials) string {
				return "key-for-" + creds.Access
			},
			CredentialStore: store,
		},
	})

	t.Setenv("PIG_EXT_SOCKET", host.sockPath)

	done := make(chan error, 1)
	go func() { done <- ext.Run() }()

	host.accept(t)
	reg := host.readEnvelope(t)

	// Register payload carries the capability flags, no closures.
	if len(reg.Register.Providers) != 1 {
		t.Fatalf("providers = %+v", reg.Register.Providers)
	}
	var cfg struct {
		OAuth providerOAuthConfig `json:"oauth"`
	}
	if err := json.Unmarshal(reg.Register.Providers[0].Config, &cfg); err != nil {
		t.Fatalf("decode provider config: %v", err)
	}
	if !cfg.OAuth.IsSubscription {
		t.Fatal("subscription metadata missing from registration")
	}
	if cfg.OAuth.Name != "example-provider" || !cfg.OAuth.HasLogin || !cfg.OAuth.HasRefresh ||
		!cfg.OAuth.HasGetAPIKey || !cfg.OAuth.HasCredentialStore {
		t.Fatalf("oauth flags = %+v", cfg.OAuth)
	}
	if bytes := string(reg.Register.Providers[0].Config); contains(bytes, "func") {
		t.Fatalf("provider config leaked a closure: %s", bytes)
	}

	host.writeEnvelope(t, envelope{Type: msgReady, Ready: &readyMsg{Cwd: "/tmp", Width: 80}})
	time.Sleep(50 * time.Millisecond)

	// oauth_login: the extension drives callbacks, then returns creds.
	host.writeEnvelope(t, envelope{
		Type:    msgRequest,
		ID:      "login-1",
		Request: &requestMsg{Method: methodOAuthLogin, Tool: "example-provider"},
	})

	// onDeviceCode call: ack it.
	dc := host.readEnvelope(t)
	if dc.Type != msgCall || dc.Call.Method != callOAuthOnDeviceCode {
		t.Fatalf("expected onDeviceCode call, got %s/%v", dc.Type, dc.Call)
	}
	if dc.Call.ParentRequestID != "login-1" {
		t.Fatalf("oauth callback parent request = %q, want login-1", dc.Call.ParentRequestID)
	}
	var dcArgs OAuthDeviceCodeInfo
	_ = json.Unmarshal(dc.Call.Args, &dcArgs)
	if dcArgs.UserCode != "WXYZ" || dcArgs.VerificationURI != "https://verify" {
		t.Fatalf("device code args = %+v", dcArgs)
	}
	host.writeEnvelope(t, envelope{Type: msgCallResult, ID: dc.ID, CallResult: &callResultMsg{}})

	// onPrompt call: return a value.
	pr := host.readEnvelope(t)
	if pr.Type != msgCall || pr.Call.Method != callOAuthOnPrompt {
		t.Fatalf("expected onPrompt call, got %s/%v", pr.Type, pr.Call)
	}
	inputRes, _ := json.Marshal(oauthInputResult{Value: "typed-value"})
	host.writeEnvelope(t, envelope{Type: msgCallResult, ID: pr.ID, CallResult: &callResultMsg{Result: inputRes}})

	// login response: creds echo the prompt value.
	loginResp := host.readEnvelope(t)
	if loginResp.Type != "response" || loginResp.ID != "login-1" {
		t.Fatalf("login response = %+v", loginResp)
	}
	if loginResp.Response.Error != nil {
		t.Fatalf("login error: %+v", loginResp.Response.Error)
	}
	var loginCreds OAuthCredentials
	_ = json.Unmarshal(loginResp.Response.Result, &loginCreds)
	if loginCreds.Access != "tok:typed-value" {
		t.Fatalf("login creds = %+v, prompt value did not round-trip", loginCreds)
	}

	// oauth_get_api_key.
	credArgs, _ := json.Marshal(OAuthCredentials{Access: "abc"})
	host.writeEnvelope(t, envelope{Type: msgRequest, ID: "key-1", Request: &requestMsg{Method: methodOAuthGetAPIKey, Tool: "example-provider", Args: credArgs}})
	keyResp := host.readEnvelope(t)
	var keyRes oauthAPIKeyResult
	_ = json.Unmarshal(keyResp.Response.Result, &keyRes)
	if keyRes.APIKey != "key-for-abc" {
		t.Fatalf("api key = %q", keyRes.APIKey)
	}

	// oauth_refresh.
	host.writeEnvelope(t, envelope{Type: msgRequest, ID: "ref-1", Request: &requestMsg{Method: methodOAuthRefresh, Tool: "example-provider", Args: credArgs}})
	refResp := host.readEnvelope(t)
	var refCreds OAuthCredentials
	_ = json.Unmarshal(refResp.Response.Result, &refCreds)
	if refCreds.Access != "fresh" || refCreds.Refresh != "r2" || refCreds.Expires != 42 {
		t.Fatalf("refresh creds = %+v", refCreds)
	}

	// oauth_store_credentials then oauth_credential_status reflect the store.
	host.writeEnvelope(t, envelope{Type: msgRequest, ID: "store-1", Request: &requestMsg{Method: methodOAuthStoreCredentials, Tool: "example-provider", Args: credArgs}})
	storeResp := host.readEnvelope(t)
	var storeRes oauthStoreResult
	_ = json.Unmarshal(storeResp.Response.Result, &storeRes)
	if storeRes.Path != "/fake/path" {
		t.Fatalf("store path = %q", storeRes.Path)
	}
	host.writeEnvelope(t, envelope{Type: msgRequest, ID: "status-1", Request: &requestMsg{Method: methodOAuthCredentialStatus, Tool: "example-provider"}})
	statusResp := host.readEnvelope(t)
	var statusRes oauthCredentialStatusResult
	_ = json.Unmarshal(statusResp.Response.Result, &statusRes)
	if !statusRes.Present || statusRes.Source != "fake" {
		t.Fatalf("status = %+v", statusRes)
	}

	host.writeEnvelope(t, envelope{Type: msgShutdown, Shutdown: &shutdownMsg{Reason: "done"}})
	<-done
}

// An unknown oauth provider name fails the request rather than dispatching.
func TestExtension_OAuthUnknownProvider(t *testing.T) {
	host := newMockHost(t)
	defer host.close()

	ext := New("test-ext")
	t.Setenv("PIG_EXT_SOCKET", host.sockPath)

	done := make(chan error, 1)
	go func() { done <- ext.Run() }()

	host.accept(t)
	host.readEnvelope(t) // register
	host.writeEnvelope(t, envelope{Type: msgReady, Ready: &readyMsg{Cwd: "/tmp", Width: 80}})
	time.Sleep(50 * time.Millisecond)

	host.writeEnvelope(t, envelope{Type: msgRequest, ID: "x", Request: &requestMsg{Method: methodOAuthLogin, Tool: "nope"}})
	resp := host.readEnvelope(t)
	if resp.Response == nil || resp.Response.Error == nil {
		t.Fatalf("expected error for unknown provider, got %+v", resp.Response)
	}

	host.writeEnvelope(t, envelope{Type: msgShutdown, Shutdown: &shutdownMsg{Reason: "done"}})
	<-done
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
