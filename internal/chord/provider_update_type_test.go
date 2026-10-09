package chord

import "testing"

// wire.ts WireServiceProviderUpdate / provider ServiceProviderUpdate: "type" is the closed union state | reset | unavailable | replaced | spawned | closed.
func TestProviderUpdateTypeConstantsAreTheUpstreamLiterals(t *testing.T) {
	for constant, literal := range map[ServiceProviderUpdateType]string{
		UpdateState: "state", UpdateReset: "reset", UpdateUnavailable: "unavailable", UpdateReplaced: "replaced", UpdateSpawned: "spawned", UpdateClosed: "closed",
	} {
		if string(constant) != literal {
			t.Errorf("constant %q, want %q", constant, literal)
		}
	}
	update, err := UnmarshalWireServiceProviderUpdate([]byte(`{"type":"unavailable"}`))
	if err != nil || update.Type() != UpdateUnavailable {
		t.Fatalf("decoded %+v (%v), want the unavailable update", update, err)
	}
}
