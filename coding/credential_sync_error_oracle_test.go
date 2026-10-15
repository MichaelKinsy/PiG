package coding

import (
	"bytes"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"os/exec"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// CredentialSynchronizationError (core/model-runtime.ts) against pinned Pi: the message, name, members and cause for every operation, provider ids with format verbs, unicode and the empty string, api-key and OAuth credentials, and causes with unicode and empty messages. Pig's Error() is Pi's `${error}` and errors.As finds the typed error through a wrapping error.
func TestCredentialSynchronizationErrorMatchesPi(t *testing.T) {
	type probe struct {
		Provider   string         `json:"provider"`
		Operation  string         `json:"operation"`
		Credential map[string]any `json:"credential"`
		Cause      string         `json:"cause"`
	}
	rng := rand.New(rand.NewPCG(3, 17))
	pick := func(items []string) string { return items[rng.IntN(len(items))] }
	operations := []string{"login", "logout", "setRuntimeApiKey", "removeRuntimeApiKey"}
	providers := []string{"anthropic", "openai", "%s", "%d %v", "é😀", "", "a b", "x\ny", "custom-1"}
	causes := []string{"disk full", "", "ünï", "multi\nline", "%s"}
	credentials := []map[string]any{nil, {"type": "api_key", "key": "sk-1"}, {"type": "oauth", "refresh": "r", "access": "a", "expires": float64(1760000000000)}, {"type": "api_key", "key": "k", "env": map[string]any{"A": "b"}}}
	var probes []probe
	for range 200 {
		probes = append(probes, probe{pick(providers), pick(operations), credentials[rng.IntN(len(credentials))], pick(causes)})
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/credential_sync_error.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []struct {
		Message    string  `json:"message"`
		Name       string  `json:"name"`
		IsError    bool    `json:"isError"`
		ProviderID string  `json:"providerId"`
		Operation  string  `json:"operation"`
		Credential *string `json:"credential"`
		Cause      string  `json:"cause"`
		String     string  `json:"string"`
	}
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	for i, p := range probes {
		var credential *ai.Credential
		if p.Credential != nil {
			raw, _ := json.Marshal(p.Credential)
			credential = new(ai.Credential)
			if err := json.Unmarshal(raw, credential); err != nil {
				t.Fatal(err)
			}
		}
		cause := errors.New(p.Cause)
		var err error = NewCredentialSynchronizationError(p.Provider, CredentialSynchronizationOperation(p.Operation), credential, cause)
		var typed *CredentialSynchronizationError
		if !errors.As(err, &typed) {
			t.Fatalf("probe %d: errors.As fails", i)
		}
		w := want[i]
		var gotCredential, wantCredential any
		if typed.Credential != nil {
			raw, err := json.Marshal(typed.Credential)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(raw, &gotCredential); err != nil {
				t.Fatal(err)
			}
		}
		if w.Credential != nil {
			if err := json.Unmarshal([]byte(*w.Credential), &wantCredential); err != nil {
				t.Fatal(err)
			}
		}
		if err.Error() != w.Message || err.Error() != w.String[len(w.Name)+2:] || typed.Name() != w.Name || !w.IsError ||
			typed.ProviderID != w.ProviderID || string(typed.Operation) != w.Operation || !reflect.DeepEqual(gotCredential, wantCredential) ||
			typed.Unwrap() == nil || typed.Unwrap().Error() != w.Cause {
			t.Errorf("probe %+v:\n Go %q %q %q %q %#v cause %v\n Pi %+v", p, err.Error(), typed.Name(), typed.ProviderID, typed.Operation, gotCredential, typed.Unwrap(), w)
		}
	}
}
