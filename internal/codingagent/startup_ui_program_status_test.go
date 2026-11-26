package codingagent

// Port of Pi main 27a40ea2a0f58246fa9793a752215bad7d2cecaf & 642a705e11c6f2a3baafc76fb15399fd2187fd43
// (packages/coding-agent/test/startup-ui-program-status.test.ts and interactive-mode-extension-dialogs.test.ts).

import (
	"context"
	"io"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/piglogin"
	"github.com/MichaelKinsy/PiG/tui"
)

type statusStartupTerminal struct {
	keys    []string
	reports []tui.ProgramStatus
	stopped bool
}

func (s *statusStartupTerminal) StartWithReadError(input func([]byte), _ func(), _ func(error)) error {
	for _, key := range s.keys {
		input([]byte(key))
	}
	return nil
}

func (s *statusStartupTerminal) Stop()        { s.stopped = true }
func (s *statusStartupTerminal) Write(string) {}
func (s *statusStartupTerminal) SetProgramStatus(status tui.ProgramStatus) {
	s.reports = append(s.reports, status)
}

func TestStartupDialogProgramStatus(t *testing.T) {
	restoreStartupTheme(t)
	restoreTUIKeybindings(t)

	t.Run("selector reports first line of title as question by default", func(t *testing.T) {
		term := &statusStartupTerminal{keys: []string{"\r"}}
		ui := tui.NewWithOutput(io.Discard, 80, 24)
		opts := StartupUIOptions{AgentDir: t.TempDir()}
		selector := newStartupSelector("Trust project folder?\n/work/project\n\nDetails", []string{"Trust"}, opts)
		completed, err := runStartupComponentWithBlocked(selector, opts, true, ui, term, new(DialogBlockedStatus(tui.ProgramStatusKindQuestion, "Trust project folder?\n/work/project\n\nDetails")))
		if err != nil || !completed {
			t.Fatalf("completed=%v err=%v", completed, err)
		}
		want := []tui.ProgramStatus{{App: AppName, State: tui.ProgramStateBlocked, Kind: tui.ProgramStatusKindQuestion, Message: "Trust project folder?"}}
		if !reflect.DeepEqual(term.reports, want) {
			t.Fatalf("reports = %+v, want %+v", term.reports, want)
		}
		if !term.stopped {
			t.Fatal("terminal was not stopped")
		}
	})

	t.Run("selector reports permission when blockedKind is permission", func(t *testing.T) {
		term := &statusStartupTerminal{keys: []string{"\r"}}
		ui := tui.NewWithOutput(io.Discard, 80, 24)
		opts := StartupUIOptions{AgentDir: t.TempDir()}
		selector := newStartupSelector("Run project hooks?", []string{"Run", "Skip"}, opts)
		completed, err := runStartupComponentWithBlocked(selector, opts, true, ui, term, new(DialogBlockedStatus(tui.ProgramStatusKindPermission, "Run project hooks?")))
		if err != nil || !completed {
			t.Fatalf("completed=%v err=%v", completed, err)
		}
		want := []tui.ProgramStatus{{App: AppName, State: tui.ProgramStateBlocked, Kind: tui.ProgramStatusKindPermission, Message: "Run project hooks?"}}
		if !reflect.DeepEqual(term.reports, want) {
			t.Fatalf("reports = %+v, want %+v", term.reports, want)
		}
	})

	t.Run("input reports blocked on its title", func(t *testing.T) {
		term := &statusStartupTerminal{keys: []string{"demo", "\r"}}
		ui := tui.NewWithOutput(io.Discard, 80, 24)
		opts := StartupUIOptions{AgentDir: t.TempDir()}
		input := newStartupInput("Project name", "", opts)
		completed, err := runStartupComponentWithBlocked(input, opts, true, ui, term, new(DialogBlockedStatus(tui.ProgramStatusKindQuestion, "Project name")))
		if err != nil || !completed || input.Text() != "demo" {
			t.Fatalf("completed=%v text=%q err=%v", completed, input.Text(), err)
		}
		want := []tui.ProgramStatus{{App: AppName, State: tui.ProgramStateBlocked, Kind: tui.ProgramStatusKindQuestion, Message: "Project name"}}
		if !reflect.DeepEqual(term.reports, want) {
			t.Fatalf("reports = %+v, want %+v", term.reports, want)
		}
	})

	t.Run("first-time setup reports permission request", func(t *testing.T) {
		pigHome, cwd := t.TempDir(), t.TempDir()
		t.Setenv("PIG_HOME", pigHome)
		piglogin.Refresh()
		t.Cleanup(func() { piglogin.Refresh() })
		agentDir := filepath.Join(pigHome, "agent")
		term := &statusStartupTerminal{keys: []string{"\x1b"}}
		_, err := showFirstTimeSetupWith(NewSettingsManager(cwd, agentDir), StartupUIOptions{AgentDir: agentDir}, tui.NewWithOutput(io.Discard, 80, 24), term)
		if err != nil {
			t.Fatal(err)
		}
		want := []tui.ProgramStatus{{App: AppName, State: tui.ProgramStateBlocked, Kind: tui.ProgramStatusKindPermission, Message: FirstTimeSetupQuestion}}
		if !reflect.DeepEqual(term.reports, want) {
			t.Fatalf("reports = %+v, want %+v", term.reports, want)
		}
	})
}

func TestExtensionDialogBlockedStatusFirstLineAndKind(t *testing.T) {
	t.Run("multi-line selector title reports only its first line", func(t *testing.T) {
		m, _ := newExtensionDialogProbe(t)
		m.runCtx = t.Context()
		terminal := recordProgramStatus(m, "")
		ui := &ExtUIContext{m: m}
		res := startDialog(t, m, func() (string, error) {
			return ui.Select(context.Background(), "Trust project folder?\n/work/project\n\nDetails", []string{"Trust"}, extension.ExtensionUIDialogOptions{})
		})
		if len(terminal.reports) == 0 {
			t.Fatal("expected blocked program status report")
		}
		got := terminal.reports[len(terminal.reports)-1]
		want := tui.ProgramStatus{App: AppName, State: tui.ProgramStateBlocked, Kind: tui.ProgramStatusKindQuestion, Message: "Trust project folder?"}
		if got != want {
			t.Fatalf("blocked report = %+v, want %+v", got, want)
		}
		if err := m.dispatchKey(t.Context(), "\r"); err != nil { t.Fatal(err) }
		ans := requireResult(t, res, "selector")
		if ans.err != nil || ans.value != "Trust" {
			t.Fatalf("result = %q, %v", ans.value, ans.err)
		}
	})

	t.Run("select with blockedKind reports the requested kind", func(t *testing.T) {
		m, _ := newExtensionDialogProbe(t)
		m.runCtx = t.Context()
		terminal := recordProgramStatus(m, "")
		ui := &ExtUIContext{m: m}
		res := startDialog(t, m, func() (string, error) {
			return ui.Select(context.Background(), "Trust project folder?\n/work/project", []string{"Trust"}, extension.ExtensionUIDialogOptions{BlockedKind: tui.ProgramStatusKindPermission})
		})
		if len(terminal.reports) == 0 {
			t.Fatal("expected blocked program status report")
		}
		got := terminal.reports[len(terminal.reports)-1]
		want := tui.ProgramStatus{App: AppName, State: tui.ProgramStateBlocked, Kind: tui.ProgramStatusKindPermission, Message: "Trust project folder?"}
		if got != want {
			t.Fatalf("blocked report = %+v, want %+v", got, want)
		}
		if err := m.dispatchKey(t.Context(), "\r"); err != nil { t.Fatal(err) }
		ans := requireResult(t, res, "selector")
		if ans.err != nil || ans.value != "Trust" {
			t.Fatalf("result = %q, %v", ans.value, ans.err)
		}
	})
}
