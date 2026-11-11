package ai

// pi: packages/ai/src/compat/extension-oauth-types.ts

import (
	"encoding/json"
	"testing"
)

// extension-oauth-types.ts re-exports OAuthCredentials, whose stored JSON is auth.json's wire contract. Each expectation is the member order of the credential literal the named Pi flow builds (packages/ai/src/auth/oauth/*.ts); Pi writes them with JSON.stringify, which keeps literal order. This test asserts the order and spelling of every provider shape, which the type-only row has no other runtime to check.
func TestOAuthCredentialMembersFollowPiFlowLiterals(t *testing.T) {
	tests := []struct {
		name  string
		creds OAuthCredentials
		want  string
	}{
		{"anthropic.ts:132-137 and :270-275", OAuthCredentials{Refresh: "r", Access: "a", Expires: 7, order: credentialKeyOrder{keys: refreshFirstCredentialMembers}},
			`{"type":"oauth","refresh":"r","access":"a","expires":7}`},
		{"meta.ts:162", OAuthCredentials{Refresh: "r", Access: "a", Expires: 7, order: credentialKeyOrder{keys: refreshFirstCredentialMembers}},
			`{"type":"oauth","refresh":"r","access":"a","expires":7}`},
		{"kimi-coding.ts:260 and xai.ts:137-142", OAuthCredentials{Refresh: "r", Access: "a", Expires: 7, order: credentialKeyOrder{keys: accessFirstCredentialMembers}},
			`{"type":"oauth","access":"a","refresh":"r","expires":7}`},
		{"openai-codex.ts:323-329", OAuthCredentials{Refresh: "r", Access: "a", Expires: 7, AccountID: "acct", order: credentialKeyOrder{keys: codexCredentialMembers}},
			`{"type":"oauth","access":"a","refresh":"r","expires":7,"accountId":"acct"}`},
		{"openai-chatgpt.ts:173-180", OAuthCredentials{Refresh: "r", Access: "a", Expires: 7, Extra: map[string]json.RawMessage{"clientId": json.RawMessage(`"c"`), "scopes": json.RawMessage(`["s"]`)}, order: credentialKeyOrder{keys: chatgptCredentialMembers}},
			`{"type":"oauth","access":"a","refresh":"r","expires":7,"clientId":"c","scopes":["s"]}`},
		{"radius.ts:127-133", OAuthCredentials{Refresh: "r", Access: "a", Expires: 7, Scope: "sc", order: credentialKeyOrder{keys: radiusCredentialMembers}},
			`{"type":"oauth","access":"a","refresh":"r","expires":7,"scope":"sc"}`},
		{"github-copilot.ts:335-341", OAuthCredentials{Refresh: "r", Access: "a", Expires: 7, Extra: map[string]json.RawMessage{"enterpriseUrl": json.RawMessage(`"e.example"`)}, order: credentialKeyOrder{keys: copilotCredentialMembers}},
			`{"type":"oauth","refresh":"r","access":"a","expires":7,"enterpriseUrl":"e.example"}`},
	}
	for _, tc := range tests {
		credential, err := credentialFromOAuth(tc.creds)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		got, err := json.Marshal(credential)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if string(got) != tc.want {
			t.Errorf("%s: credential JSON = %s, want %s", tc.name, got, tc.want)
		}
	}
}
