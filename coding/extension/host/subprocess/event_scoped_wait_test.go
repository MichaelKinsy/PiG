package subprocess

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi awaits an extension handler with the provider still running (agent-session.ts:894-919 awaits _emitExtensionEvent; runner.ts:emit awaits each handler). The host handler waits for its extension process, so it must release the stream's continuation queue for that wait. A provider reaction that is queued behind the caller's turn can only run if the handler suspends.
func TestEventHandlerReleasesStreamQueueWhileExtensionRuns(t *testing.T) {
	failureBound, cancelBound := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancelBound()
	hostEnd, peer := net.Pipe()
	defer func() { _ = peer.Close() }()
	conn := NewConn("observe", hostEnd)
	conn.Start(t.Context())
	managed := withConn(&managedExt{config: ExtConfig{Name: "observe"}, host: NewHost(t.TempDir())}, conn)
	handler := managed.makeEventHandler("message_start", 1)

	progressed := make(chan struct{})
	peerDone := make(chan error, 1)
	go func() {
		peerDone <- func() error {
			var header [4]byte
			if _, err := io.ReadFull(peer, header[:]); err != nil {
				return err
			}
			body := make([]byte, binary.BigEndian.Uint32(header[:]))
			if _, err := io.ReadFull(peer, body); err != nil {
				return err
			}
			var request Envelope
			if err := json.Unmarshal(body, &request); err != nil {
				return err
			}
			// The extension replies only after the provider has advanced. A host that keeps the queue across the wait never lets it advance.
			select {
			case <-progressed:
			case <-failureBound.Done():
				return failureBound.Err()
			}
			reply, err := json.Marshal(Envelope{Type: MsgResponse, ID: request.ID, Response: &ResponsePayload{Result: json.RawMessage("null")}})
			if err != nil {
				return err
			}
			binary.BigEndian.PutUint32(header[:], uint32(len(reply)))
			_, err = peer.Write(append(header[:], reply...))
			return err
		}()
	}()

	ctx := ai.WithStreamContinuations(failureBound)
	model := &ai.Model{ID: "probe"}
	var forwarded *ai.AssistantMessageEventStream
	err := ai.RunStreamContinuation(ctx, func(observation *ai.StreamObservation) error {
		// This lazy stream's setup is a reaction behind the current turn: it is the provider making progress.
		forwarded = ai.LazyStream(ctx, model, func(context.Context) (*ai.AssistantMessageEventStream, error) {
			close(progressed)
			source := ai.NewAssistantMessageEventStream()
			_ = source.Push(ai.DoneEvent{Reason: ai.StopReasonStop, Message: &ai.AssistantMessage{Content: []ai.AssistantContentBlock{}, StopReason: ai.StopReasonStop}})
			source.End()
			return source, nil
		})
		result, err := handler(map[string]any{"type": "message_start"}, observation.Context(context.Background()))
		if err != nil || result != nil {
			t.Errorf("handler returned %#v, %v; want nil, nil", result, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-peerDone; err != nil {
		t.Fatalf("provider did not advance while the handler waited for the extension: %v", err)
	}
	if forwarded.Result().StopReason != ai.StopReasonStop {
		t.Fatal("forwarded stream did not settle after the handler returned")
	}
}
