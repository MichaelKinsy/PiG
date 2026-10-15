package codingagent

// pi: packages/coding-agent/src/modes/interactive/program-status-reporter.ts

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// programStatusTerminal records the OSC 7501 reports of program-status-reporter.test.ts setup().
type programStatusTerminal struct {
	tui.Terminal
	reports []tui.ProgramStatus
}

func (t *programStatusTerminal) SetProgramStatus(status tui.ProgramStatus) {
	t.reports = append(t.reports, status)
}

type programStatusHarness struct {
	t        *testing.T
	terminal *programStatusTerminal
	name     string
	reporter *ProgramStatusReporter
}

func newProgramStatusHarness(t *testing.T, sessionName string) *programStatusHarness {
	h := &programStatusHarness{t: t, terminal: &programStatusTerminal{}, name: sessionName}
	h.reporter = NewProgramStatusReporter(func() tui.Terminal { return h.terminal }, func() string { return h.name })
	return h
}

func (h *programStatusHarness) send(events ...agent.AgentEvent) {
	for _, event := range events {
		h.reporter.HandleEvent(event)
	}
}

// last is the last report without its app name.
func (h *programStatusHarness) last(want tui.ProgramStatus) {
	h.t.Helper()
	if len(h.terminal.reports) == 0 {
		h.t.Fatal("nothing was reported")
	}
	got := h.terminal.reports[len(h.terminal.reports)-1]
	if got.App != AppName {
		h.t.Fatalf("app %q, want %q", got.App, AppName)
	}
	got.App = ""
	if got != want {
		h.t.Fatalf("last report %+v, want %+v", got, want)
	}
}

func assistantEnd(stopReason ai.StopReason, errorMessage string) agent.AgentEvent {
	return agent.MessageEndEvent{Message: agent.AgentMessage{Assistant: &agent.AssistantMessage{StopReason: stopReason, ErrorMessage: errorMessage, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "secret assistant output"}}}}}
}

var (
	settledRun     = agent.AgentSettledEvent{}
	abortedSettled = agent.AgentSettledEvent{Aborted: true}
)

func compactionEnd(reason string, aborted bool, errorMessage string) agent.AgentEvent {
	return agent.CompactionEndEvent{Reason: reason, Aborted: aborted, ErrorMessage: errorMessage}
}

func status(state tui.ProgramState, message string) tui.ProgramStatus {
	return tui.ProgramStatus{State: state, Message: message}
}

// program-status-reporter.test.ts "reports idle, working during a run, and done once it settles".
func TestProgramStatusReporterReportsIdleWorkingAndDone(t *testing.T) {
	h := newProgramStatusHarness(t, "Fix login")
	h.reporter.Report()
	h.last(status(tui.ProgramStateIdle, ""))
	h.send(agent.AgentStartEvent{})
	h.last(status(tui.ProgramStateWorking, "Fix login"))
	h.send(assistantEnd(ai.StopReasonToolUse, ""), assistantEnd(ai.StopReasonStop, ""))
	h.last(status(tui.ProgramStateWorking, "Fix login"))
	h.send(settledRun)
	h.last(status(tui.ProgramStateDone, "Fix login"))
	for _, report := range h.terminal.reports {
		if strings.Contains(report.Message, "secret assistant output") {
			t.Fatal("assistant output was reported")
		}
	}
}

// "reports only the outcome of the run: retried errors, final errors, and aborts".
func TestProgramStatusReporterReportsOnlyTheRunOutcome(t *testing.T) {
	h := newProgramStatusHarness(t, "")
	h.send(agent.AgentStartEvent{}, assistantEnd(ai.StopReasonError, "overloaded"), assistantEnd(ai.StopReasonStop, ""), settledRun)
	h.last(status(tui.ProgramStateDone, ""))
	h.send(agent.AgentStartEvent{}, assistantEnd(ai.StopReasonError, "Invalid API key\n{details}"), settledRun)
	h.last(status(tui.ProgramStateError, "Invalid API key"))
	h.send(agent.AgentStartEvent{}, assistantEnd(ai.StopReasonAborted, ""), abortedSettled)
	h.last(status(tui.ProgramStateIdle, ""))
	h.send(agent.AgentStartEvent{}, assistantEnd(ai.StopReasonStop, ""), abortedSettled)
	h.last(status(tui.ProgramStateIdle, ""))
}

// "reports a failed recovery compaction as the run's error unless a later response succeeds".
func TestProgramStatusReporterReportsAFailedRecoveryCompaction(t *testing.T) {
	h := newProgramStatusHarness(t, "")
	h.send(agent.AgentStartEvent{}, assistantEnd(ai.StopReasonLength, ""), agent.CompactionStartEvent{Reason: "overflow"}, compactionEnd("overflow", false, "Compaction failed\nstack"), settledRun)
	h.last(status(tui.ProgramStateError, "Compaction failed"))
	h.send(agent.AgentStartEvent{}, agent.CompactionStartEvent{Reason: "threshold"}, compactionEnd("threshold", false, "Compaction failed"), assistantEnd(ai.StopReasonStop, ""), settledRun)
	h.last(status(tui.ProgramStateDone, ""))
}

// program-status-reporter.ts:54-57, a branch the upstream file has no case for: an aborted recovery compaction ends the run idle, ahead of its error
// message, unless a later response succeeds.
func TestProgramStatusReporterReportsAnAbortedRecoveryCompactionAsIdle(t *testing.T) {
	h := newProgramStatusHarness(t, "Session")
	h.send(agent.AgentStartEvent{}, assistantEnd(ai.StopReasonLength, ""), agent.CompactionStartEvent{Reason: "overflow"}, compactionEnd("overflow", true, "Compaction aborted"), settledRun)
	h.last(status(tui.ProgramStateIdle, ""))
	h.send(agent.AgentStartEvent{}, agent.CompactionStartEvent{Reason: "threshold"}, compactionEnd("threshold", true, ""), assistantEnd(ai.StopReasonStop, ""), settledRun)
	h.last(status(tui.ProgramStateDone, "Session"))
}

// "reports compaction inside a run and the result of a manual compaction".
func TestProgramStatusReporterReportsCompaction(t *testing.T) {
	h := newProgramStatusHarness(t, "Session")
	h.send(agent.AgentStartEvent{}, agent.CompactionStartEvent{Reason: "threshold"})
	h.last(status(tui.ProgramStateWorking, "Compacting context"))
	h.send(compactionEnd("threshold", false, ""))
	h.last(status(tui.ProgramStateWorking, "Session"))
	h.send(assistantEnd(ai.StopReasonStop, ""), settledRun)

	h.send(agent.CompactionStartEvent{Reason: "manual"}, compactionEnd("manual", false, ""))
	h.last(status(tui.ProgramStateDone, "Session"))
	h.send(agent.CompactionStartEvent{Reason: "manual"}, compactionEnd("manual", false, "No model"))
	h.last(status(tui.ProgramStateError, "No model"))
	h.send(agent.CompactionStartEvent{Reason: "manual"}, compactionEnd("manual", true, ""))
	h.last(status(tui.ProgramStateIdle, ""))
}

// "reports the most recent open dialog and the underlying state once all close".
func TestProgramStatusReporterReportsTheMostRecentDialog(t *testing.T) {
	h := newProgramStatusHarness(t, "")
	h.send(agent.AgentStartEvent{})
	h.reporter.SetBlocked("extension-selector", &BlockedStatus{Kind: tui.ProgramStatusKindPermission, Message: "Allow bash?"})
	h.reporter.SetBlocked("login", &BlockedStatus{Kind: tui.ProgramStatusKindAuth, Message: "Log in to Anthropic"})
	h.last(tui.ProgramStatus{State: tui.ProgramStateBlocked, Kind: tui.ProgramStatusKindAuth, Message: "Log in to Anthropic"})

	h.reporter.SetBlocked("login", nil)
	h.send(assistantEnd(ai.StopReasonStop, ""), settledRun)
	h.last(tui.ProgramStatus{State: tui.ProgramStateBlocked, Kind: tui.ProgramStatusKindPermission, Message: "Allow bash?"})

	h.reporter.SetBlocked("extension-selector", &BlockedStatus{Kind: tui.ProgramStatusKindQuestion, Message: "Pick one"})
	h.last(tui.ProgramStatus{State: tui.ProgramStateBlocked, Kind: tui.ProgramStatusKindQuestion, Message: "Pick one"})
	h.reporter.SetBlocked("extension-selector", nil)
	h.last(status(tui.ProgramStateDone, ""))
}

// "sends each status once and follows session name changes".
func TestProgramStatusReporterSendsEachStatusOnce(t *testing.T) {
	h := newProgramStatusHarness(t, "Old")
	h.send(agent.AgentStartEvent{}, agent.TurnStartEvent{}, assistantEnd(ai.StopReasonToolUse, ""))
	h.reporter.Report()
	if len(h.terminal.reports) != 1 {
		t.Fatalf("%d reports, want 1", len(h.terminal.reports))
	}
	h.name = "New"
	h.send(agent.SessionInfoChangedEvent{Name: "New"})
	h.last(status(tui.ProgramStateWorking, "New"))
}

// "returns to idle when the session is replaced".
func TestProgramStatusReporterReturnsToIdleWhenTheSessionIsReplaced(t *testing.T) {
	h := newProgramStatusHarness(t, "")
	h.send(agent.AgentStartEvent{}, assistantEnd(ai.StopReasonStop, ""), settledRun)
	h.reporter.Reset()
	h.last(status(tui.ProgramStateIdle, ""))
}

func recordProgramStatus(m *InteractiveMode, name string) *programStatusTerminal {
	terminal := &programStatusTerminal{}
	m.programStatus = NewProgramStatusReporter(func() tui.Terminal { return terminal }, func() string { return name })
	return terminal
}

// interactive-mode.ts:3412 forwards every agent-session event to the reporter before it renders the event.
func TestInteractiveModeForwardsAgentEventsToTheProgramStatusReporter(t *testing.T) {
	m := statusBorderMode(t, true)
	terminal := recordProgramStatus(m, "Run")
	m.handleAgentEvent(agent.AgentStartEvent{})
	m.handleAgentEvent(agent.AgentEndEvent{})
	m.handleAgentEvent(agent.AgentSettledEvent{})
	got := []tui.ProgramState{}
	for _, report := range terminal.reports {
		got = append(got, report.State)
	}
	if len(got) != 2 || got[0] != tui.ProgramStateWorking || got[1] != tui.ProgramStateDone {
		t.Fatalf("reported states %v, want [working done]", got)
	}
}

// interactive-mode.ts:2729, :2742, :2755, :2810, :2854: an extension dialog reports blocked while it is open (a confirmation as permission, the others as question, each titled by the dialog) and the resting state once it closes.
func TestExtensionDialogsReportBlockedWhileTheyAreOpen(t *testing.T) {
	m, _ := newExtensionDialogProbe(t)
	terminal := recordProgramStatus(m, "")
	ui := &ExtUIContext{m: m}
	ctx := context.Background()
	opts := extension.ExtensionUIDialogOptions{}
	for _, tc := range []struct {
		name  string
		call  func() (string, error)
		input []string
		want  tui.ProgramStatus
	}{
		{"select", func() (string, error) { return ui.Select(ctx, "Pick one", []string{"a"}, opts) }, []string{"\r"}, tui.ProgramStatus{State: tui.ProgramStateBlocked, Kind: tui.ProgramStatusKindQuestion, Message: "Pick one"}},
		{"confirm", func() (string, error) {
			ok, err := ui.Confirm(ctx, "Allow bash?", "rm -rf", opts)
			return fmt.Sprint(ok), err
		}, []string{"\r"}, tui.ProgramStatus{State: tui.ProgramStateBlocked, Kind: tui.ProgramStatusKindPermission, Message: "Allow bash?"}},
		{"input", func() (string, error) { return ui.Input(ctx, "Your name", "name", opts) }, []string{"x", "\r"}, tui.ProgramStatus{State: tui.ProgramStateBlocked, Kind: tui.ProgramStatusKindQuestion, Message: "Your name"}},
		{"editor", func() (string, error) { return ui.Editor(ctx, "Edit note", "") }, []string{"x", "\r"}, tui.ProgramStatus{State: tui.ProgramStateBlocked, Kind: tui.ProgramStatusKindQuestion, Message: "Edit note"}},
	} {
		before := len(terminal.reports)
		if _, err := runExtensionDialogProbe(t, m, tc.call, tc.input); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		reports := terminal.reports[before:]
		if len(reports) != 2 {
			t.Fatalf("%s: %d reports %+v, want blocked then idle", tc.name, len(reports), reports)
		}
		got := reports[0]
		got.App = ""
		if got != tc.want || reports[1].State != tui.ProgramStateIdle {
			t.Fatalf("%s: reports %+v, want %+v then idle", tc.name, reports, tc.want)
		}
	}
}

// interactive-mode.ts:6321, :6334: a login reports blocked on auth, titled by the provider, until it ends.
func TestLoginReportsBlockedOnAuthUntilItEnds(t *testing.T) {
	m := statusBorderMode(t, true)
	terminal := recordProgramStatus(m, "")
	end := m.reportLoginBlocked("Anthropic")
	end()
	if len(terminal.reports) != 2 {
		t.Fatalf("%d reports %+v, want blocked then idle", len(terminal.reports), terminal.reports)
	}
	got := terminal.reports[0]
	if got.State != tui.ProgramStateBlocked || got.Kind != tui.ProgramStatusKindAuth || got.Message != "Log in to Anthropic" || terminal.reports[1].State != tui.ProgramStateIdle {
		t.Fatalf("reports %+v", terminal.reports)
	}
}

// A dialog's first line only: PiG's firstLine is upstream's `text?.split(/\r?\n/, 1)[0]?.trim() || "Error"`.
func TestProgramStatusFirstLine(t *testing.T) {
	for text, want := range map[string]string{
		"": "Error", "\n": "Error", "  \r\nrest": "Error", "Invalid key\r\nbody": "Invalid key", "  spaced  \nx": "spaced", "a\rb\nc": "a\rb",
	} {
		if got := firstLine(text); got != want {
			t.Errorf("firstLine(%q) = %q, want %q", text, got, want)
		}
	}
}

// interactive-mode.ts:2854, :2866: the editor a slash command opens (/tree custom summarization, /bug) is the extension
// editor, so it reports blocked as a question while it is open and idle once it closes.
func TestSlashCommandEditorReportsBlockedWhileItIsOpen(t *testing.T) {
	m, _ := newExtensionDialogProbe(t)
	terminal := recordProgramStatus(m, "")
	slash := m.buildSlashContext(context.Background())
	input, release := m.acquireModalInputChannel()
	defer release()
	type editorResult struct {
		value string
		ok    bool
	}
	done := make(chan editorResult, 1)
	go func() {
		value, ok := slash.ShowExtensionEditor("Custom summarization instructions", "", "")
		done <- editorResult{value, ok}
	}()
	input <- []byte("x")
	input <- []byte("\r")
	if result := <-done; !result.ok || result.value != "x" {
		t.Fatalf("editor = %+v, want x", result)
	}
	if len(terminal.reports) != 2 {
		t.Fatalf("%d reports %+v, want blocked then idle", len(terminal.reports), terminal.reports)
	}
	got := terminal.reports[0]
	if got.State != tui.ProgramStateBlocked || got.Kind != tui.ProgramStatusKindQuestion || got.Message != "Custom summarization instructions" || terminal.reports[1].State != tui.ProgramStateIdle {
		t.Fatalf("reports %+v", terminal.reports)
	}
}

// interactive-mode.ts:2742: an extension dialog dismissed by its signal still clears its blocked status, so an aborted
// call never leaves the terminal reporting a question that is no longer open.
func TestAbortedExtensionDialogClearsItsBlockedStatus(t *testing.T) {
	m, _ := newExtensionDialogProbe(t)
	terminal := recordProgramStatus(m, "")
	ui := &ExtUIContext{m: m}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := ui.Select(ctx, "Pick one", []string{"a"}, extension.ExtensionUIDialogOptions{})
		errCh <- err
	}()
	(<-m.uiTaskCh)()
	cancel()
	(<-m.uiTaskCh)()
	if err := <-errCh; err == nil {
		t.Fatal("aborted select returned no error")
	}
	if len(terminal.reports) != 2 || terminal.reports[0].State != tui.ProgramStateBlocked || terminal.reports[1].State != tui.ProgramStateIdle {
		t.Fatalf("reports %+v, want blocked then idle", terminal.reports)
	}
	if m.extensionDialog != nil {
		t.Fatal("the dialog stays installed after its signal aborted it")
	}
}
