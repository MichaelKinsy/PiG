//go:build !pig_strip_node_extensions

package subprocess

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Pi's sendCustomMessage with triggerTurn marks the Session busy before its first await (agent-session.ts:1895), and the code that sent
// the message keeps the loop until it returns or awaits, whichever code that is: a command or event handler, or a timer's or an I/O
// callback that runs after the handler returned. Each case below runs one such code in a Node extension while the host answers idle at
// every read (a run that already ended), so only the host's hold (Host.syncRuns) makes a read false, and reports its reads by naming the
// session.
//
// The host binds sendMessage to a function that fails when sendFails is set. Pi throws before it sets busy for every error the host
// can answer (the runtime not bound, the Session not created, the runner replaced by a reload: agent-session.ts:3341, loader.ts
// assertActive), so a failed send holds nothing.
func TestNodeHoldAfterATriggeringSendEndsWhereTheSendingCodeYields(t *testing.T) {
	const send = `pi.sendMessage({ customType: "note", content: "go", display: true }, { triggerTurn: true });`
	cases := []struct {
		name      string
		body      string
		sendFails bool
		want      string
	}{
		{
			// The handler awaits with no read in between: the hold ends at the await, whatever the order of the call and the report.
			name: "handler awaits with no read in between",
			body: send + `
    await Promise.resolve();
    pi.setSessionName(JSON.stringify({ later: ctx.isIdle() }));`,
			want: `{"later":true}`,
		},
		{
			name: "handler reads before and after its await",
			body: send + `
    const sync = ctx.isIdle();
    await Promise.resolve();
    pi.setSessionName(JSON.stringify({ sync, later: ctx.isIdle() }));`,
			want: `{"sync":false,"later":true}`,
		},
		{
			// The loop runs the callback after the handler returned: the callback holds the Session busy until it awaits, as the handler does.
			name: "timer callback after the handler returned",
			body: `setTimeout(async () => {
      ` + send + `
      const sync = ctx.isIdle();
      await Promise.resolve();
      pi.setSessionName(JSON.stringify({ sync, later: ctx.isIdle() }));
    }, 10);`,
			want: `{"sync":false,"later":true}`,
		},
		{
			name: "I/O callback after the handler returned",
			body: `const { readFile } = await import("node:fs");
    readFile(import.meta.filename, () => {
      ` + send + `
      const sync = ctx.isIdle();
      setImmediate(() => pi.setSessionName(JSON.stringify({ sync, later: ctx.isIdle() })));
    });`,
			want: `{"sync":false,"later":true}`,
		},
		{
			name:      "failed send holds nothing",
			body:      send + "\n    pi.setSessionName(JSON.stringify({ sync: ctx.isIdle() }));",
			sendFails: true,
			want:      `{"sync":true}`,
		},
		{
			// Pi's registerProvider and addAutocompleteProvider return nothing: the handler has not awaited.
			name: "registerProvider does not end the hold",
			body: send + `
    pi.registerProvider("anthropic", { baseUrl: "http://127.0.0.1:1" });
    pi.setSessionName(JSON.stringify({ sync: ctx.isIdle() }));`,
			want: `{"sync":false}`,
		},
	}
	for _, tc := range cases {
		for _, isolation := range []string{"isolated", ""} {
			name := tc.name + "/" + isolation
			if isolation == "" {
				name = tc.name + "/packed"
			}
			t.Run(name, func(t *testing.T) {
				root := t.TempDir()
				source := filepath.Join(root, "send-idle.mjs")
				script := "export default function(pi) {\n  pi.on(\"session_start\", async (_event, ctx) => {\n    " + tc.body + "\n  });\n}\n"
				if err := os.WriteFile(source, []byte(script), 0o600); err != nil {
					t.Fatal(err)
				}
				ctx := testbudget.Context(t)
				names := make(chan string, 4)
				h := newTestHost(t)
				h.SetMode("print")
				bridge := NewUIBridge(func() {})
				bridge.SetHostAction("isIdle", func() bool { return true })
				bridge.SetHostAction("sendMessage", func(extension.CustomMessageRef, SendMessageOptions) error {
					if tc.sendFails {
						return errors.New("the Session is not created")
					}
					return nil
				})
				bridge.SetHostAction("setSessionName", func(name string) error { names <- name; return nil })
				h.SetUIBridge(bridge)
				defer h.Shutdown("test done")
				loaded, errs := h.LoadAll(ctx, []ExtConfig{{Name: "send-idle", Source: source, Enabled: true, Isolation: isolation}})
				if len(errs) != 0 || len(loaded) != 1 {
					t.Fatalf("LoadAll = %d loaded, %v; want 1", len(loaded), errs)
				}
				handled := make(chan error, 1)
				go func() {
					_, err := loaded[0].Handlers["session_start"][0](map[string]any{"type": "session_start"}, ctx)
					handled <- err
				}()
				select {
				case got := <-names:
					if got != tc.want {
						t.Fatalf("the code read %s, want %s", got, tc.want)
					}
				case <-ctx.Done():
					t.Fatal("the code never reported its reads")
				}
				if err := <-handled; err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
