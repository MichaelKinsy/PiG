package coding

import (
	"fmt"
	"sync"
	"testing"

	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// A Session's methods may be called from any goroutine (the Session doc), and a host call that reads the Session's id, path or working directory runs on
// another goroutine than the /new, /resume or /fork that swaps the log with ReplaceInner. The swap holds the Session's mutex through the host's report, so
// the read must not wait for it, and must not race with it either: -race fails this test when the log pointer is a plain field.
func TestSessionLogAccessorsReadTheLogWhileReplaceInnerSwapsIt(t *testing.T) {
	session, err := NewSession(warmingServices(t, "off"), SessionOptions{Model: warmingModel(&warmingProvider{}), NoSession: true, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	cwd := session.CWD()
	const replacements = 50
	var readers sync.WaitGroup
	for range 4 {
		readers.Go(func() {
			for range 20 * replacements {
				_ = session.ID()
				_ = session.Path()
				_ = session.CWD()
				_ = session.Inner()
			}
		})
	}
	for i := range replacements {
		session.ReplaceInner(icodingagent.NewSession(fmt.Sprintf("replacement-%d", i), cwd))
	}
	readers.Wait()
	if got, want := session.ID(), fmt.Sprintf("replacement-%d", replacements-1); got != want {
		t.Fatalf("Session.ID() after the last ReplaceInner = %q, want %q", got, want)
	}
}
