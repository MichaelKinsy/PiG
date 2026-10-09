package sdk

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
)

// runner.ts:868-960 guards every ExtensionContext member with assertActive(), so a ctx captured before a session replacement or reload throws the stale message from cwd, mode, hasUI and model. The Host sends NotifyInvalidate to each connected extension for this (host.go:Invalidate).
func TestLocalContextMembersPanicWithTheStaleMessageAfterInvalidate(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	connection, host := newConn(client), newConn(server)
	connection.start()
	host.start()
	ext := New("stale-probe")
	ext.conn = connection
	ext.cwd, ext.mode, ext.hasUI, ext.model, ext.modelProvider = "/work", "print", true, "m", "p"
	captured := make(chan Context, 1)
	ext.Command("capture", "Capture", func(ctx Context, _ string) error { captured <- ctx; return nil })
	done := make(chan struct{})
	go func() { ext.handleRequest("origin", &requestMsg{Method: "command", Tool: "capture"}); close(done) }()
	ctx := <-captured
	<-done
	const stale = "This extension ctx is stale after session replacement or reload."
	args, _ := json.Marshal(map[string]string{"message": stale})
	// Before the notification the cached values answer.
	if ctx.Cwd() != "/work" || ctx.Mode() != "print" || !ctx.HasUI() || ctx.Model() != "m" {
		t.Fatalf("active context = %q %q %v %q", ctx.Cwd(), ctx.Mode(), ctx.HasUI(), ctx.Model())
	}
	ext.handleNotify(envelope{Type: msgNotify, Notify: &notifyMsg{Method: "invalidate", Args: args}})
	for name, read := range map[string]func(){
		"Cwd":   func() { _ = ctx.Cwd() },
		"Mode":  func() { _ = ctx.Mode() },
		"HasUI": func() { _ = ctx.HasUI() },
		"Model": func() { _ = ctx.Model() },
	} {
		func() {
			defer func() {
				got := recover()
				if got == nil || !strings.Contains(toString(got), stale) {
					t.Errorf("%s after invalidate: recovered %v, want the stale message", name, got)
				}
			}()
			read()
		}()
	}
}

func toString(v any) string {
	switch t := v.(type) {
	case error:
		return t.Error()
	case string:
		return t
	}
	return ""
}

// A later invalidate does not replace the first message: runner.ts:725-727 keeps the first staleMessage.
func TestFirstInvalidateMessageWins(t *testing.T) {
	ext := New("stale-first")
	for _, message := range []string{"first", "second"} {
		args, _ := json.Marshal(map[string]string{"message": message})
		ext.handleNotify(envelope{Type: msgNotify, Notify: &notifyMsg{Method: "invalidate", Args: args}})
	}
	defer func() {
		if got := toString(recover()); got != "first" {
			t.Fatalf("stale message = %q, want first", got)
		}
	}()
	_ = Context{ext: ext}.Cwd()
}

// A replacement context belongs to the new session, so the old runtime's invalidation does not reach it (runner.ts: the withSession ctx is bound to the new runner).
func TestReplacementContextIsNotStaleAfterInvalidate(t *testing.T) {
	ext := New("stale-replacement")
	args, _ := json.Marshal(map[string]string{"message": "stale"})
	ext.handleNotify(envelope{Type: msgNotify, Notify: &notifyMsg{Method: "invalidate", Args: args}})
	ctx := Context{ext: ext, replacement: &replacementState{cwd: "/new", mode: "tui", hasUI: true}}
	if ctx.Cwd() != "/new" || ctx.Mode() != "tui" || !ctx.HasUI() {
		t.Fatalf("replacement context = %q %q %v", ctx.Cwd(), ctx.Mode(), ctx.HasUI())
	}
}
