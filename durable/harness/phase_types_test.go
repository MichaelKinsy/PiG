package harness

import (
	"encoding/json"
	"testing"
)

// generation.ts GenerationCheckpoint and tool.ts ToolTaskCheckpoint select their shape by a closed `phase` literal union; the persisted JSON carries exactly that literal.
func TestCheckpointPhasesAreTheUpstreamLiterals(t *testing.T) {
	for phase, literal := range map[GenerationPhase]string{
		generationPrepare: "prepare", generationRequest: "request", generationRetry: "retry", generationPoll: "poll", generationTools: "tools",
	} {
		if string(phase) != literal {
			t.Errorf("generation phase %q, want %q", phase, literal)
		}
	}
	for phase, literal := range map[ToolTaskPhase]string{toolPhaseCall: "call", toolPhaseExecute: "execute"} {
		raw, err := json.Marshal(ToolTaskCheckpoint{Phase: phase})
		if err != nil {
			t.Fatal(err)
		}
		var back struct {
			Phase string `json:"phase"`
		}
		if err := json.Unmarshal(raw, &back); err != nil || back.Phase != literal {
			t.Errorf("tool phase %q persisted as %s", literal, raw)
		}
	}
}
