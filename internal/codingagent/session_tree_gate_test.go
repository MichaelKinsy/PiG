//go:build !race

package codingagent

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/bench/sessionscale"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// The /tree selector over the 300 MB owner-shape session opens and renders its first screen within Pi 1.1.0's time for the same file (0.58 s
// from Enter to the selector, measured on the real binaries), because it reads no entry it does not show (opt-in: SESSIONSCALE_GATE=1,
// about 40 s and 300 MB of disk to generate the fixture).
func TestTreeSelectorOpensWithinPiTimeOnTheOwnerShape(t *testing.T) {
	if os.Getenv("SESSIONSCALE_GATE") != "1" {
		t.Skip("set SESSIONSCALE_GATE=1 to run the session-scale gates")
	}
	const piOpen = 580 * time.Millisecond
	dir := t.TempDir()
	if _, err := sessionscale.Write(dir, sessionscale.Shape{Bytes: sessionscale.OwnerBytes, Seed: 1}, ""); err != nil {
		t.Fatal(err)
	}
	s, _, err := loadPersistedSession(filepath.Join(dir, "session.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	best := time.Duration(1<<63 - 1)
	for range 3 {
		start := time.Now()
		roots, lt := s.lazyTree()
		selector := newLazyTreeSelectorComponent(roots, lt, s.GetLeafID(), 40, nil, "default")
		if len(selector.Render(120)) == 0 {
			t.Fatal("the selector rendered nothing")
		}
		best = min(best, time.Since(start))
	}
	t.Logf("/tree open and first render: %v (Pi 1.1.0: %v)", best, piOpen)
	testenv.TimingGateFatal(t, best > piOpen, "/tree opens in %v, slower than Pi's %v", best, piOpen)
	// Pi types the nine-key query "retry_100" in 2.0-2.5 s on the same file (real binary, reviewer).
	const piQuery = 2 * time.Second
	roots, lt := s.lazyTree()
	selector := newLazyTreeSelectorComponent(roots, lt, s.GetLeafID(), 40, nil, "default")
	start := time.Now()
	for _, key := range "retry_100" {
		selector.HandleInput(string(key))
		selector.Render(120)
	}
	query := time.Since(start)
	t.Logf("search query typed: %v (Pi 1.1.0: %v)", query, piQuery)
	testenv.TimingGateFatal(t, query > piQuery, "typing the search query takes %v, slower than Pi's %v", query, piQuery)
}
