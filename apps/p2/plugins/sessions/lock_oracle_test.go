package sessions_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/oracle"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions"
)

// The lock mirror fingerprints as Pi's `LockMirror`, which lock-worker.ts declares without exporting it: the oracle records
// every contract a module declares (plugins/sessions/lock-worker.ts, loaded under Node with stubs for its runtime imports).
func TestLockMirrorFingerprintsAsPi(t *testing.T) {
	const module = "plugins/sessions/lock-worker.ts"
	want, ok := oracle.Fingerprints(t, module)[module+"#declared:p2.session-lock-mirror"]
	if !ok {
		t.Fatal("lock-worker.ts declares no p2.session-lock-mirror")
	}
	got := sessions.LockMirror
	if got.Name != want.Name || got.Version != want.Version || got.Fingerprint != want.Fingerprint {
		t.Errorf("LockMirror = {%s v%d %s}, Pi has {%s v%d %s}", got.Name, got.Version, got.Fingerprint, want.Name, want.Version, want.Fingerprint)
	}
}
