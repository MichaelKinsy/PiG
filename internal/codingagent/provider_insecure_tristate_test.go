package codingagent

// pi: packages/coding-agent/src/core/model-runtime.ts

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// insecureRegistration decodes a registration the way the host decodes an SDK's raw config: an extension that never states insecure, one that
// sets `"insecure": false` and one that sets `"insecure": true` differ in the JSON the host receives and nowhere else. fragment is the member
// (empty for omitted); refresh adds the refreshModels callback that composes the provider in the native collection.
func insecureRegistration(t *testing.T, fragment string, refresh bool) extension.ProviderConfig {
	t.Helper()
	var config extension.ProviderConfig
	raw := `{"baseUrl":"https://corp.test/v1","apiKey":"corp-key","api":"openai-completions","models":[{"id":"corp-m1","name":"Corp M1"}]` + fragment + `}`
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		t.Fatal(err)
	}
	if refresh {
		config.RefreshModels = func(extension.RefreshModelsContext) ([]extension.ProviderModelConfig, error) { return nil, nil }
	}
	return config
}

const (
	insecureOmitted = ""
	insecureOff     = `,"insecure":false`
	insecureOn      = `,"insecure":true`
)

// TestRegisteredProviderInsecureHasThreeStates (D36, owner decision 2026-11-17): Pi's registerProvider merges a re-registration's defined values over
// the previous registration and keeps the undefined ones (model-runtime.ts:970-977, `if (value !== undefined)`). An explicit `insecure: false` is a
// defined value and turns the opt-in off; an omitted one keeps the earlier value; the first registration with false is off, as omitted is. Both
// registration paths merge: the dynamic registry (upsertRegisteredProviderLocked) and the native composition a refreshModels registration takes
// (mergeProviderConfigInput).
func TestRegisteredProviderInsecureHasThreeStates(t *testing.T) {
	for _, path := range []struct {
		name    string
		refresh bool
	}{{"dynamic registry", false}, {"native composition", true}} {
		for _, tc := range []struct {
			name  string
			steps []string
			want  bool
		}{
			{"first registration omitted is off", []string{insecureOmitted}, false},
			{"first registration false is off", []string{insecureOff}, false},
			{"first registration true is on", []string{insecureOn}, true},
			{"true then false turns the opt-in off", []string{insecureOn, insecureOff}, false},
			{"true then omitted keeps the opt-in", []string{insecureOn, insecureOmitted}, true},
			{"true then true stays on", []string{insecureOn, insecureOn}, true},
			{"false then true turns it on", []string{insecureOff, insecureOn}, true},
			{"false then omitted stays off", []string{insecureOff, insecureOmitted}, false},
			{"omitted then false is off", []string{insecureOmitted, insecureOff}, false},
			{"true, false, omitted: the off value is kept", []string{insecureOn, insecureOff, insecureOmitted}, false},
			{"true, false, true: on again", []string{insecureOn, insecureOff, insecureOn}, true},
			{"true, omitted, false: off", []string{insecureOn, insecureOmitted, insecureOff}, false},
		} {
			t.Run(path.name+"/"+tc.name, func(t *testing.T) {
				r := NewModelRegistry(t.TempDir())
				for _, fragment := range tc.steps {
					if err := r.RegisterExtensionProvider("corp", insecureRegistration(t, fragment, path.refresh)); err != nil {
						t.Fatal(err)
					}
				}
				if got := r.ProviderInsecure("corp"); got != tc.want {
					t.Fatalf("ProviderInsecure = %v after registrations %q, want %v", got, tc.steps, tc.want)
				}
				if !path.refresh {
					entry, ok := r.Resolve("corp", "corp-m1")
					if !ok || entry.Insecure != tc.want {
						t.Fatalf("Resolve(corp/corp-m1) = %+v (found %v) after registrations %q, want Insecure %v", entry, ok, tc.steps, tc.want)
					}
				}
			})
		}
	}
}

// TestRegisteredProviderInsecureFalseIsReadBack: getRegisteredProviderConfig returns the effective configuration (model-runtime.ts getRegisteredProviderConfig),
// so a defined `insecure: false` is a member of it, and an omitted one is not.
func TestRegisteredProviderInsecureFalseIsReadBack(t *testing.T) {
	for _, tc := range []struct {
		name  string
		steps []string
		want  string
	}{
		{"never stated", []string{insecureOmitted}, "absent"},
		{"true", []string{insecureOn}, "true"},
		{"true then false", []string{insecureOn, insecureOff}, "false"},
		{"false then omitted", []string{insecureOff, insecureOmitted}, "false"},
		{"true then false then omitted", []string{insecureOn, insecureOff, insecureOmitted}, "false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := NewModelRegistry(t.TempDir())
			for _, fragment := range tc.steps {
				if err := r.RegisterExtensionProvider("corp", insecureRegistration(t, fragment, true)); err != nil {
					t.Fatal(err)
				}
			}
			input := r.GetRegisteredProviderConfig("corp")
			if input == nil {
				t.Fatal("no registered configuration")
			}
			graph, err := GraphProviderConfig(input)
			if err != nil {
				t.Fatal(err)
			}
			got := "absent"
			if value, has := graph["insecure"]; has {
				got = fmt.Sprint(value)
			}
			if got != tc.want {
				t.Fatalf("getRegisteredProviderConfig insecure = %s after registrations %q, want %s", got, tc.steps, tc.want)
			}
		})
	}
}

// TestUnregisterProviderClearsInsecure: unregisterProvider removes the registration (model-runtime.ts:988-994), so a later registration starts from nothing:
// an omitted or false insecure is off whatever the removed registration held.
func TestUnregisterProviderClearsInsecure(t *testing.T) {
	for _, refresh := range []bool{false, true} {
		for _, fragment := range []string{insecureOmitted, insecureOff} {
			t.Run(fmt.Sprintf("refresh=%v/%q", refresh, strings.TrimPrefix(fragment, ",")), func(t *testing.T) {
				r := NewModelRegistry(t.TempDir())
				if err := r.RegisterExtensionProvider("corp", insecureRegistration(t, insecureOn, refresh)); err != nil {
					t.Fatal(err)
				}
				if !r.ProviderInsecure("corp") {
					t.Fatal("the opt-in did not take")
				}
				r.UnregisterProvider("corp")
				if err := r.RegisterExtensionProvider("corp", insecureRegistration(t, fragment, refresh)); err != nil {
					t.Fatal(err)
				}
				if r.ProviderInsecure("corp") {
					t.Fatal("a registration after unregisterProvider inherited the removed registration's opt-in")
				}
			})
		}
	}
}
