//go:build !windows

package codingagent

import (
	"os"
	"testing"
	"time"
)

// Node delivers stdin to Pi's listener only after the synchronous start has run, so a held reader reads nothing until its hold is closed: the
// reply a terminal sends at once waits in the pipe, then arrives whole.
func TestHeldInteractiveTerminalReaderReadsOnlyAfterTheHoldCloses(t *testing.T) {
	in, out, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = in.Close(); _ = out.Close() }()
	hold := make(chan struct{})
	reads := newTerminalReads()
	reader := newHeldInteractiveTerminalReader(t.Context(), in, reads, hold)
	defer reader.stop()
	if _, err := out.WriteString("reply"); err != nil {
		t.Fatal(err)
	}
	select {
	case data := <-reads.data:
		t.Fatalf("read %q before the hold closed", data)
	case <-time.After(100 * time.Millisecond):
	}
	close(hold)
	select {
	case data := <-reads.data:
		if string(data) != "reply" {
			t.Fatalf("read %q, want the reply that waited", data)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the reply was not read after the hold closed")
	}
}
