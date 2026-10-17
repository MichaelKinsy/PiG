package extension

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// upstream: model-registry.ts:97-104 returns headers: resolution.auth.headers and env: resolution.env as resolved, so an empty object
// stays {} and only an absent one is dropped.
func TestResolvedRequestAuthKeepsEmptyHeadersAndEnv(t *testing.T) {
	for _, tc := range []struct {
		auth ResolvedRequestAuth
		want string
	}{
		{ResolvedRequestAuth{OK: true, Headers: ai.ProviderHeaders{}, Env: map[string]string{}}, `{"ok":true,"headers":{},"env":{}}`},
		{ResolvedRequestAuth{OK: true}, `{"ok":true}`},
	} {
		got, err := json.Marshal(tc.auth)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != tc.want {
			t.Errorf("auth = %s, want %s", got, tc.want)
		}
	}
}
