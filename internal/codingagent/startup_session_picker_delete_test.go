package codingagent

// pi: packages/coding-agent/src/cli/session-picker.ts

import (
	"errors"
	"io/fs"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

type startupPickerOutput struct {
	mu   sync.Mutex
	text strings.Builder
}

func (out *startupPickerOutput) Write(p []byte) (int, error) {
	out.mu.Lock()
	defer out.mu.Unlock()
	return out.text.Write(p)
}

func (out *startupPickerOutput) String() string {
	out.mu.Lock()
	defer out.mu.Unlock()
	return out.text.String()
}

// Pi's --resume picker (cli/session-picker.ts:26) is the same SessionSelectorComponent, whose confirmed deletion (session-selector.ts:842-866) removes the Session, reports it and refreshes. PiG's startup picker must delete through the same path.
func TestStartupSessionPickerDeletesLikePi(t *testing.T) {
	t.Setenv(ENV_AGENT_DIR, t.TempDir())
	t.Setenv("PATH", t.TempDir()) // No trash command: Pi unlinks and reports "Session deleted".
	cwd := t.TempDir()
	first := listingPersistedSession(t, defaultSessionDir(cwd), cwd, "first")
	second := listingPersistedSession(t, defaultSessionDir(cwd), cwd, "second")
	manager := NewSessionManager(cwd)
	selector, _ := newStartupSessionSelector(
		func(options SessionListOptions) ([]SessionInfo, error) { return manager.ListCurrentSessions(options) },
		func(options SessionListOptions) ([]SessionInfo, error) { return manager.ListAllSessions(options) },
		sessionSelectorInputBindings(t))
	for selector.scopeLoad(sessionScopeCurrent) != nil {
		select {
		case update := <-selector.work.updates:
			update()
		case result := <-selector.loadResult(sessionScopeCurrent):
			selector.finishLoad(sessionScopeCurrent, result)
		}
	}
	if len(selector.filtered) != 2 {
		t.Fatalf("listed %d sessions, want 2", len(selector.filtered))
	}
	victim := selector.filtered[0].Session.Path
	survivor := first
	if victim == first {
		survivor = second
	}
	out := &startupPickerOutput{}
	ui := tui.NewWithOutput(out, 120, 30)
	terminal := &fakeStartupTerminal{}
	done := make(chan error, 1)
	go func() {
		_, err := runStartupComponentWith(selector, StartupUIOptions{Settings: Settings{Theme: "dark"}}, false, ui, terminal)
		done <- err
	}()
	terminal.send(t, "\x04")
	terminal.send(t, "\r")
	terminal.send(t, "\x1b")
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("startup picker did not finish")
	}
	if _, err := os.Stat(victim); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("confirmed deletion left %s: %v", victim, err)
	}
	if _, err := os.Stat(survivor); err != nil {
		t.Errorf("unselected session changed: %v", err)
	}
	if frames := stripANSI(out.String()); !strings.Contains(frames, "Session deleted") {
		t.Errorf("startup picker never reported the deletion; frames end with %q", frames[max(0, len(frames)-400):])
	}
}
