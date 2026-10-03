package subprocess

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net"
	"slices"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// heldSelectUI answers a dialog when release closes, after it marked the call initiated.
type heldSelectUI struct {
	extension.UIContext
	release chan struct{}
}

func (u *heldSelectUI) Select(ctx context.Context, _ string, _ []string, _ extension.ExtensionUIDialogOptions) (string, error) {
	extension.CallInitiated(ctx)
	<-u.release
	return "yes", nil
}

// The host counts a command's dialogs and its other host calls from the call frames it applies, so a concurrent call does not erase a dialog the way the last request_state report did, and a dialog counts from its own frame, not from a report that precedes it.
func TestHostCallsAreCountedPerRequestFromTheirFrames(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
		releaseSelect := make(chan struct{})
		releaseExec := make(chan struct{})
		bridge := NewUIBridge(func() {})
		bridge.SetUIContext(&heldSelectUI{UIContext: extension.NoopUIContext, release: releaseSelect})
		bridge.SetHostAction("newSession", func(ctx context.Context, _ *extension.NewSessionOptions) (extension.CancelledResult, error) {
			extension.CallInitiated(ctx)
			<-releaseExec
			return extension.CancelledResult{}, nil
		})
		h.SetUIBridge(bridge)
		hostSide, extSide := net.Pipe()
		ctx, cancel := context.WithCancel(t.Context())
		conn := NewConn("count", hostSide)
		conn.Start(ctx)
		me := withConn(&managedExt{config: ExtConfig{Name: "count"}, host: h}, conn)
		me.shuttingDown.Store(true)
		h.exts = map[string]*managedExt{"count": me}
		incomingDone := make(chan struct{})
		go func() {
			defer close(incomingDone)
			h.handleIncoming(me, conn)
		}()
		requests := make(chan string, 1)
		go func() {
			for {
				env, err := readEnvelope(extSide)
				if err != nil {
					return
				}
				if env.Type == MsgRequest {
					requests <- env.ID
				}
			}
		}()
		var mu sync.Mutex
		var events []string
		record := func(dialog bool, delta int) {
			kind := "call"
			if dialog {
				kind = "dialog"
			}
			sign := "+"
			if delta < 0 {
				sign = "-"
			}
			mu.Lock()
			events = append(events, sign+kind)
			mu.Unlock()
		}
		snapshot := func() []string {
			mu.Lock()
			defer mu.Unlock()
			return slices.Clone(events)
		}
		requestDone := make(chan struct{})
		go func() {
			defer close(requestDone)
			_, _ = conn.Request(withRequestHostCalls(ctx, record), &Envelope{Type: MsgRequest, Request: &RequestPayload{Method: "command", Tool: "cmd"}})
		}()
		id := <-requests
		write := func(env Envelope) {
			t.Helper()
			data, err := json.Marshal(env)
			if err != nil {
				t.Fatal(err)
			}
			var length [4]byte
			binary.BigEndian.PutUint32(length[:], uint32(len(data)))
			if _, err := extSide.Write(append(length[:], data...)); err != nil {
				t.Fatal(err)
			}
		}
		write(Envelope{Type: MsgCall, ID: "select", Call: &CallPayload{Method: "ui.select", ParentRequestID: id, Args: json.RawMessage(`{"title":"Ask","options":["yes"]}`)}})
		synctest.Wait()
		if got, want := snapshot(), []string{"+dialog"}; !slices.Equal(got, want) {
			t.Fatalf("events after the dialog frame = %v, want %v", got, want)
		}
		write(Envelope{Type: MsgCall, ID: "exec", Call: &CallPayload{Method: "newSession", ParentRequestID: id, Args: json.RawMessage(`{}`)}})
		synctest.Wait()
		// A call that does not keep Pi's loop alive is not counted at all.
		write(Envelope{Type: MsgCall, ID: "status", Call: &CallPayload{Method: "ui.setStatus", ParentRequestID: id, Args: json.RawMessage(`{}`)}})
		// A call of another request is not this command's.
		write(Envelope{Type: MsgCall, ID: "other", Call: &CallPayload{Method: "newSession", ParentRequestID: "another", Args: json.RawMessage(`{}`)}})
		synctest.Wait()
		if got, want := snapshot(), []string{"+dialog", "+call"}; !slices.Equal(got, want) {
			t.Fatalf("events with a dialog and an exec = %v, want %v", got, want)
		}
		close(releaseExec)
		synctest.Wait()
		if got, want := snapshot(), []string{"+dialog", "+call", "-call"}; !slices.Equal(got, want) {
			t.Fatalf("events after the exec returned = %v, want %v: the dialog stays counted", got, want)
		}
		close(releaseSelect)
		synctest.Wait()
		if got, want := snapshot(), []string{"+dialog", "+call", "-call", "-dialog"}; !slices.Equal(got, want) {
			t.Fatalf("events after the dialog returned = %v, want %v", got, want)
		}
		cancel()
		_ = extSide.Close()
		<-conn.Done()
		<-incomingDone
		<-requestDone
	})
}
