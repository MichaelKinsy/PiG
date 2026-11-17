package sdk

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
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

// Pi guards every context member (runner.ts:883-1037). After the session was replaced the members that cannot return an error panic with the stale message
// and the calls that can return it, and a call that fails changes nothing: the widget a renderer installed is still there.
func TestStaleContextMembersFailWithTheStaleMessageAndChangeNothing(t *testing.T) {
	const stale = "This extension ctx is stale after session replacement or reload."
	ext := New("stale-members")
	ext.width, ext.height = 90, 30
	ctx := Context{ext: ext, toolCallID: "tool", requestID: "request"}
	if ctx.Width() != 90 || ctx.Height() != 30 || ctx.SessionName() != "" {
		t.Fatalf("active context = %d %d %q", ctx.Width(), ctx.Height(), ctx.SessionName())
	}
	renderer := &surfaceRenderer{method: widgetMethod, key: "kept", render: func(int) []string { return []string{"row"} }}
	ext.replaceSurfaceID(widgetRendererID("kept"), renderer)
	args, _ := json.Marshal(map[string]string{"message": stale})
	ext.handleNotify(envelope{Type: msgNotify, Notify: &notifyMsg{Method: "invalidate", Args: args}})
	for name, read := range map[string]func(){
		"UITheme":     func() { _ = ctx.UITheme() },
		"Width":       func() { _ = ctx.Width() },
		"Height":      func() { _ = ctx.Height() },
		"SessionName": func() { _ = ctx.SessionName() },
	} {
		func() {
			defer func() {
				if got := recover(); got == nil || !strings.Contains(toString(got), stale) {
					t.Errorf("%s after invalidate: recovered %v, want the stale message", name, got)
				}
			}()
			read()
		}()
	}
	for name, call := range map[string]func() error{
		"SetWidget lines":   func() error { return ctx.SetWidget("kept", []string{"x"}) },
		"SetWidget clear":   func() error { return ctx.SetWidget("kept", nil) },
		"SetWidget options": func() error { return ctx.SetWidget("kept", []string{"x"}, WidgetOptions{"placement": "belowEditor"}) },
		"SetWidget view":    func() error { return ctx.SetWidget("kept", kit.View{Root: kit.NewText("x", 0, 0)}) },
		"SetWidgetRenderer": func() error {
			return ctx.SetWidgetRenderer("kept", func(int) []string { return []string{"y"} })
		},
		"OnUpdate": func() error { return ctx.OnUpdate("x") },
	} {
		if err := call(); err == nil || err.Error() != stale {
			t.Errorf("%s after invalidate = %v, want the stale message", name, err)
		}
	}
	ext.surfaceMu.Lock()
	kept := ext.surfaces[widgetRendererID("kept")]
	ext.surfaceMu.Unlock()
	if kept != renderer {
		t.Fatal("a failed widget call retired or replaced the installed renderer")
	}
}

// The SDK renders a footer, header or widget again on its own goroutine after a width change. That read of the width is not the context's Width: a
// context the session replaced would panic there and end the process. The refresh still renders at the width the host reported, and the push fails with
// the host's error.
func TestStaleSurfaceRefreshReadsTheWidthWithoutPanicking(t *testing.T) {
	const stale = "This extension ctx is stale after session replacement or reload."
	client, server := net.Pipe()
	connection, host := newConn(client), newConn(server)
	connection.start()
	host.start()
	_ = server.Close()
	t.Cleanup(func() { _ = client.Close() })
	ext := New("stale-refresh")
	ext.conn = connection
	ext.width = 77
	widths := make(chan int, 4)
	ext.replaceSurface(footerMethod, &surfaceRenderer{method: footerMethod, render: func(width int) []string {
		widths <- width
		return []string{"row"}
	}})
	args, _ := json.Marshal(map[string]string{"message": stale})
	ext.handleNotify(envelope{Type: msgNotify, Notify: &notifyMsg{Method: "invalidate", Args: args}})
	ext.refreshSurfaces()
	select {
	case width := <-widths:
		if width != 77 {
			t.Fatalf("refresh rendered at %d, want the reported 77", width)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the refresh worker never rendered")
	}
	if err := (Context{ext: ext}).SetFooter([]string{"x"}); err == nil {
		t.Fatal("SetFooter on a stale context sent its rows")
	}
}
