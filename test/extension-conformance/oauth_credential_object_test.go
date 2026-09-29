package extensionconformance

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// captureOAuthCredentialObject drives the fixture's conformance-oauth-object provider. Pi passes the complete stored credential, with `type: "oauth"`, to refreshToken and getApiKey, and stores `{ ...result, type: "oauth" }` (provider-composer.ts:276-293, auth/resolve.ts:145-155). A fractional expiry and every provider-owned key survive each step.
func captureOAuthCredentialObject(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	provider, ok := ai.GetOAuthProvider("conformance-oauth-object")
	if !ok {
		t.Fatal("conformance-oauth-object provider not registered by fixture")
	}
	type contextual interface {
		LoginContext(context.Context, ai.OAuthLoginCallbacks) (ai.OAuthCredentials, error)
		RefreshTokenContext(context.Context, ai.OAuthCredentials) (ai.OAuthCredentials, error)
		GetAPIKeyContext(context.Context, ai.OAuthCredentials) (string, error)
	}
	bridged, ok := provider.(contextual)
	if !ok {
		t.Fatalf("provider %T lacks contextual OAuth methods", provider)
	}
	login, err := bridged.LoginContext(ctx, ai.OAuthLoginCallbacks{})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	stored, err := ai.CredentialFromOAuth(login)
	if err != nil {
		t.Fatal(err)
	}
	assertCredentialJSON(t, "login", stored, `{"type":"oauth","access":"object-access","refresh":"object-refresh","expires":1700000000000.25,"meta":{"k":[1,null,""]},"projectId":""}`)
	if got := stored.ExpiresMillis(); got != 1700000000000.25 {
		t.Fatalf("stored ExpiresMillis = %v", got)
	}

	key, err := bridged.GetAPIKeyContext(ctx, stored.OAuthCredentials())
	if err != nil || key != "key:typed:meta" {
		t.Fatalf("getApiKey = %q, %v; want the complete stored credential", key, err)
	}

	refreshed, err := bridged.RefreshTokenContext(ctx, stored.OAuthCredentials())
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	next, err := ai.CredentialFromOAuth(refreshed)
	if err != nil {
		t.Fatal(err)
	}
	assertCredentialJSON(t, "refresh", next, `{"type":"oauth","access":"refreshed-object-refresh","refresh":"object-refresh","expires":1700000000000.75,"meta":{"k":[1,null,""]},"projectId":""}`)
}

func assertCredentialJSON(t *testing.T, step string, credential ai.Credential, want string) {
	t.Helper()
	encoded, err := json.Marshal(credential)
	if err != nil {
		t.Fatal(err)
	}
	decode := func(text string) any {
		decoder := json.NewDecoder(strings.NewReader(text))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			t.Fatalf("%s: decode %s: %v", step, text, err)
		}
		return value
	}
	if got := decode(string(encoded)); !reflect.DeepEqual(got, decode(want)) {
		t.Fatalf("%s credential = %s, want %s", step, encoded, want)
	}
}

// captureOAuthLargeExpiry drives the fixture's conformance-oauth-large provider. Its expires is the double 2**60: the wire and auth.json carry JSON.stringify's digits 1152921504606847000, and the extension reads the same double back (types.ts:24-27 declares expires a number). getApiKey reports the distance of the number and of its integer projection from 2**60, which is 0 for a JavaScript number and 24 for an SDK that keeps the integer 1152921504606847000.
func captureOAuthLargeExpiry(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	provider, ok := ai.GetOAuthProvider("conformance-oauth-large")
	if !ok {
		t.Fatal("conformance-oauth-large provider not registered by fixture")
	}
	bridged, ok := provider.(interface {
		LoginContext(context.Context, ai.OAuthLoginCallbacks) (ai.OAuthCredentials, error)
		GetAPIKeyContext(context.Context, ai.OAuthCredentials) (string, error)
	})
	if !ok {
		t.Fatalf("provider %T lacks contextual OAuth methods", provider)
	}
	login, err := bridged.LoginContext(ctx, ai.OAuthLoginCallbacks{})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	stored, err := ai.CredentialFromOAuth(login)
	if err != nil {
		t.Fatal(err)
	}
	assertCredentialJSON(t, "login", stored, `{"type":"oauth","access":"large-access","refresh":"large-refresh","expires":1152921504606847000}`)
	if key, err := bridged.GetAPIKeyContext(ctx, stored.OAuthCredentials()); err != nil || key != "key:0:0" {
		t.Fatalf("getApiKey = %q, %v; want key:0:0", key, err)
	}
}
