package harness

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func TestValidateToolNamesRejectsDuplicates(t *testing.T) {
	tool := func(name string) AgentHarnessTool {
		return AgentHarnessTool{ToolSchema: ai.ToolSchema{Name: name}}
	}
	if err := ValidateToolNames([]AgentHarnessTool{tool("read"), tool("write")}); err != nil {
		t.Fatalf("unique names rejected: %v", err)
	}
	err := ValidateToolNames([]AgentHarnessTool{tool("read"), tool("read")})
	if err == nil || err.Error() != `Duplicate tool name: "read"` {
		t.Fatalf("duplicate err = %v", err)
	}
}

func TestValidateRetryPolicyBounds(t *testing.T) {
	if err := ValidateRetryPolicy(DefaultRetryPolicy); err != nil {
		t.Fatalf("default policy rejected: %v", err)
	}
	invalid := []ai.RetryPolicy{
		{MaxRetries: -1},
		{MaxRetries: maxSafeInteger},
		{MaxRetries: maxSafeInteger + 1},
		{BaseDelayMs: -1},
		{MaxAgentDelayMs: new(-1)},
		{MaxAgentDelayMs: new(maxSafeInteger + 1)},
	}
	for _, policy := range invalid {
		err := ValidateRetryPolicy(policy)
		if err == nil || !strings.Contains(err.Error(), "finite non-negative safe integers") {
			t.Fatalf("policy %#v err = %v", policy, err)
		}
	}
	if err := ValidateRetryPolicy(ai.RetryPolicy{MaxRetries: maxSafeInteger - 1, MaxAgentDelayMs: new(0)}); err != nil {
		t.Fatalf("largest safe retry count rejected: %v", err)
	}
}

func TestValidateCompactionSettingsBounds(t *testing.T) {
	if err := ValidateCompactionSettings(CompactionSettings{Enabled: true, ReserveTokens: 16384, KeepRecentTokens: 20000}); err != nil {
		t.Fatalf("valid settings rejected: %v", err)
	}
	for _, settings := range []CompactionSettings{{ReserveTokens: -1}, {KeepRecentTokens: -1}, {ReserveTokens: maxSafeInteger + 1}} {
		if err := ValidateCompactionSettings(settings); err == nil {
			t.Fatalf("settings %#v accepted", settings)
		}
	}
}

func TestDefaultRetryPolicyMatchesUpstream(t *testing.T) {
	if !DefaultRetryPolicy.Enabled || DefaultRetryPolicy.MaxRetries != 3 || DefaultRetryPolicy.BaseDelayMs != 1000 ||
		DefaultRetryPolicy.MaxAgentDelayMs == nil || *DefaultRetryPolicy.MaxAgentDelayMs != ai.DefaultMaxAgentRetryDelayMs {
		t.Fatalf("default policy = %#v", DefaultRetryPolicy)
	}
}
