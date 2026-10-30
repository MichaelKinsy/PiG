//go:build chordstall

package parking

import (
	"fmt"
	"os"
	"runtime"
	"time"
)

// StallAfter is how long a section waits for its handler before it reports the wait (the chordstall build only).
var StallAfter = 5 * time.Second

// Report receives the report of a stalled wait: the text and the stacks of every goroutine, the handler's among them. It
// writes to os.Stderr by default. A report changes nothing: the section keeps waiting, and a handler that parks late goes on
// as one that parks at once.
var Report = func(text string) { fmt.Fprint(os.Stderr, text) }

// wait is Wait that reports once when the handler holds the section for StallAfter without returning or parking: a handler
// that waits for something that is not a host primitive (a raw channel receive, a lock) before its first host call, which
// Pi's `await` would not do.
func (s *Signal) wait() {
	timer := time.NewTimer(StallAfter)
	defer timer.Stop()
	select {
	case <-s.done:
		return
	case <-timer.C:
	}
	stacks := make([]byte, 1<<20)
	stacks = stacks[:runtime.Stack(stacks, true)]
	Report(fmt.Sprintf("chord: a method handler has held the section that admitted its call for %s without returning or parking (waiting on a raw channel or a lock before its first host call?); use chordctx.Receive or chordctx.Await, or wait with a host primitive:\n%s\n", StallAfter, stacks))
	<-s.done
}
