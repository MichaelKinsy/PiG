package coding

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// A caller-supplied Go Provider is the custom request implementation. Its identity metadata does not turn it into a registry-built provider or require unrelated registry credentials.
func TestCustomProviderSummaryKeepsCallerOwnedAuth(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{}, fauxReply("custom provider summary", ai.StopReasonStop, 0))
	model := *h.session.Model()
	model.ProviderMeta.ProviderID = model.Provider.ID()
	h.session.agent.SetModel(&model)
	suiteCompactionSeed(t, h.session)
	result, err := h.session.Compact(t.Context(), "")
	if err != nil || result == nil || !strings.Contains(result.Summary, "custom provider summary") || h.provider.callCount() != 1 {
		t.Fatalf("result=%+v error=%v calls=%d", result, err, h.provider.callCount())
	}
	fmt.Printf("COMPACTION_BOUNDARY custom requests=%d\n", h.provider.callCount())
}
