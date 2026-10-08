package codingagent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
)

// modelSelectCountingRunner is an extension runner whose model_select handler counts its events.
func modelSelectCountingRunner(t *testing.T, events *[]any) *inproc.Runner {
	t.Helper()
	ext := extension.Extension{Path: "/ext/probe.ts", Handlers: map[string][]extension.HandlerFn{
		EventModelSelect: {func(args ...any) (any, error) {
			*events = append(*events, args[0])
			return nil, nil
		}},
	}}
	return inproc.NewRunner([]extension.Extension{ext}, t.TempDir())
}

// Pi's AgentSession emits model_select once per changed selection, from setModel (source "set") and cycleModel (source "cycle") alone (agent-session.ts:2372-2384, 2411, 2480, 2512); interactive mode only calls them (interactive-mode.ts handleModelCommand, cycleModel). Pig's Session emits the event with the full model when the mode calls it; the mode emitted a second event of its own, built by modelToExtModel from the model's id and display name, with no provider. The /model path therefore reached an extension twice, the second time as `undefined/undefined`.
func TestInteractiveModelCommandEmitsNoModelSelectOfItsOwn(t *testing.T) {
	m := modelPickerTestMode(t)
	handle := &recordingCompactHandle{agent: m.agent}
	m.opts.SessionHandle = handle
	var events []any
	m.newRunner = modelSelectCountingRunner(t, &events)

	if err := m.buildSlashContext(context.Background()).SwitchModel("capture/next"); err != nil {
		t.Fatal(err)
	}
	if len(handle.switched) != 1 || handle.switched[0].ID != "capture/next" || len(handle.cycled) != 0 {
		t.Fatalf("Session got SetModel %v and CycleModel %v, want one SetModel of capture/next", handle.switched, handle.cycled)
	}
	if len(events) != 0 {
		t.Fatalf("interactive mode emitted model_select %v itself; the Session owns the event", events)
	}
}

// Cycling a model reaches the Session as a cycle (agent-session.ts:2480, 2512), so the Session's one model_select carries source "cycle". Pig's cycle called SetModel, whose event says source "set", and then added its own event.
func TestInteractiveModelCycleReachesTheSessionAsACycle(t *testing.T) {
	m := modelPickerTestMode(t)
	next := &ai.Model{ID: "next", ProviderMeta: ai.ProviderMetadata{ProviderID: "capture"}}
	handle := &recordingCompactHandle{agent: m.agent, cycleResults: []*ModelCycleResult{{Model: next}}}
	m.opts.SessionHandle = handle
	var events []any
	m.newRunner = modelSelectCountingRunner(t, &events)

	m.cycleModel(true)
	if len(handle.cycled) != 1 || len(handle.switched) != 0 {
		t.Fatalf("Session got CycleModel %v and SetModel %v, want one CycleModel", handle.cycled, handle.switched)
	}
	if len(events) != 0 {
		t.Fatalf("interactive mode emitted model_select %v itself; the Session owns the event", events)
	}
}

// The extension pi.setModel action already leaves the event to the Session (agent-session.ts:3343-3347); it must stay that way.
func TestInteractiveExtensionSetModelEmitsNoModelSelectOfItsOwn(t *testing.T) {
	m := modelPickerTestMode(t)
	handle := &recordingCompactHandle{agent: m.agent}
	m.opts.SessionHandle = handle
	m.uiTaskCh = make(chan func(), 4)
	m.editor = tui.NewEditor()
	bridge := &captureUIBridge{}
	m.opts.SubprocessUIBridge = bridge
	var events []any
	m.newRunner = modelSelectCountingRunner(t, &events)
	detach := m.wireSubprocessHostCallbacks()
	defer detach()

	if ok, err := bridge.actions["setModel"].(func(context.Context, string) (bool, error))(t.Context(), "capture/next"); err != nil || !ok {
		t.Fatalf("setModel = %v, %v", ok, err)
	}
	if len(events) != 0 {
		t.Fatalf("interactive mode emitted model_select %v itself", events)
	}
}

// failingCompactHandle fails every compaction the mode starts itself.
type failingCompactHandle struct{ *recordingCompactHandle }

func (*failingCompactHandle) Compact(context.Context, string) error {
	return errors.New("Nothing to compact (session too small)")
}

func (*failingCompactHandle) CompactForExtension(context.Context, string) (any, error) {
	return nil, errors.New("Nothing to compact (session too small)")
}

// Pi's ctx.compact() calls the Session's compact and drops the outcome when the extension passes no callbacks (agent-session.ts:3369-3379). Interactive mode started its own goroutine that wrote a failed compaction to stderr, which corrupts the screen in the TUI, and used a different context from every other mode. It now hands the options to the Session, which owns the task, its cancellation and its join.
func TestInteractiveExtensionCompactWithoutCallbacksIsTheSessionsAndSilent(t *testing.T) {
	m := modelPickerTestMode(t)
	handle := &failingCompactHandle{recordingCompactHandle: &recordingCompactHandle{agent: m.agent}}
	m.opts.SessionHandle = handle
	m.uiTaskCh = make(chan func(), 4)
	m.editor = tui.NewEditor()
	bridge := &captureUIBridge{}
	m.opts.SubprocessUIBridge = bridge
	detach := m.wireSubprocessHostCallbacks()
	defer detach()
	compact := bridge.actions["compact"].(func(context.Context, *extension.CompactOptions))

	restore := withCapturedStderr(t)
	compact(t.Context(), &extension.CompactOptions{CustomInstructions: "keep todos"})
	compact(t.Context(), nil)
	time.Sleep(200 * time.Millisecond)
	if stderr := restore(); stderr != "" {
		t.Fatalf("compact without callbacks wrote %q to stderr", stderr)
	}
	if len(handle.compacted) != 2 || handle.compacted[0] == nil || handle.compacted[0].CustomInstructions != "keep todos" {
		t.Fatalf("Session got ExtensionCompact %+v, want both calls with their options", handle.compacted)
	}
	if handle.compactCount != 0 {
		t.Fatalf("compact bypassed ExtensionCompact through Compact %d times", handle.compactCount)
	}
}

// The callbacks reach the Session unchanged, so the result and the failure come back exactly as in every other mode.
func TestInteractiveExtensionCompactPassesCallbacksToTheSession(t *testing.T) {
	m := modelPickerTestMode(t)
	handle := &recordingCompactHandle{agent: m.agent}
	m.opts.SessionHandle = handle
	m.uiTaskCh = make(chan func(), 4)
	m.editor = tui.NewEditor()
	bridge := &captureUIBridge{}
	m.opts.SubprocessUIBridge = bridge
	detach := m.wireSubprocessHostCallbacks()
	defer detach()
	compact := bridge.actions["compact"].(func(context.Context, *extension.CompactOptions))

	var completed, failed int
	options := &extension.CompactOptions{OnComplete: func(extension.CompactionResult) { completed++ }, OnError: func(error) { failed++ }}
	compact(t.Context(), options)
	if len(handle.compacted) != 1 || handle.compacted[0] != options {
		t.Fatalf("Session got %+v, want the extension's own options", handle.compacted)
	}
	handle.compacted[0].OnComplete(nil)
	handle.compacted[0].OnError(nil)
	if completed != 1 || failed != 1 {
		t.Fatalf("callbacks ran %d and %d times", completed, failed)
	}
}

// A mode without a Session reports the failure to onError, as before.
func TestInteractiveExtensionCompactWithoutASessionReportsToOnError(t *testing.T) {
	m := modelPickerTestMode(t)
	m.uiTaskCh = make(chan func(), 4)
	m.editor = tui.NewEditor()
	bridge := &captureUIBridge{}
	m.opts.SubprocessUIBridge = bridge
	detach := m.wireSubprocessHostCallbacks()
	defer detach()
	compact := bridge.actions["compact"].(func(context.Context, *extension.CompactOptions))

	var got error
	compact(t.Context(), &extension.CompactOptions{OnError: func(err error) { got = err }})
	if got == nil || !strings.Contains(got.Error(), "compaction is not available") {
		t.Fatalf("onError = %v", got)
	}
	compact(t.Context(), nil)
}
