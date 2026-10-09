package chord

import (
	"encoding/json"
	"testing"
)

// Pi types a wire member's kind as the closed union "method" | "state" (chord/src/services/wire.ts WireServiceMemberSnapshot).
func TestWireServiceMemberSnapshotKindIsTheClosedMemberKind(t *testing.T) {
	typed := WireServiceMemberSnapshot{Name: "n", Kind: MemberState, Sequence: 1}.Kind
	if typed != ServiceMemberKind("state") {
		t.Fatalf("kind = %q", typed)
	}
	var m WireServiceMemberSnapshot
	if err := json.Unmarshal([]byte(`{"name":"n","kind":"method"}`), &m); err != nil || m.Kind != MemberMethod {
		t.Fatalf("method kind decode: %v %q", err, m.Kind)
	}
	for _, unknown := range []string{`{"name":"n","kind":"other"}`, `{"name":"n","kind":"other","sequence":1,"ops":[]}`} {
		if err := json.Unmarshal([]byte(unknown), &m); err == nil {
			t.Fatalf("an unknown kind decoded: %s", unknown)
		}
	}
}
