package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/durable"
)

// A VersionSignal wakes its waiters after a change, and a context that ends ends a wait (commits.ts's signal, as the MCP
// worker's activations use it).

func TestVersionSignalWakesWaitersOnABump(t *testing.T) {
	var signal VersionSignal
	seen := signal.Version()
	woke := make(chan error, 1)
	go func() { woke <- signal.Next(context.Background(), seen) }()
	select {
	case err := <-woke:
		t.Fatalf("woken without a change: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	signal.Bump()
	select {
	case err := <-woke:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a change must wake the waiter")
	}
	if signal.Version() != seen+1 {
		t.Fatalf("version %d", signal.Version())
	}
	// A version the waiter has not seen resolves at once.
	if err := signal.Next(context.Background(), seen); err != nil {
		t.Fatal(err)
	}
}

func TestVersionSignalNextEndsWithItsContext(t *testing.T) {
	var signal VersionSignal
	ctx, cancel := context.WithCancelCause(context.Background())
	cause := errors.New("done")
	woke := make(chan error, 1)
	go func() { woke <- signal.Next(ctx, signal.Version()) }()
	time.Sleep(20 * time.Millisecond)
	cancel(cause)
	if err := <-woke; !errors.Is(err, cause) {
		t.Fatalf("error %v", err)
	}
	if err := signal.Next(ctx, signal.Version()); !errors.Is(err, cause) {
		t.Fatalf("an ended context refuses at once: %v", err)
	}
}

func TestChangesDocumentIsAboutTheDocumentKindOnly(t *testing.T) {
	if ChangesDocument(durable.CommitPublication{}, "p2.subagents") {
		t.Fatal("an empty commit changes no document")
	}
	publication := durable.CommitPublication{Changes: []durable.CommitChange{durable.DocumentChange{Record: durable.DocumentRecord{Kind: "p2.subagents"}}}}
	if !ChangesDocument(publication, "p2.subagents") || ChangesDocument(publication, "p2.other") {
		t.Fatal("a document's change is for its own kind")
	}
}
