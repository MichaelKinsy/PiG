package subprocess

import (
	"testing"
)

// upstream: models.ts:164 Provider.auth. The carrier an extension's provider object registers with exposes the auth methods the extension declared,
// with the API-key and OAuth operations wired to the provider object's own auth callbacks, and none for a method it did not declare.
func TestNativeProviderCarrierExposesTheDeclaredAuth(t *testing.T) {
	yes := true
	cases := []struct {
		name          string
		auth          *ProviderObjectAuthDeclaration
		apiKey, oauth bool
	}{
		{"api key", &ProviderObjectAuthDeclaration{APIKey: &ProviderObjectAuthMethodDeclaration{Name: "Key"}}, true, false},
		{"oauth", &ProviderObjectAuthDeclaration{OAuth: &ProviderObjectAuthMethodDeclaration{Name: "Sub", IsSubscription: &yes}}, false, true},
		{"both", &ProviderObjectAuthDeclaration{APIKey: &ProviderObjectAuthMethodDeclaration{Name: "Key"}, OAuth: &ProviderObjectAuthMethodDeclaration{Name: "Sub"}}, true, true},
		{"undeclared", nil, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			proxy := &nativeProviderProxy{declaration: NativeProviderDeclaration{ID: "p", Name: "P", Auth: tc.auth}}
			auth := proxy.carrier().Auth
			if (auth.APIKey != nil) != tc.apiKey || (auth.OAuth != nil) != tc.oauth {
				t.Fatalf("carrier auth = %+v, want apiKey=%v oauth=%v", auth, tc.apiKey, tc.oauth)
			}
			if auth.APIKey != nil && (auth.APIKey.Name != "Key" || auth.APIKey.Resolve == nil) {
				t.Fatalf("apiKey auth = %+v", auth.APIKey)
			}
			if auth.OAuth != nil && (auth.OAuth.Refresh == nil || auth.OAuth.ToAuth == nil || auth.OAuth.IsSubscription != (tc.auth.OAuth.IsSubscription != nil)) {
				t.Fatalf("oauth auth = %+v", auth.OAuth)
			}
		})
	}
}
