package ai

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

type roundTripOAuthProvider struct {
	fakeOAuthProvider
	login OAuthCredentials
}

func (p roundTripOAuthProvider) Login(OAuthLoginCallbacks) (OAuthCredentials, error) {
	return p.login, nil
}
func (roundTripOAuthProvider) RefreshToken(value OAuthCredentials) (OAuthCredentials, error) {
	return value, nil
}

// ResolveProviderAuth persists the refreshed provider record, not only its usable access token.
func TestRefreshedOAuthCatalogFilterReachesStore(t *testing.T) {
	var credential Credential
	if err := json.Unmarshal([]byte(`{"type":"oauth","refresh":"r","access":"a","expires":1,"availableModelIds":["only"],"providerField":{"value":7}}`), &credential); err != nil {
		t.Fatal(err)
	}
	store := NewInMemoryCredentialStore()
	defer store.operations.Wait()
	if _, err := store.Modify(t.Context(), "probe", func(*Credential) (*Credential, error) { return &credential, nil }); err != nil {
		t.Fatal(err)
	}
	auth := ProviderAuth{OAuth: &OAuthAuth{Refresh: func(ctx context.Context, current Credential) (Credential, error) {
		refreshed, err := oauthRefresh(roundTripOAuthProvider{})(ctx, current)
		refreshed.Expires = 4102444800000 // A fixed valid fixture expiry, 2100-01-01.
		return refreshed, err
	}, ToAuth: func(value Credential) (ModelAuth, error) { return ModelAuth{APIKey: value.Access}, nil }}}
	if result, err := ResolveProviderAuth(t.Context(), "probe", auth, store, DefaultProviderAuthContext(), AuthResolutionOverrides{}); err != nil || result == nil || result.Auth.APIKey != "a" {
		t.Fatalf("auth=%+v error=%v", result, err)
	}
	stored, err := store.Read(t.Context(), "probe")
	if err != nil || stored == nil || string(stored.AvailableModelIDs) != `["only"]` || string(stored.Extra["providerField"]) != `{"value":7}` {
		t.Fatalf("persisted credential=%+v error=%v", stored, err)
	}
}

// OAuth results retain provider-owned fields. Copilot's availableModelIds remains present through refresh/login and must still reach the typed account filter (oauth/github-copilot.ts:359-365).
func TestOAuthAdapterPreservesCatalogFilterAndProviderFields(t *testing.T) {
	for _, ids := range []string{`[]`, `["allowed"]`, `null`, `"invalid external value"`} {
		t.Run(ids, func(t *testing.T) {
			wire := []byte(`{"type":"oauth","refresh":"r","access":"a","expires":1,"availableModelIds":` + ids + `,"providerField":{"value":7}}`)
			var credential Credential
			var oauth OAuthCredentials
			if err := json.Unmarshal(wire, &credential); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(wire, &oauth); err != nil {
				t.Fatal(err)
			}
			provider := roundTripOAuthProvider{login: oauth}
			for _, operation := range []string{"refresh", "login"} {
				t.Run(operation, func(t *testing.T) {
					var got Credential
					var err error
					if operation == "refresh" {
						got, err = oauthRefresh(provider)(t.Context(), credential)
					} else {
						got, err = oauthNativeLogin(provider)(t.Context(), AuthInteraction{})
					}
					if err != nil {
						t.Fatal(err)
					}
					if string(got.AvailableModelIDs) != ids {
						t.Errorf("typed filter=%s want=%s", got.AvailableModelIDs, ids)
					}
					data, err := json.Marshal(got)
					if err != nil {
						t.Fatal(err)
					}
					var wantValue, gotValue any
					if err := json.Unmarshal(wire, &wantValue); err != nil {
						t.Fatal(err)
					}
					if err := json.Unmarshal(data, &gotValue); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(gotValue, wantValue) {
						t.Fatalf("credential=%s want=%s", data, wire)
					}
				})
			}
		})
	}
}
