package client

import (
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
)

// Pi packages/client/src/client.ts onConnectionStateChange / onAttachmentChange: the returned Unsubscribe removes exactly that listener; the others keep receiving.
func TestUnsubscribeRemovesOnlyItsListener(t *testing.T) {
	t.Parallel()
	server := newMemoryByteServer("")
	client := mustConnectClient(t, server)

	kept, dropped := &stateRecorder{}, &stateRecorder{}
	if _, err := client.OnConnectionStateChange(kept.listener()); err != nil {
		t.Fatal(err)
	}
	unsubscribe, err := client.OnConnectionStateChange(dropped.listener())
	if err != nil {
		t.Fatal(err)
	}
	typed := Unsubscribe(unsubscribe)
	typed()
	typed() // idempotent, like Set.delete

	var mu sync.Mutex
	var keptAttachments, droppedAttachments []*protocol.SessionTarget
	if _, err := client.OnAttachmentChange(NewAttachmentChangeListener(func(target *protocol.SessionTarget) {
		mu.Lock()
		keptAttachments = append(keptAttachments, target)
		mu.Unlock()
	})); err != nil {
		t.Fatal(err)
	}
	unsubscribeAttachment, err := client.OnAttachmentChange(NewAttachmentChangeListener(func(target *protocol.SessionTarget) {
		mu.Lock()
		droppedAttachments = append(droppedAttachments, target)
		mu.Unlock()
	}))
	if err != nil {
		t.Fatal(err)
	}
	unsubscribeAttachment()

	server.send(t, protocol.AttachmentEnvelope{Attachment: &protocol.SessionTarget{ServerId: testServerId, SessionId: "s", AttachmentId: "a"}})
	pollFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(keptAttachments) == 1 })
	client.Disconnect("Client disconnected")
	pollFor(t, func() bool { return len(kept.states()) == 1 })

	mu.Lock()
	defer mu.Unlock()
	if len(droppedAttachments) != 0 || len(dropped.states()) != 0 {
		t.Fatalf("unsubscribed listeners were called: attachments=%d states=%v", len(droppedAttachments), dropped.states())
	}
	if keptAttachments[0].AttachmentId != "a" || kept.states()[0] != Disconnected {
		t.Fatalf("kept attachments=%v states=%v", keptAttachments, kept.states())
	}
}

func pollFor(t *testing.T, condition func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); !condition(); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
	}
}
