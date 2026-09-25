package coding

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// The public construction path must expose the UUIDv7 identity used in the
// session header, session filename, extension context, and resume hint.
func TestNewSessionUsesUUIDv7Identity(t *testing.T) {
	services := newTestServices(t)
	for _, noSession := range []bool{false, true} {
		session, err := NewSession(services, SessionOptions{Model: fakeModel(), NoSession: noSession})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = session.Close() })
		id, err := uuid.Parse(session.ID())
		if err != nil || id.Version() != 7 {
			t.Errorf("NewSession(NoSession=%v).ID() = %q, want UUIDv7", noSession, session.ID())
		}
		if session.Inner().Header().ID != session.ID() {
			t.Fatal("runtime and persisted header disagree on session identity")
		}
		if !noSession && !strings.HasSuffix(filepath.Base(session.Path()), "_"+session.ID()+".jsonl") {
			t.Errorf("session file %q does not carry identity %q", session.Path(), session.ID())
		}
	}
}
