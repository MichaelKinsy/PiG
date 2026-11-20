package codingagent

import (
	"errors"
	"testing"
)

func TestDeferredReloadFailurePrefixesOneLineAndKeepsContinuation(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want string
	}{
		{"success", nil, "resume\nnext step"},
		{"failure", errors.New("build failed"), "[reload failed: build failed]\nresume\nnext step"},
		{"multiline failure", errors.New("build failed:\n\tinvalid source"), "[reload failed: build failed: invalid source]\nresume\nnext step"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := reloadContinuationText("resume\nnext step", test.err); got != test.want {
				t.Fatalf("continuation = %q, want %q", got, test.want)
			}
		})
	}
}

func TestPendingReloadWaitsForTheActiveTurn(t *testing.T) {
	m := &InteractiveMode{pendingReloadContinuations: []string{"resume"}}
	m.turnActive.Store(true)
	m.runPendingReloadContinuations()
	if len(m.pendingReloadContinuations) != 1 {
		t.Fatal("an active turn consumed the deferred reload")
	}
	m.turnActive.Store(false)
	m.isCompacting = true
	m.runPendingReloadContinuations()
	if len(m.pendingReloadContinuations) != 1 {
		t.Fatal("active compaction consumed the deferred reload")
	}
}
