package subprocess

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// heldKind is one kind of call whose failure the host classifies when it ends the call itself (dispatchError): the host request the extension holds and the call that makes it.
type heldKind struct {
	name   string
	method string
	call   func(ctx context.Context, ext *extension.Extension) error
}

var heldKinds = []heldKind{
	{"event", "event", func(ctx context.Context, ext *extension.Extension) error {
		_, err := ext.Handlers["session_start"][0](map[string]any{"type": "session_start", "reason": "fork"}, ctx)
		return err
	}},
	{"command", "command", func(ctx context.Context, ext *extension.Extension) error {
		command, ok := ext.Command("held")
		if !ok {
			return errors.New("no command held")
		}
		return command.Handler(ctx, "")
	}},
	{"shortcut", "shortcut", func(ctx context.Context, ext *extension.Extension) error {
		return ext.Shortcuts["ctrl+shift+h"].Handler(ctx)
	}},
}

// holdACall loads an extension that holds the call of kind: the call is made, the extension has received its request and answers it only when the test sends the answer to release.
func holdACall(t *testing.T, kind heldKind) (host *Host, me *managedExt, w *wireExt, release chan *ResponsePayload, result <-chan error) {
	t.Helper()
	host = newWireHost(t)
	w = newWireExt(t)
	release = make(chan *ResponsePayload)
	t.Cleanup(func() { close(release) })
	w.handle = func(env *Envelope) *ResponsePayload {
		if env.Request.Method != kind.method {
			return nil
		}
		return <-release
	}
	ext, err := loadWireExt(t, host, w, &RegisterPayload{
		Name:      "held",
		Handlers:  []HandlerDecl{{Event: "session_start", HandlerID: 1}},
		Commands:  []CommandDecl{{Name: "held", Description: "holds"}},
		Shortcuts: []ShortcutDecl{{Key: "ctrl+shift+h", Description: "holds"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), testbudget.Wait(t))
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- kind.call(ctx, ext) }()
	w.request(kind.method)
	host.mu.Lock()
	me = host.exts["held"]
	host.mu.Unlock()
	if me == nil {
		t.Fatal("the host holds no extension named held")
	}
	return host, me, w, release, done
}

func awaitHeldCall(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-t.Context().Done():
		t.Fatal("the call did not end")
		return nil
	}
}

// A user who orders /fork, /new or /resume while a handler of the old extension has not returned stops that extension: the stop closes the connection, and the connection answers the request still pending with its own "connection closed". The extension did not give that answer, so the runner must not report it as the handler's failure, as it does not report the same call cut short by a transport error. The extension's own error answer and a crash stay reported, whatever their text. Pi runs the extension in its own process and lets the handler finish, so it shows nothing.
func TestAHandlerPendingWhenTheHostStopsItsExtensionIsNotItsFailure(t *testing.T) {
	for _, kind := range heldKinds {
		t.Run(kind.name+"/session swap", func(t *testing.T) {
			host, me, _, _, result := holdACall(t, kind)
			host.stopManaged(me, "session swap")
			err := awaitHeldCall(t, result)
			if !errors.Is(err, extension.ErrHandlerStopped) {
				t.Fatalf("a call the host cut short by stopping the extension = %v, want it marked as stopped by the host", err)
			}
			if !strings.Contains(err.Error(), "connection closed") {
				t.Fatalf("the cut-short call lost its text: %v", err)
			}
		})
		t.Run(kind.name+"/host shutdown", func(t *testing.T) {
			host, _, _, _, result := holdACall(t, kind)
			host.Shutdown("quit")
			if err := awaitHeldCall(t, result); !errors.Is(err, extension.ErrHandlerStopped) {
				t.Fatalf("a call the host cut short by shutting down = %v, want it marked as stopped by the host", err)
			}
		})
		t.Run(kind.name+"/crash", func(t *testing.T) {
			_, _, w, _, result := holdACall(t, kind)
			_ = w.conn.Close()
			err := awaitHeldCall(t, result)
			if err == nil || errors.Is(err, extension.ErrHandlerStopped) {
				t.Fatalf("a call a crashed extension left pending = %v, want a reported failure", err)
			}
			if !strings.Contains(err.Error(), "connection closed") && !strings.Contains(err.Error(), "EOF") {
				t.Fatalf("the crashed call's text changed: %v", err)
			}
		})
		t.Run(kind.name+"/own error", func(t *testing.T) {
			_, _, _, release, result := holdACall(t, kind)
			release <- &ResponsePayload{Error: &ErrorInfo{Message: "handler failed"}}
			err := awaitHeldCall(t, result)
			if err == nil || errors.Is(err, extension.ErrHandlerStopped) || !strings.Contains(err.Error(), "handler failed") {
				t.Fatalf("the extension's own error = %v, want it reported as the handler's", err)
			}
		})
		t.Run(kind.name+"/own error with the text of the close", func(t *testing.T) {
			_, me, _, release, result := holdACall(t, kind)
			// The stop has begun, and the extension answers with an error of its own whose text is the connection's: it is the extension's answer, so it stays the handler's failure.
			me.shuttingDown.Store(true)
			release <- &ResponsePayload{Error: &ErrorInfo{Message: "connection closed"}}
			err := awaitHeldCall(t, result)
			if err == nil || errors.Is(err, extension.ErrHandlerStopped) {
				t.Fatalf("an extension's own error that reads \"connection closed\" = %v, want it reported as the handler's", err)
			}
		})
	}
}
