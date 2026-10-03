package sdk

import (
	"fmt"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// Pi runs every extension handler in one process and a handler can always await a host call (packages/coding-agent/src/core/extensions/types.ts: the `ctx.ui` dialogs `select`, `confirm` and `input`, `exec` and `executeTool` return promises; `notify` and `setFooter` are synchronous in-process calls that cannot wait on anything). A pig handler that makes a blocking host call therefore has to complete whatever else the host sends meanwhile. A width handler is a pig-only trigger (OnWidthChange); it makes the same calls any other handler makes.

// armWidthHandler registers the width handler through a command, the way an extension subscribes, and runs that command.
func armWidthHandler(t *testing.T, host *mockHost) {
	t.Helper()
	if _, resp := runSurfaceCommand(t, host, "arm", func(*callMsg) *callResultMsg { return &callResultMsg{} }); resp.Error != nil {
		t.Fatal(resp.Error)
	}
}

func readCall(t *testing.T, host *mockHost) envelope {
	t.Helper()
	for {
		if env := host.readEnvelope(t); env.Type == msgCall {
			return env
		}
	}
}

// A host that streams more notifies than the message queue holds before it answers the handler's call: the read loop cannot stop routing replies because the queue is full, or the reply behind those notifies is never read.
func TestWidthHandlerHostCallCompletesWhileTheHostStreamsNotifies(t *testing.T) {
	ext := New("width-call")
	done := make(chan struct{})
	ext.Command("arm", "", func(ctx Context, _ string) error {
		_, err := ctx.OnWidthChange(func(c Context, w int) {
			if err := c.SetFooter([]string{"footer"}); err != nil {
				t.Errorf("SetFooter: %v", err)
			}
			close(done)
		})
		return err
	})
	host, _, finished := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, finished)
	armWidthHandler(t, host)

	host.writeEnvelope(t, envelope{Type: msgNotify, Notify: &notifyMsg{Method: "width_change", Args: json.RawMessage(`{"width":100}`)}})
	call := readCall(t, host)
	if call.Call.Method != "ui.setFooter" {
		t.Fatalf("call = %s", call.Call.Method)
	}
	for range 64 { // the message queue holds 16 (protocol.go newConn)

		sendState(t, host, `{"settings":{}}`)
	}
	host.writeEnvelope(t, envelope{Type: msgCallResult, ID: call.ID, CallResult: &callResultMsg{}})
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the width handler's host call never returned: the extension stopped reading its replies")
	}
}

// A handler that is blocked on a host call does not stop the extension serving the host's other messages: a command request starts and finishes while it waits.
func TestBlockedWidthHandlerDoesNotStopRequests(t *testing.T) {
	ext := New("width-requests")
	blocked := make(chan struct{})
	ext.Command("arm", "", func(ctx Context, _ string) error {
		_, err := ctx.OnWidthChange(func(c Context, w int) {
			close(blocked)
			c.Notify("from the width handler", "info")
		})
		return err
	})
	ext.Command("ping", "", func(Context, string) error { return nil })
	host, _, finished := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, finished)
	watchdog := time.AfterFunc(10*time.Second, host.close)
	defer watchdog.Stop()
	armWidthHandler(t, host)

	host.writeEnvelope(t, envelope{Type: msgNotify, Notify: &notifyMsg{Method: "width_change", Args: json.RawMessage(`{"width":100}`)}})
	call := readCall(t, host)
	recv(t, blocked)
	// The handler's call has no reply yet.
	_, resp := runSurfaceCommand(t, host, "ping", func(*callMsg) *callResultMsg { return &callResultMsg{} })
	if resp.Error != nil {
		t.Fatalf("command while the handler waits: %+v", resp.Error)
	}
	host.writeEnvelope(t, envelope{Type: msgCallResult, ID: call.ID, CallResult: &callResultMsg{}})
}

// Width deliveries reach the handlers one at a time, in the order the host sent the widths, while an earlier delivery waits on a host call.
func TestWidthHandlersRunInOrderWhileOneBlocks(t *testing.T) {
	ext := New("width-order")
	seen := make(chan int, 3)
	ext.Command("arm", "", func(ctx Context, _ string) error {
		_, err := ctx.OnWidthChange(func(c Context, w int) {
			c.Notify("width", "info")
			seen <- w
		})
		return err
	})
	host, _, finished := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, finished)
	watchdog := time.AfterFunc(10*time.Second, host.close)
	defer watchdog.Stop()
	armWidthHandler(t, host)

	for _, w := range []int{100, 90, 80} {
		host.writeEnvelope(t, envelope{Type: msgNotify, Notify: &notifyMsg{Method: "width_change", Args: json.RawMessage(fmt.Sprintf(`{"width":%d}`, w))}})
	}
	for range 3 {
		call := readCall(t, host)
		host.writeEnvelope(t, envelope{Type: msgCallResult, ID: call.ID, CallResult: &callResultMsg{}})
	}
	for _, want := range []int{100, 90, 80} {
		if got := recv(t, seen); got != want {
			t.Fatalf("delivered width %d, want %d", got, want)
		}
	}
}

// A width handler that panics does not take the extension process down or stop the handlers after it, as a request handler's panic fails one request only (extension.go handleArmedRequest).
func TestWidthHandlerPanicDoesNotStopTheExtension(t *testing.T) {
	ext := New("width-panic")
	seen := make(chan int, 1)
	ext.Command("arm", "", func(ctx Context, _ string) error {
		if _, err := ctx.OnWidthChange(func(Context, int) { panic("handler failed") }); err != nil {
			return err
		}
		_, err := ctx.OnWidthChange(func(_ Context, w int) { seen <- w })
		return err
	})
	ext.Command("ping", "", func(Context, string) error { return nil })
	host, _, finished := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, finished)
	watchdog := time.AfterFunc(10*time.Second, host.close)
	defer watchdog.Stop()
	armWidthHandler(t, host)

	host.writeEnvelope(t, envelope{Type: msgNotify, Notify: &notifyMsg{Method: "width_change", Args: json.RawMessage(`{"width":100}`)}})
	if got := recv(t, seen); got != 100 {
		t.Fatalf("second handler saw width %d", got)
	}
	if _, resp := runSurfaceCommand(t, host, "ping", func(*callMsg) *callResultMsg { return &callResultMsg{} }); resp.Error != nil {
		t.Fatalf("command after the panic: %+v", resp.Error)
	}
}

// Shutdown ends a width handler that waits on a host call the host never answers, as the Python and Rust SDKs end every waiting call when they stop (test_shutdown_ends_a_width_handler_blocked_on_a_host_call, shutdown_ends_a_width_handler_blocked_on_a_host_call). A width handler has no request whose cancellation would end the call, so without this Run waits out the handler deadline and fails.
func TestShutdownEndsAWidthHandlerBlockedOnAHostCall(t *testing.T) {
	ext := New("width-shutdown")
	ended := make(chan error, 1)
	ext.Command("arm", "", func(ctx Context, _ string) error {
		_, err := ctx.OnWidthChange(func(c Context, _ int) {
			ended <- c.SetFooter([]string{"never answered"})
		})
		return err
	})
	host, _, finished := surfaceHost(t, ext, nil)
	armWidthHandler(t, host)

	host.writeEnvelope(t, envelope{Type: msgNotify, Notify: &notifyMsg{Method: "width_change", Args: json.RawMessage(`{"width":100}`)}})
	readCall(t, host)
	start := time.Now()
	surfaceShutdown(t, host, finished)
	if elapsed := time.Since(start); elapsed >= extensionHandlerStopTimeout {
		t.Fatalf("shutdown waited %v for the blocked width handler", elapsed)
	}
	if err := recv(t, ended); err == nil {
		t.Fatal("the unanswered host call returned no error after shutdown")
	}
}
