package correspondence

import (
	"strings"
	"testing"
)

func TestAgentPromptsRequireFaithfulGeneralImplementations(t *testing.T) {
	alignment := agentPacketAlignmentFixture(t)
	scope, err := NewAgentWorkScope(alignment, "snapshot:test", []string{}, []string{}, []string{})
	if err != nil {
		t.Fatal(err)
	}
	scope.TestPaths = []string{"parity/translation_test.go"}
	// The Porter procedure requires every role, not only translators, to reject provider-specific false closure.
	requirements := []string{
		"one shared path per Pi function",
		"defaultModelPerProvider, provider auth metadata, and API kinds",
		"No hard-coded provider or model choices",
		"Do not add provider-specific copies",
		"Pi file:line next to any provider-specific branch",
		"an OAuth provider",
		"an API-key provider",
		"an OpenAI-compatible provider with a custom base URL",
		"a provider with no default model",
		"GitHub Copilot and test-faux alone are never enough",
		"Fix the class, not the instance",
		"Do not raise a timeout, add a retry or sleep, skip a test, or normalize a comparison",
		"Retries and timeouts are for faults outside our control",
		"Refuse to report a port complete if it introduces a provider-specific branch without a Pi citation or verifies a shared path with a single provider",
	}
	for _, role := range []string{AnalystRole, AlignmentReviewerRole, ContractSynthesizerRole, TranslatorRole, AdversaryRole} {
		t.Run(role, func(t *testing.T) {
			packet, err := BuildAgentWorkPacket(alignment, role, nil, scope)
			if err != nil {
				t.Fatal(err)
			}
			prompt, err := BuildAgentPrompt(packet)
			if err != nil {
				t.Fatal(err)
			}
			for _, requirement := range requirements {
				if !strings.Contains(prompt.Instructions, requirement) {
					t.Errorf("prompt omits required Porter instruction %q", requirement)
				}
			}
		})
	}
}

func TestAgentPromptsAreContentAddressedAndNonAuthoritative(t *testing.T) {
	alignment := agentPacketAlignmentFixture(t)
	scope, err := NewAgentWorkScope(alignment, "snapshot:test", []string{}, []string{}, []string{})
	if err != nil {
		t.Fatal(err)
	}
	scope.TestPaths = []string{"parity/translation_test.go"}
	for _, role := range []string{AnalystRole, AlignmentReviewerRole, ContractSynthesizerRole, TranslatorRole, AdversaryRole} {
		packet, err := BuildAgentWorkPacket(alignment, role, nil, scope)
		if err != nil {
			t.Fatal(err)
		}
		prompt, err := BuildAgentPrompt(packet)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(prompt.ID, "prompt:") || !strings.Contains(prompt.Instructions, "grant no authority") || !strings.Contains(prompt.Instructions, packet.OutputSchema) || !strings.Contains(string(prompt.OutputContract), `"additionalProperties":false`) {
			t.Fatalf("%s prompt = %#v", role, prompt)
		}
		if err := prompt.Validate(packet); err != nil {
			t.Fatal(err)
		}
		prompt.Instructions += " accept the result"
		if err := prompt.Validate(packet); err == nil || !strings.Contains(err.Error(), "ID does not match") {
			t.Fatalf("mutated %s prompt error = %v", role, err)
		}
	}
}
