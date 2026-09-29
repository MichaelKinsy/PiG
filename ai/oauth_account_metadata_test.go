package ai

import (
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"
)

type accountMetadataOAuthProvider struct{ fakeOAuthProvider }

func (accountMetadataOAuthProvider) ID() string { return "account-metadata-test" }
func (accountMetadataOAuthProvider) RefreshToken(credentials OAuthCredentials) (OAuthCredentials, error) {
	if credentials.AccountID != "account-old" || credentials.Scope != "scope-retained" {
		return OAuthCredentials{}, errors.New("input credential metadata lost")
	}
	credentials.AccountID = "account-new"
	return credentials, nil
}

func BenchmarkCodexCredentialMetadata(b *testing.B) {
	credentials := OAuthCredentials{Access: codexTestTokenForAccount("account-benchmark"), Refresh: "refresh", Expires: 1234}
	b.ReportAllocs()
	for b.Loop() {
		result, err := codexCredentialsFromToken(credentials, nil)
		if err != nil || result.AccountID != "account-benchmark" {
			b.Fatalf("credentials=%#v err=%v", result, err)
		}
	}
}

func TestOAuthAccountMetadataPersistsAcrossRefresh(t *testing.T) {
	provider := accountMetadataOAuthProvider{}
	RegisterOAuthProvider(provider.ID(), provider)
	t.Cleanup(func() { UnregisterOAuthProvider(provider.ID()) })
	storage, err := NewAuthStorage(filepath.Join(t.TempDir(), "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	initial := Credential{Type: CredentialOAuth, Access: "access", Refresh: "refresh", Expires: 0, AccountID: "account-old", Scope: "scope-retained"}
	if err = storage.Set(provider.ID(), initial); err != nil {
		t.Fatal(err)
	}
	key, ok, err := ResolveStoredAPIKeyFromStorageContext(t.Context(), storage, provider.ID())
	if err != nil || !ok || key != "access" {
		t.Fatalf("key=%q ok=%v err=%v", key, ok, err)
	}
	persisted, ok, err := storage.Get(provider.ID())
	if err != nil || !ok || persisted.AccountID != "account-new" || persisted.Scope != "scope-retained" {
		t.Fatalf("persisted=%#v ok=%v err=%v", persisted, ok, err)
	}
	refreshed, err := oauthRefresh(provider)(t.Context(), initial)
	if err != nil || refreshed.AccountID != "account-new" || refreshed.Scope != "scope-retained" {
		t.Fatalf("auth refresh=%#v err=%v", refreshed, err)
	}
}

func TestCodexOAuthRejectsTokenWithoutAccountID(t *testing.T) {
	probe := mockCodexOAuthUpstream(t, "unused", "ABCD-1234", "5", []codexOAuthReply{codexApprovedReply()})
	probe.access = "invalid-access-token"
	_, err := codexLoginForTest(t.Context(), func(OAuthDeviceCodeInfo) {})
	if err == nil || err.Error() != "Failed to extract accountId from token" {
		t.Fatalf("error=%v", err)
	}
}

func TestCodexOAuthAcceptsZeroTokenExpiry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		withMockCodexClient(t, func(_ *http.Request) (*http.Response, error) {
			return codexJSONResp(200, fmt.Sprintf(`{"access_token":%q,"refresh_token":"refresh-token","expires_in":0}`, codexTestToken(t, "account-zero"))), nil
		})
		credentials, err := (CodexOAuthProvider{}).RefreshTokenContext(t.Context(), OAuthCredentials{Refresh: "refresh-token"})
		if err != nil || credentials.Expires != time.Now().UnixMilli() || credentials.AccountID != "account-zero" {
			t.Fatalf("credentials=%#v err=%v", credentials, err)
		}
	})
}
