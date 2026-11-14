//go:build parity

package runner

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// A visible-state wait is evidence about a step's keys only when the pane did not show that state before them. The program prints "leaf B"
// and, after a line of input, "leaf C". A step that sends Enter and waits for "leaf B" is refused before its keys go out; the same key with a
// wait for "leaf C" passes; a step that says its keys change nothing (a stable window) may name the state that holds.
func TestStepRefusesVisibleStateThatHeldBeforeItsKeys(t *testing.T) {
	useTmuxSocket(t, "pig-parity-held-state-"+uniqueID())
	tmuxServerMu.Lock()
	err := ensureTmuxServer()
	tmuxServerMu.Unlock()
	if err != nil {
		t.Skipf("tmux unavailable: %v", err)
	}
	session := "parity-held-state-" + uniqueID()
	registerSession(session)
	t.Cleanup(func() { killSession(session) })
	program := `printf 'leaf B\n'; read key; printf 'leaf C\n'; sleep 999`
	if out, err := exec.Command("tmux", tmuxArgs(createTmuxSessionArgs(session, "sh -c "+shellQuote(program), TmuxDriverConfig{Width: 60, Height: 6})...)...).CombinedOutput(); err != nil {
		t.Fatalf("tmux new-session: %v: %s", err, out)
	}
	ctx := context.Background()
	transcript := t.TempDir() + "/transcript"
	if !waitForPaneState(ctx, session, []string{"leaf B"}, nil, 10*time.Second) {
		t.Fatal("the program did not print leaf B")
	}

	unchanged := TmuxStep{Keys: []string{"x"}, WaitVisibleContains: []string{"leaf B"}, WaitStableMilliseconds: 100, WaitTimeoutSeconds: 10}
	if _, err := runTmuxStep(ctx, session, transcript, "pig", unchanged); err != nil {
		t.Fatalf("a step with a stable window may name a state that already holds: %v", err)
	}
	for _, held := range []TmuxStep{
		{Keys: []string{"Enter"}, WaitVisibleContains: []string{"leaf B"}, WaitTimeoutSeconds: 10},
		{Keys: []string{"Enter"}, WaitNotContains: []string{"leaf C"}, WaitTimeoutSeconds: 10},
		{Keys: []string{"Enter"}, WaitVisibleContainsPig: []string{"leaf B"}, WaitNotContains: []string{"leaf C"}, WaitTimeoutSeconds: 10},
	} {
		_, err := runTmuxStep(ctx, session, transcript, "pig", held)
		if err == nil || !errors.Is(err, errVisibleStateHeld) {
			t.Fatalf("step %+v: err = %v, want it refused: the pane showed that state before Enter", held, err)
		}
		if pane := capturePaneVisible(ctx, session); strings.Contains(pane, "leaf C") {
			t.Fatalf("step %+v was refused after its keys were sent: %q", held, pane)
		}
	}
	produced := TmuxStep{Keys: []string{"Enter"}, WaitVisibleContains: []string{"leaf C"}, WaitTimeoutSeconds: 10}
	if _, err := runTmuxStep(ctx, session, transcript, "pig", produced); err != nil {
		t.Fatalf("a wait for the state the key produces: %v", err)
	}
}

func TestVisibleWaitIsOnlyEvidence(t *testing.T) {
	visible := []string{"state"}
	for _, row := range []struct {
		name string
		step TmuxStep
		want bool
	}{
		{"keys and a visible wait", TmuxStep{Keys: []string{"Enter"}, WaitVisibleContains: visible}, true},
		{"keys and an excluded state", TmuxStep{Keys: []string{"Enter"}, WaitNotContains: visible}, true},
		{"no keys: the step waits for earlier work", TmuxStep{WaitVisibleContains: visible}, false},
		{"no visible wait", TmuxStep{Keys: []string{"Enter"}}, false},
		{"new output is awaited too", TmuxStep{Keys: []string{"Enter"}, WaitVisibleContains: visible, WaitContains: []string{"done"}}, false},
		{"a stable window declares no change", TmuxStep{Keys: []string{"Enter"}, WaitVisibleContains: visible, WaitStableMilliseconds: 400}, false},
		{"a settle", TmuxStep{Keys: []string{"Enter"}, WaitVisibleContains: visible, SettleSeconds: 1}, false},
		{"a frame watch", TmuxStep{Keys: []string{"Enter"}, WaitVisibleContains: visible, FrameWatch: &TmuxFrameWatch{}}, false},
	} {
		if got := row.step.visibleWaitIsOnlyEvidence(row.step.visibleContains("pig")); got != row.want {
			t.Errorf("%s: visibleWaitIsOnlyEvidence = %t, want %t", row.name, got, row.want)
		}
	}
}
