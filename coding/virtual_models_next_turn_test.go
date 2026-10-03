package coding

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// A virtual selection is checked in prepareRequest against the routed model, never against the virtual model's declared window in the between-turns check. upstream: agent-session.ts:737-739 (_compactBeforeNextAssistantResponse). Guard for the between-turns branch that the ported suite cases do not reach.
func TestVirtualSelectionIsNotCheckedAgainstItsDeclaredWindowBetweenTurns(t *testing.T) {
	s := newVirtualSuite(t, defaultVirtualRoute, "", nil)
	s.faux.SetResponses([]ai.FauxResponseStep{fauxText("short answer"), fauxEchoCall(), fauxText("done")})
	s.prompt(t, "hello")

	// About 20k tokens exceed the virtual model's declared 1k window but fit the 50k window of the model it routes to.
	s.prompt(t, strings.Repeat("x", 80_000))

	if reasons := s.compactionStartReasons(); len(reasons) != 0 {
		t.Fatalf("compaction started: %v", reasons)
	}
	assertEqual(t, "requests", len(s.reasons()), 3)
}
