package subprocess

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
	"github.com/MichaelKinsy/PiG/tui"
)

// frameProbePart is a component of the renderer whose Render is a function, so a test can make the calls an extension makes from a component's render.
type frameProbePart struct{ render func(width int) []string }

func (p *frameProbePart) Render(width int) []string { return p.render(width) }
func (p *frameProbePart) Invalidate()               {}

// returnsWithinBudget fails t when fn has not returned within the hang budget; a deadlocked fn stays blocked and the test reports it instead of hanging.
func returnsWithinBudget(t *testing.T, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(testbudget.Wait(t)):
		t.Fatalf("%s did not return", what)
	}
}

// Every call an extension makes from a component's render runs on the goroutine that runs the frame, which holds the frame lock (mu): a member that takes
// that lock again waits for the frame, which waits for the extension, which waits for the call. TestTuiAltScreenFrameReadsAcrossSDKs and
// TestTuiAltScreenFrameReentryAcrossSDKs hung every SDK's first row on `state` and `scrollBy` after the viewport accessors took mu (7db4e68fc4).
func TestTuiAltCallsFromARenderDoNotTakeTheFrameLockAgain(t *testing.T) {
	const rejected = "cannot run while a component renders"
	calls := []struct{ op, call, wantErr string }{
		{"state", `{"op":"state"}`, ""},
		{"scrollBy", `{"op":"scrollBy","lines":-1}`, ""},
		{"scrollToTop", `{"op":"scrollToTop"}`, ""},
		{"scrollToBottom", `{"op":"scrollToBottom"}`, ""},
		{"getScreenLines", `{"op":"getScreenLines"}`, ""},
		{"requestRender", `{"op":"requestRender"}`, ""},
		{"invalidate", `{"op":"invalidate"}`, ""},
		{"flash", `{"op":"flash","message":"hi","durationMs":50}`, ""},
		{"setWheelScrollLines", `{"op":"setWheelScrollLines","lines":2}`, ""},
		{"setCopyOnSelect", `{"op":"setCopyOnSelect","enabled":true}`, ""},
		{"setShowHardwareCursor", `{"op":"setShowHardwareCursor","enabled":true}`, ""},
		{"setClearOnShrink", `{"op":"setClearOnShrink","enabled":true}`, ""},
		{"resetTextSelection", `{"op":"resetTextSelection"}`, ""},
		{"setOnDebug", `{"op":"setOnDebug","enabled":false}`, ""},
		{"hideOverlay", `{"op":"hideOverlay"}`, ""},
		{"addInputListener", `{"op":"addInputListener","listenerId":"l1"}`, ""},
		{"removeInputListener", `{"op":"removeInputListener","listenerId":"l1"}`, ""},
		{"setTerminalColorSchemeNotifications", `{"op":"setTerminalColorSchemeNotifications","enabled":false}`, ""},
		{"handleMouse", `{"op":"handleMouse","event":{"type":"wheel","button":"none","width":20,"height":6,"wheelDelta":1}}`, ""},
		{"resize", `{"op":"resize"}`, ""},
		{"render", `{"op":"render","width":10}`, rejected},
		{"renderNow", `{"op":"renderNow"}`, rejected},
		{"start", `{"op":"start"}`, rejected},
		{"stop", `{"op":"stop"}`, rejected},
		{"copyActiveSelectionToClipboard", `{"op":"copyActiveSelectionToClipboard"}`, rejected},
		{"queryTerminalColors", `{"op":"queryTerminalColors"}`, rejected},
		{"input", `{"op":"input","data":"x"}`, rejected},
	}
	rows := []string{"one", "two", "three", "four", "five", "six", "seven", "eight"}
	for _, tc := range calls {
		t.Run(tc.op, func(t *testing.T) {
			var out bytes.Buffer
			screen := tui.NewTuiAltScreenWithOutput(&out, 20, 4, tui.TuiAltScreenOptions{})
			screen.SetRenderDispatcher(func(func()) {})
			screen.EnableFrameAccess()
			s := &tuiAltScreenState{
				id: "s", screen: screen,
				components: map[string]*tuiAltComponent{}, byComp: map[tui.Component]string{},
				overlays: map[string]*tui.OverlayHandle{}, listeners: map[string]*tui.TuiInputListener{},
			}
			var call tuiAltCall
			if err := json.Unmarshal([]byte(tc.call), &call); err != nil {
				t.Fatal(err)
			}
			var (
				result *CallResultPayload
				callEr error
				ran    bool
				armed  bool
			)
			screen.Add(&frameProbePart{render: func(int) []string {
				if !armed {
					return rows
				}
				armed = false
				frame, ok := screen.Frame()
				if !ok {
					t.Error("render is not the frame")
					return rows
				}
				result, callEr = s.run(&call, frame, true)
				ran = true
				return rows
			}})
			screen.Start()
			armed = true
			// Stop waits for the frame, so it follows the bounded wait and a call that deadlocked the frame fails the test instead of hanging its cleanup.
			returnsWithinBudget(t, "a "+tc.op+" call made from a component's render", screen.Render)
			screen.StopWithOptions(tui.StopOptions{PreserveScreen: true})
			if !ran {
				t.Fatal("the render did not make the call")
			}
			switch {
			case tc.wantErr != "":
				if callEr == nil || !strings.Contains(callEr.Error(), tc.wantErr) {
					t.Fatalf("%s from a render: err = %v, want one that says %q", tc.op, callEr, tc.wantErr)
				}
			case callEr != nil:
				t.Fatalf("%s from a render: %v", tc.op, callEr)
			case result == nil:
				t.Fatalf("%s from a render returned no result", tc.op)
			}
			if tc.op == "state" {
				var state map[string]any
				if err := json.Unmarshal(result.Result, &state); err != nil {
					t.Fatal(err)
				}
				if _, ok := state["viewportTop"]; !ok {
					t.Errorf("state = %s, want a viewportTop", result.Result)
				}
				if following, ok := state["isFollowingOutput"].(bool); !ok || !following {
					t.Errorf("state = %s, want a screen that follows its output", result.Result)
				}
			}
		})
	}
}
