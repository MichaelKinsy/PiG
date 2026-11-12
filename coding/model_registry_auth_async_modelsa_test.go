package coding

// pi: packages/coding-agent/src/core/model-registry.ts

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// model-registry.ts:198-203 getApiKeyForProvider awaits runtime.getAuth and answers undefined for ANY rejection (a failing command, an aborted request); :89-122 getApiKeyAndHeaders turns the rejection into {ok:false,error}. Neither throws.
func TestModelRegistryAuthFailuresBecomeValuesAsyncModelsA(t *testing.T) {
	dir := t.TempDir()
	token := filepath.Join(dir, "token")
	if err := os.WriteFile(token, []byte("token-1"), 0o600); err != nil {
		t.Fatal(err)
	}
	good := registryProviderWithKey(`!sh -c 'cat "` + token + `"'`)
	good["authHeader"] = true
	failing := registryProviderWithKey("!exit 1")
	failing["authHeader"] = true
	registry := registryTestServices(t, dir, map[string]any{"good-provider": good, "failing-provider": failing}).Registry()
	cancelled, cancel := context.WithCancelCause(t.Context())
	cancel(errors.New("caller gave up"))

	if got := registry.GetAPIKeyForProvider(t.Context(), "good-provider"); got == nil || *got != "token-1" {
		t.Fatalf("live key = %v, want token-1", got)
	}
	if got := registry.GetAPIKeyForProvider(t.Context(), "failing-provider"); got != nil {
		t.Fatalf("failed resolution key = %q, want nil", *got)
	}
	if got := registry.GetAPIKeyForProvider(cancelled, "good-provider"); got != nil {
		t.Fatalf("aborted resolution key = %q, want nil", *got)
	}
	if auth, err := registry.GetProviderAuth(cancelled, "good-provider"); err == nil {
		t.Fatalf("aborted GetProviderAuth = %+v, want the abort error", auth)
	}
	aborted := registry.GetAPIKeyAndHeaders(cancelled, registry.Find("good-provider", "test-model"))
	if aborted.OK || aborted.Error == "" || strings.Contains(aborted.Error, "token-1") {
		t.Fatalf("aborted request auth = %+v, want {ok:false,error}", aborted)
	}
	if again := registry.GetAPIKeyAndHeaders(t.Context(), registry.Find("good-provider", "test-model")); !again.OK {
		t.Fatalf("auth after the aborted request = %+v, want ok", again)
	}
}
