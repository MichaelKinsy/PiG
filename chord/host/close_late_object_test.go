package host

import (
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// A hold that a link delivers while Close runs creates an object after Close took its snapshot of the objects. Close disposes it once the links are closed. Without that, its resource open, which waits for the abort a disposal raises, kept waitTasks blocked for good (the portmap-3 chord/host evidence run that timed out after 10 minutes; `go test -count=1000 -cpu 2,4 -run Awaiting ./chord/host` hung on 3ee123c6 as well).
//
// The seam afterCloseDisposal creates the object in exactly that window, after the snapshot's objects were disposed and before the links close: nothing else disposes it.
func TestCloseDisposesAnObjectCreatedAfterItsSnapshot(t *testing.T) {
	gate := newRequestsLatch()
	t.Cleanup(gate.open)
	server := requestsHost(t, HostOptions{Name: "server"})
	requestsServe(t, server.Serve, awaitingSlow, awaitingSlowFactory(gate), ServeOptions{})
	server.afterCloseDisposal = func() {
		ref := requestsGet(t, server, awaitingSlow, map[string]any{}, nil)
		// The object is activating: its resource open waits on the gate or on the abort of a disposal.
		if status := ref.Status(); status == RefGone {
			t.Errorf("the late object's reference is already gone (%v): the window was not entered", status)
		}
	}
	closed := make(chan error, 1)
	go func() { closed <- server.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(testbudget.Wait(t)):
		gate.open() // frees the open the missing disposal left running, so the cleanup's Close returns
		t.Fatal("Close did not return: the object created after its snapshot was never disposed")
	}
	var remaining int
	server.run(func() { remaining = server.objects.Len() })
	if remaining != 0 {
		t.Fatalf("%d objects left after Close", remaining)
	}
}
