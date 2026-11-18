package extension

// pi: packages/coding-agent/src/core/model-runtime.ts

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestProviderConfigInsecureKeepsThreeStatesOnTheWire (D36, owner decision 2026-11-17): the registration's `insecure` member is absent, false or true on the
// wire, and the host's ProviderConfig keeps the three apart: a decoded false is a defined value (a re-registration with it turns an earlier opt-in off),
// where Go's zero Insecure alone could not tell it from an omitted member. The exported Insecure bool keeps its meaning: true is on, false is not on.
func TestProviderConfigInsecureKeepsThreeStatesOnTheWire(t *testing.T) {
	for _, tc := range []struct {
		name    string
		raw     string
		setting string // "absent", "false" or "true"
		on      bool
	}{
		{"absent", `{"baseUrl":"https://a.test"}`, "absent", false},
		{"null", `{"baseUrl":"https://a.test","insecure":null}`, "absent", false},
		{"false", `{"baseUrl":"https://a.test","insecure":false}`, "false", false},
		{"true", `{"baseUrl":"https://a.test","insecure":true}`, "true", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var config ProviderConfig
			if err := json.Unmarshal([]byte(tc.raw), &config); err != nil {
				t.Fatal(err)
			}
			got := "absent"
			if setting := config.InsecureSetting(); setting != nil {
				got = map[bool]string{true: "true", false: "false"}[*setting]
			}
			if got != tc.setting || config.Insecure != tc.on {
				t.Fatalf("decoded insecure = %s (Insecure %v), want %s (Insecure %v)", got, config.Insecure, tc.setting, tc.on)
			}
			encoded, err := json.Marshal(config)
			if err != nil {
				t.Fatal(err)
			}
			var members map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &members); err != nil {
				t.Fatal(err)
			}
			member, present := members["insecure"]
			if present != (tc.setting != "absent") || present && strings.TrimSpace(string(member)) != tc.setting {
				t.Fatalf("encoded %s: insecure member = %q (present %v), want %s", encoded, member, present, tc.setting)
			}
		})
	}
}

// TestProviderConfigInputInsecureSetting: the native registration form says the three states through SetInsecureSetting, and the exported Insecure bool
// stays the on switch it was.
func TestProviderConfigInputInsecureSetting(t *testing.T) {
	var input ProviderConfigInput
	if input.InsecureSetting() != nil {
		t.Fatal("a zero registration states insecure")
	}
	input.SetInsecureSetting(new(true))
	if setting := input.InsecureSetting(); setting == nil || !*setting || !input.Insecure {
		t.Fatalf("after true: setting %v, Insecure %v", setting, input.Insecure)
	}
	input.SetInsecureSetting(new(false))
	if setting := input.InsecureSetting(); setting == nil || *setting || input.Insecure {
		t.Fatalf("after false: setting %v, Insecure %v", setting, input.Insecure)
	}
	input.SetInsecureSetting(nil)
	if input.InsecureSetting() != nil || input.Insecure {
		t.Fatalf("after nil: setting %v, Insecure %v", input.InsecureSetting(), input.Insecure)
	}
}
