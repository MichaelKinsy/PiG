//go:build !race

package cli

import (
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testscaling"
)

// Resolving an exact session id reads the headers of the project's sessions and no transcript body (Pi main.ts findLocalSessionByExactId), so its cost must not grow with the size of the sessions. The race detector inflates allocations, so the measurement runs without it.
func TestScalingResolveExactSessionIDWithSessionSize(t *testing.T) {
	testscaling.Check(t, testscaling.Case{Name: "--session <exact id>, 20 sessions of growing size", Size: 100, Bound: testscaling.Constant, Prepare: func(msgs int) func() {
		dir := filepath.Join(t.TempDir(), "s")
		id := writeExactIDSessions(t, dir, 20, msgs)
		return func() {
			sel, err := resolveStartupSessionSelectionIn(Args{Session: id}, "/work/proj0", dir)
			if err != nil || sel.resumePath == "" {
				panic(err)
			}
		}
	}})
}
