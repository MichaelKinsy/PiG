package codingagent

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// beforeUIHarness is an interactive mode whose owner loop is the goroutine that
// runs a slash command, with terminal input fed through keys and one
// in-process extension handling a session_before_* event.
type beforeUIHarness struct {
	m           *InteractiveMode
	keys        *io.PipeWriter
	events      chan any
	dialogReady chan struct{}
	userID      string
	other       string
}

// beforeUIHandle supplies the Session command boundary for these owner-loop tests.
// The real coding.Session and its complete lifecycle are exercised separately by TestInteractiveBuiltinSessionLifecycle.
type beforeUIHandle struct {
	*recordingCompactHandle
	runner    *inproc.Runner
	sm        *SessionManager
	onReplace func()
}

func (h *beforeUIHandle) ExtensionCommandActions() extension.CommandActions {
	replace := func(ctx context.Context, event extension.ExtensionEvent, prepare func() (*Session, error)) (extension.CancelledResult, error) {
		value, err := h.runner.Emit(ctx, event)
		if err != nil {
			return extension.CancelledResult{}, err
		}
		data, err := json.Marshal(value)
		if err != nil {
			return extension.CancelledResult{}, err
		}
		var result struct {
			Cancel bool `json:"cancel"`
		}
		if err := json.Unmarshal(data, &result); err != nil {
			return extension.CancelledResult{}, err
		}
		if result.Cancel {
			return extension.CancelledResult{Cancelled: true}, nil
		}
		next, err := prepare()
		if err == nil {
			h.ReplaceInner(next)
			if h.onReplace != nil {
				h.onReplace()
			}
		}
		return extension.CancelledResult{}, err
	}
	return extension.CommandActions{
		NewSessionContext: func(ctx context.Context, _ *extension.NewSessionOptions) (extension.CancelledResult, error) {
			return replace(ctx, extension.SessionBeforeSwitchEvent{Type: EventSessionBeforeSwitch, Reason: "new"}, func() (*Session, error) { return h.sm.Create("new-before-ui", "") })
		},
		SwitchSessionContext: func(ctx context.Context, path string, _ *extension.SwitchSessionOptions) (extension.CancelledResult, error) {
			return replace(ctx, extension.SessionBeforeSwitchEvent{Type: EventSessionBeforeSwitch, Reason: "resume", TargetSessionFile: path}, func() (*Session, error) { return h.sm.Load(path) })
		},
		ForkContext: func(ctx context.Context, id string, options *extension.ForkOptions) (extension.CancelledResult, error) {
			return replace(ctx, extension.SessionBeforeForkEvent{Type: EventSessionBeforeFork, EntryID: id, Position: options.Position}, func() (*Session, error) {
				if options.Position == "at" {
					if err := h.inner.CheckSavedForFork(); err != nil {
						return nil, err
					}
					return h.sm.Clone(h.inner, id)
				}
				next, _, err := h.sm.ForkToNewSession(h.inner, id)
				return next, err
			})
		},
	}
}

// bindReplacementTestHandle preserves the Session-owned command boundary in leaf UI tests.
func bindReplacementTestHandle(t *testing.T, m *InteractiveMode) {
	t.Helper()
	handle := &recordingCompactHandle{agent: m.agent}
	if previous, ok := m.opts.SessionHandle.(*recordingCompactHandle); ok {
		handle = previous
	}
	m.opts.SessionHandle = &beforeUIHandle{
		recordingCompactHandle: handle,
		runner:                 inproc.NewRunner(nil, m.opts.CWD),
		sm:                     NewSessionManagerWithDir(m.opts.CWD, m.opts.SessionDir),
		onReplace:              m.renderCurrentSessionState,
	}
}

// newBeforeUIHarness registers handle for eventType. handle receives the
// event and the mode's extension UI and returns the handler's result.
func newBeforeUIHarness(t *testing.T, eventType string, handle func(context.Context, *ExtUIContext, any) any) *beforeUIHarness {
	t.Helper()
	dir := t.TempDir()
	sm := NewSessionManagerWithDir(dir, dir)
	sess, err := sm.Create("before-ui", "")
	if err != nil {
		t.Fatal(err)
	}
	var userID string
	for _, msg := range []agent.AgentMessage{userMsg("question"), assistantMsg("", ai.TextContent{Text: "answer"})} {
		id, err := sess.AppendMessage(msg)
		if err != nil {
			t.Fatal(err)
		}
		if userID == "" {
			userID = id
		}
	}
	other, err := sm.Create("before-ui-other", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.AppendMessage(userMsg("elsewhere")); err != nil {
		t.Fatal(err)
	}
	loaded, err := NewSessionManagerWithDir(dir, dir).Load(sess.Path())
	if err != nil {
		t.Fatal(err)
	}

	sessionHandle := &beforeUIHandle{recordingCompactHandle: &recordingCompactHandle{inner: loaded}, sm: sm}
	m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: dir, SessionDir: dir, SessionHandle: sessionHandle})
	m.editor = tui.NewEditor()
	m.editorContainer = tui.NewContainer()
	m.editorContainer.Add(m.editor)
	m.chatContainer = tui.NewContainer()
	m.layout = tui.NewContainer(m.chatContainer, m.editorContainer)
	m.tuiInst = tui.NewWithOutput(io.Discard, 100, 30)
	m.installRenderDispatcher()
	t.Cleanup(m.tuiInst.Stop)
	m.tuiInst.Add(m.layout)
	m.statusLine = NewFooterComponent(nil, "", nil)

	h := &beforeUIHarness{m: m, events: make(chan any, 4), dialogReady: make(chan struct{}), userID: userID, other: other.Path()}
	ui := &ExtUIContext{m: m}
	m.newRunner = inproc.NewRunner([]extension.Extension{{Path: "before-ui", Handlers: map[string][]extension.HandlerFn{
		eventType: {func(args ...any) (any, error) {
			h.events <- args[0]
			ctx := extension.WithCallInitiation(m.runCtx, func() {
				m.runOnMain(m.runCtx, func() { close(h.dialogReady) })
			})
			return handle(ctx, ui, args[0]), nil
		}},
	}}}, dir)

	sessionHandle.runner = m.newRunner
	ctx, cancel := context.WithCancel(context.Background())
	m.runCtx = ctx
	reader, writer := io.Pipe()
	h.keys = writer
	m.startTerminalInput(ctx, reader)
	t.Cleanup(func() {
		_ = writer.Close()
		cancel()
		_ = m.stopTerminalInput()
		m.backgroundTasks.Wait()
	})
	return h
}

// run executes a slash handler on this goroutine as the owner loop does, and
// sends key once the handler's dialog can take it.
func (h *beforeUIHarness) run(t *testing.T, key string, handler func(*SlashContext) error, setup func(*SlashContext)) error {
	t.Helper()
	sc := h.m.buildSlashContext(h.m.runCtx)
	if setup != nil {
		setup(sc)
	}
	go func() { <-h.dialogReady; _, _ = io.WriteString(h.keys, key) }()
	done := make(chan error, 1)
	go func() { done <- handler(sc) }()
	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("the slash command did not return while its session_before handler waited on a dialog")
		return nil
	}
}

// event returns the event the handler received, failing when none was emitted.
func (h *beforeUIHarness) event(t *testing.T) any {
	t.Helper()
	select {
	case event := <-h.events:
		return event
	default:
		t.Fatal("the extension handler was not called")
		return nil
	}
}

func (h *beforeUIHarness) transcript() string {
	return widthx.StripAnsi(strings.Join(h.m.chatContainer.Render(100), "\n"))
}

// Pi 0.87.1 awaits session_before_switch with its event loop live
// (agent-session-runtime.ts:133-146, 226-235), so a handler's confirm shows and
// takes keys. A cancel result stops /new silently (handleClearCommand,
// interactive-mode.ts:6657-6666); otherwise the new session starts.
func TestSlashNewAwaitsSessionBeforeSwitchDialog(t *testing.T) {
	for _, tc := range []struct {
		name      string
		key       string
		cancelled bool
	}{
		{"escape cancels", "\x1b", true},
		{"yes replaces", "\r", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newBeforeUIHarness(t, EventSessionBeforeSwitch, func(ctx context.Context, ui *ExtUIContext, _ any) any {
				ok, _ := ui.Confirm(ctx, "Clear session?", "This will delete all messages in the current session.", extension.ExtensionUIDialogOptions{})
				return map[string]any{"cancel": !ok}
			})
			before := h.m.currentSession().Path()
			if err := h.run(t, tc.key, newHandler, nil); err != nil {
				t.Fatalf("/new = %v", err)
			}
			event, ok := h.event(t).(extension.SessionBeforeSwitchEvent)
			if !ok || event.Reason != "new" || event.TargetSessionFile != "" {
				t.Fatalf("event = %#v; want reason new with no target", event)
			}
			replaced := h.m.currentSession().Path() != before
			started := strings.Contains(h.transcript(), "New session started")
			if replaced == tc.cancelled || started == tc.cancelled {
				t.Fatalf("cancelled=%v: session replaced=%v, started line=%v", tc.cancelled, replaced, started)
			}
		})
	}
}

// Pi's fork emits session_before_fork with position "before" for /fork and
// "at" for /clone (agent-session-runtime.ts:153-164, 262-269;
// interactive-mode.ts:5351-5399). A cancel result leaves the session and shows
// nothing; otherwise the command reports its status.
func TestSlashForkAndCloneAwaitSessionBeforeForkDialog(t *testing.T) {
	for _, tc := range []struct {
		name      string
		handler   func(*SlashContext) error
		position  string
		key       string
		cancelled bool
		status    string
	}{
		{"fork escape cancels", forkHandler, "before", "\x1b", true, "Forked to new session"},
		{"fork yes forks", forkHandler, "before", "\r", false, "Forked to new session"},
		{"clone escape cancels", cloneHandler, "at", "\x1b", true, "Cloned to new session"},
		{"clone yes clones", cloneHandler, "at", "\r", false, "Cloned to new session"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newBeforeUIHarness(t, EventSessionBeforeFork, func(ctx context.Context, ui *ExtUIContext, event any) any {
				fork, _ := event.(extension.SessionBeforeForkEvent)
				choice, _ := ui.Select(ctx, "Fork from entry "+fork.EntryID+"?", []string{"Yes, create fork", "No, stay in current session"}, extension.ExtensionUIDialogOptions{})
				return &extension.SessionBeforeForkResult{Cancel: choice != "Yes, create fork"}
			})
			before := h.m.currentSession().Path()
			leaf := *h.m.currentSession().GetLeafID()
			var statuses []string
			err := h.run(t, tc.key, tc.handler, func(sc *SlashContext) {
				sc.Args = h.userID
				sc.ShowStatus = func(s string) { statuses = append(statuses, s) }
			})
			if err != nil {
				t.Fatalf("%s = %v", tc.name, err)
			}
			event, ok := h.event(t).(extension.SessionBeforeForkEvent)
			wantEntry := h.userID
			if tc.position == "at" {
				wantEntry = leaf
			}
			if !ok || event.EntryID != wantEntry || event.Position != tc.position {
				t.Fatalf("event = %#v; want entry %s at position %q", event, wantEntry, tc.position)
			}
			replaced := h.m.currentSession().Path() != before
			reported := strings.Contains(strings.Join(statuses, "\n"), tc.status)
			if replaced == tc.cancelled || reported == tc.cancelled {
				t.Fatalf("cancelled=%v: session replaced=%v, statuses=%q", tc.cancelled, replaced, statuses)
			}
		})
	}
}

// Pi's switchSession emits session_before_switch with reason "resume" and the
// target file before opening it; a cancel result keeps the current session and
// shows nothing (agent-session-runtime.ts:196-207, interactive-mode.ts
// handleResumeSession).
func TestSlashResumeAwaitsSessionBeforeSwitchDialog(t *testing.T) {
	for _, tc := range []struct {
		name      string
		key       string
		cancelled bool
	}{
		{"escape cancels", "\x1b", true},
		{"yes resumes", "\r", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newBeforeUIHarness(t, EventSessionBeforeSwitch, func(ctx context.Context, ui *ExtUIContext, _ any) any {
				ok, _ := ui.Confirm(ctx, "Switch session?", "You have messages in the current session. Switch anyway?", extension.ExtensionUIDialogOptions{})
				return map[string]any{"cancel": !ok}
			})
			before := h.m.currentSession().Path()
			var statuses []string
			err := h.run(t, tc.key, resumeHandler, func(sc *SlashContext) {
				sc.PickSession = func() (string, bool) { return h.other, true }
				sc.ShowStatus = func(s string) { statuses = append(statuses, s) }
			})
			if err != nil {
				t.Fatalf("/resume = %v", err)
			}
			event, ok := h.event(t).(extension.SessionBeforeSwitchEvent)
			if !ok || event.Reason != "resume" || event.TargetSessionFile != h.other {
				t.Fatalf("event = %#v; want reason resume targeting %s", event, h.other)
			}
			resumed := h.m.currentSession().Path() == h.other
			reported := strings.Contains(strings.Join(statuses, "\n"), "Resumed session")
			if resumed == tc.cancelled || reported == tc.cancelled || !tc.cancelled && h.m.currentSession().Path() == before {
				t.Fatalf("cancelled=%v: resumed=%v, statuses=%q", tc.cancelled, resumed, statuses)
			}
		})
	}
}
