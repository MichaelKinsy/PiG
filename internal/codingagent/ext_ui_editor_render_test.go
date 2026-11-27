package codingagent

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi 8c1afaae5 (#12349): interactive-mode.ts:2855-2862 pasteToEditor and
// setEditorText call ui.requestRender() so text set outside terminal input
// handling (e.g. after an await or timer) repaints without waiting for another
// key press. ExtUIContext.PasteToEditor and ExtUIContext.SetEditorText marshal
// editor mutations onto the owner loop (runOnMainAndWait) and call
// m.requestRender() so coalescing matches Pi and tui.Editor is never mutated
// off-loop.
func TestExtUIEditorCallsRepaintWhileIdle(t *testing.T) {
	cases := []struct {
		name   string
		method string
		text   string
	}{
		{name: "setEditorText", method: "ui.setEditorText", text: "SET TEXT FROM TIMER"},
		{name: "pasteToEditor", method: "ui.pasteToEditor", text: "Hello from extension"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			var out bytes.Buffer
			editor := tui.NewEditor()
			tuiInst := tui.NewWithOutput(&out, 80, 24)
			tuiInst.AddChild(editor)
			scheduled := make(chan func(), 4)
			tuiInst.SetRenderDispatcher(func(render func()) {
				scheduled <- render
			})
			t.Cleanup(tuiInst.CancelPendingRender)

			tuiInst.Render()
			out.Reset()

			m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: t.TempDir()})
			m.runCtx = ctx
			m.editor = editor
			m.tuiInst = tuiInst
			m.bindEditorSnapshot()

			bridge := subprocess.NewUIBridge(func() {})
			extUI := &ExtUIContext{m: m}
			bridge.SetUIContext(extUI)

			args, err := json.Marshal(map[string]string{"text": tc.text})
			if err != nil {
				t.Fatalf("marshal args: %v", err)
			}

			// Run HandleCall off-loop, as subprocess host_calls.go drain() does.
			type callOutcome struct {
				res *subprocess.CallResultPayload
				err error
			}
			done := make(chan callOutcome, 1)
			go func() {
				res, err := bridge.HandleCall("ext", &subprocess.CallPayload{
					Method: tc.method,
					Args:   args,
				})
				done <- callOutcome{res: res, err: err}
			}()

			// Owner loop drains the queued editor task (like extension_notify_render_test.go).
			select {
			case fn := <-m.uiTaskCh:
				fn()
			case <-t.Context().Done():
				t.Fatalf("%s did not post its editor update to uiTaskCh", tc.method)
			}

			outcome := <-done
			if outcome.err != nil {
				t.Fatalf("HandleCall(%s): %v", tc.method, outcome.err)
			}
			if outcome.res != nil && outcome.res.Error != nil {
				t.Fatalf("HandleCall(%s) returned error: %+v", tc.method, outcome.res.Error)
			}

			// A following getEditorText on the same lane sees the updated text immediately.
			if got := extUI.GetEditorText(); got != tc.text {
				t.Fatalf("GetEditorText() after %s = %q, want %q", tc.method, got, tc.text)
			}

			// No synchronous frame should have been painted off-loop; a scheduled render must have been requested.
			select {
			case render := <-scheduled:
				render()
			case <-t.Context().Done():
				t.Fatalf("%s did not request a render via requestRender()", tc.method)
			}

			if got := out.String(); !strings.Contains(got, tc.text) {
				t.Fatalf("%s scheduled render did not paint text; output=%q, want substring %q", tc.method, got, tc.text)
			}
		})
	}
}

// Pi 8c1afaae5 commit message: verified also with a custom editor component set
// via setEditorComponent. Verify setEditorText and pasteToEditor forward to the
// custom editor on the owner loop, request a render while idle, and paint the
// updated custom editor frame once the owner loop drains its posted events.
func TestExtUIEditorCallsRepaintCustomEditorWhileIdle(t *testing.T) {
	cases := []struct {
		name     string
		method   string
		text     string
		wantText string
	}{
		{name: "setEditorText", method: "ui.setEditorText", text: "CUSTOM SET", wantText: "CUSTOM SET"},
		{name: "pasteToEditor", method: "ui.pasteToEditor", text: "CUSTOM PASTE", wantText: "\x1b[200~CUSTOM PASTE\x1b[201~"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			var out bytes.Buffer
			m, _ := newCustomEditorDispatchMode(t)
			m.runCtx = ctx
			tuiInst := tui.NewWithOutput(&out, 80, 24)
			tuiInst.AddChild(m.editor)
			scheduled := make(chan func(), 4)
			tuiInst.SetRenderDispatcher(func(render func()) {
				scheduled <- render
			})
			t.Cleanup(tuiInst.CancelPendingRender)
			m.tuiInst = tuiInst

			component := &factoryEditor{}
			extUI := &ExtUIContext{m: m}
			extUI.SetEditorComponent(func(tui.TUI, tui.EditorTheme, extension.KeybindingsManager) tui.EditorComponent {
				return component
			})
			runPostedTasks(t, m)
			if !m.editor.IsRemote() {
				t.Fatal("custom editor component did not attach")
			}

			// Initial paint, then idle (cancel any pending scheduled render from attach).
			tuiInst.CancelPendingRender()
			for len(scheduled) > 0 {
				<-scheduled
			}
			tuiInst.Render()
			out.Reset()

			bridge := subprocess.NewUIBridge(func() {})
			bridge.SetUIContext(extUI)

			args, err := json.Marshal(map[string]string{"text": tc.text})
			if err != nil {
				t.Fatalf("marshal args: %v", err)
			}

			done := make(chan error, 1)
			go func() {
				res, err := bridge.HandleCall("ext", &subprocess.CallPayload{
					Method: tc.method,
					Args:   args,
				})
				if err == nil && res != nil && res.Error != nil {
					t.Errorf("HandleCall(%s) error: %+v", tc.method, res.Error)
				}
				done <- err
			}()

			// Drain posted owner-loop tasks (including any background remoteEditorEvents
			// drain posted by SetEditorComponent) until HandleCall completes.
			for {
				select {
				case fn := <-m.uiTaskCh:
					fn()
				case err := <-done:
					if err != nil {
						t.Fatalf("HandleCall(%s): %v", tc.method, err)
					}
					goto handled
				case <-t.Context().Done():
					t.Fatalf("%s did not post its custom-editor update to uiTaskCh", tc.method)
				}
			}
		handled:
			if component.text != tc.wantText {
				t.Fatalf("custom editor text = %q, want %q", component.text, tc.wantText)
			}

			// Drain any remaining posted remoteEditorEvents (EditorFrame calls
			// tuiInst.Render() directly, which paints and cancels the throttled
			// timer if it ran before the timer fired) and run any scheduled render
			// that did fire via requestRender().
			runPostedTasks(t, m)
			if !strings.Contains(out.String(), "factory-editor:"+tc.wantText) {
				select {
				case render := <-scheduled:
					runPostedTasks(t, m)
					render()
				case <-t.Context().Done():
					t.Fatalf("%s neither painted via EditorFrame nor requested a render for custom editor", tc.method)
				}
			}

			if got := out.String(); !strings.Contains(got, "factory-editor:"+tc.wantText) {
				t.Fatalf("%s custom editor render did not paint updated text; output=%q", tc.method, got)
			}
		})
	}
}
