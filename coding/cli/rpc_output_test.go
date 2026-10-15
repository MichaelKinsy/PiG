package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

func TestRPCOutputDrainsBufferedResponseBeforeEOF(t *testing.T) {
	p := &rpcProcess{t: t, records: make(chan rpcRecord, 64), stderr: &lockedBuffer{}, budget: testbudget.Wait(t)}
	p.scanOutput(strings.NewReader("{\"id\":\"final\"}\n"))
	<-p.outputDone
	p.await("final response", func(r rpcRecord) bool { return r["id"] == "final" })
}

func TestRPCOutputCancelReleasesBlockedScanner(t *testing.T) {
	p := &rpcProcess{records: make(chan rpcRecord, 1)}
	p.scanOutput(strings.NewReader(strings.Repeat("{}\n", 128)))
	// Fill the queue so the scanner cannot finish without cancellation.
	deadline := time.After(testbudget.Wait(t))
	for len(p.records) == 0 {
		select {
		case <-deadline:
			t.Fatal("scanner did not publish a record")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	close(p.stopOutput)
	select {
	case <-p.outputDone:
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("scanner leaked while queue was full")
	}
}
