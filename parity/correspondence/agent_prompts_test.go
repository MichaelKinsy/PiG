package correspondence

import (
	"strings"
	"testing"
)

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
