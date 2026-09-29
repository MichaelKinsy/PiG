package subprocess

import (
	"encoding/json"
	"strings"
	"testing"
)

// .upstream/v0.87.1/packages/coding-agent/test/extensions-runner.test.ts:806
// loader.ts:311 rejects a mismatched default before it changes any registration.
func TestUpstreamRunnerRejectsDefaultValuesThatDoNotMatchTheFlagType(t *testing.T) {
	reg := &RegisterPayload{Name: "bad-flag-default", Flags: []FlagDecl{{Name: "safe-mode", Type: "boolean", Default: json.RawMessage(`"false"`)}}}
	err := validateRegisterPayload(reg.Name, reg)
	if err == nil || !strings.Contains(err.Error(), `Invalid default for flag "safe-mode": expected boolean, got string`) {
		t.Fatalf("invalid flag default: %v", err)
	}
	if _, exists := reg.flagDefaults["safe-mode"]; exists {
		t.Fatal("invalid flag default was retained")
	}
}

func TestFlagDefaultValidationPreservesTypesAndFirstValue(t *testing.T) {
	for _, tc := range []struct{ kind, raw, wantError string }{
		{"boolean", "true", ""}, {"boolean", "false", ""}, {"string", `""`, ""}, {"string", `"auto"`, ""},
		{"boolean", "null", "object"}, {"boolean", "1", "number"}, {"string", "false", "boolean"},
		{"boolean", "{}", "object"}, {"boolean", "[]", "object"},
	} {
		t.Run(tc.kind+"/"+tc.raw, func(t *testing.T) {
			reg := &RegisterPayload{Name: "typed", Flags: []FlagDecl{{Name: "mode", Type: tc.kind, Default: json.RawMessage(tc.raw)}}}
			err := validateRegisterPayload(reg.Name, reg)
			if tc.wantError == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "got "+tc.wantError) {
				t.Fatalf("error = %v, want got %s", err, tc.wantError)
			}
		})
	}
	reg := &RegisterPayload{Name: "repeated", Flags: []FlagDecl{
		{Name: "shared", Type: "boolean", Description: "first", Default: json.RawMessage(`true`)},
		{Name: "shared", Type: "boolean", Description: "last", Default: json.RawMessage(`false`)},
	}}
	if err := validateRegisterPayload(reg.Name, reg); err != nil {
		t.Fatal(err)
	}
	if len(reg.Flags) != 1 || reg.Flags[0].Description != "last" || reg.flagDefaults["shared"] != true {
		t.Fatalf("last definition, first runtime default: %#v / %#v", reg.Flags, reg.flagDefaults)
	}
}
