package ai

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestOAuthCredentialRetainsEnterpriseURL(t *testing.T) {
	// Provider-owned OAuth fields survive the shared auth bridge just like arbitrary extension fields.
	var oauth OAuthCredentials
	if err := json.Unmarshal([]byte(`{"refresh":"r","access":"a","expires":1,"enterpriseUrl":"https://company.ghe.com"}`), &oauth); err != nil {
		t.Fatal(err)
	}
	credential, err := credentialFromOAuth(oauth)
	if err != nil || credential.EnterpriseDomain != "https://company.ghe.com" {
		t.Fatalf("credential=%#v error=%v", credential, err)
	}
}

type enterpriseRefreshProvider struct{ fakeOAuthProvider }

func (enterpriseRefreshProvider) RefreshToken(value OAuthCredentials) (OAuthCredentials, error) {
	value.Expires = 4102444800000
	return value, nil
}

func TestStoredOAuthValidCredentialDoesNotRewrite(t *testing.T) {
	RegisterOAuthProvider("fake-oauth", fakeOAuthProvider{})
	defer UnregisterOAuthProvider("fake-oauth")
	path := filepath.Join(t.TempDir(), "auth.json")
	original := `{"fake-oauth":{"type":"oauth","refresh":"r","access":"a","expires":4102444800000,"providerField":{ "value": 7 }}}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	storage, err := NewAuthStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveOAuthAPIKeyFromStorageContext(t.Context(), storage, "fake-oauth"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != original {
		t.Fatalf("valid credential rewritten: %s, error=%v", got, err)
	}
}

func TestStoredOAuthRefreshRetainsProviderFields(t *testing.T) {
	// The storage caller must persist the complete refreshed record, not a reconstruction of the common token fields.
	provider := enterpriseRefreshProvider{}
	RegisterOAuthProvider(provider.ID(), provider)
	defer UnregisterOAuthProvider(provider.ID())
	storage, err := NewAuthStorage(filepath.Join(t.TempDir(), "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	credential := Credential{Type: CredentialOAuth, Refresh: "r", Access: "a", Expires: 1, EnterpriseDomain: "company.ghe.com", AvailableModelIDs: json.RawMessage(`["allowed"]`), GatewayConfig: json.RawMessage(`{"models":[]}`), Extra: map[string]json.RawMessage{"providerField": json.RawMessage(`{"value":7}`)}}
	if err := storage.Set(provider.ID(), credential); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveOAuthAPIKeyFromStorageContext(t.Context(), storage, provider.ID())
	if err != nil || got != "a" {
		t.Fatalf("key=%q error=%v", got, err)
	}
	stored, ok, err := storage.GetRaw(provider.ID())
	if err != nil || !ok || stored.EnterpriseDomain != credential.EnterpriseDomain {
		t.Fatalf("stored=%#v found=%t error=%v", stored, ok, err)
	}
	assertCatalogJSON(t, stored.AvailableModelIDs, `["allowed"]`)
	assertCatalogJSON(t, stored.GatewayConfig, `{"models":[]}`)
	assertCatalogJSON(t, stored.Extra["providerField"], `{"value":7}`)
}
