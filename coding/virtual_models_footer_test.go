package coding

import (
	"testing"

	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// The interactive footer reads the routed model through the session handle (footer.ts:241-244 reads session.routedModel); *Session must keep satisfying that contract.
func TestVirtualSessionHandleSuppliesTheFooterRoutedModel(t *testing.T) {
	f := createVirtualRuntime(t)
	session, _ := resumeVirtual(t, f, nil)
	handle, ok := any(session).(interface {
		RoutedModelSelection() *icodingagent.RoutedModelSelection
	})
	if !ok {
		t.Fatal("*Session does not supply RoutedModelSelection to the interactive footer")
	}
	routed := handle.RoutedModelSelection()
	if routed == nil || routed.Model == nil || routed.Model.ProviderMeta.ProviderID != "faux" || routed.Model.ID != "large" {
		t.Fatalf("footer routed model = %+v", routed)
	}
}
