//go:build parity

package runner

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// A step's frame watch ends with the step (runTmuxStep joins its sampler before it returns), and the final capture is taken after every step. The
// final capture's retry takes extra captures; none of them reaches a watch, so a pane the step's watch forbids that appears on screen while the retry
// runs is not counted as a frame the step put there, and the watch's samples are the ones the step's own duration produced.
//
// The program prints "leaf B" and waits for a key; the step waits for "leaf B" to hold still while a frame watch forbids "leaf C". The first round
// of the final capture is made unstable: between its plain and its escaped capture the key is sent, the pane becomes "leaf C", and the second plain
// capture shows the change. The watch must have seen what it saw before (enough clean samples, no forbidden frame) and the final capture must take
// exactly one more round, ending on the settled screen, which is the forbidden pane: it is the final screen, not a frame of the step.
func TestFinalCaptureRetryDoesNotFeedTheStepFrameWatch(t *testing.T) {
	useTmuxSocket(t, "pig-parity-frame-capture-"+uniqueID())
	tmuxServerMu.Lock()
	err := ensureTmuxServer()
	tmuxServerMu.Unlock()
	if err != nil {
		t.Skipf("tmux unavailable: %v", err)
	}
	step := TmuxStep{
		WaitVisibleContains:    []string{"leaf B"},
		WaitStableMilliseconds: 100,
		WaitTimeoutSeconds:     10,
		FrameWatch:             &TmuxFrameWatch{StaleContains: []string{"leaf C"}, MinSamples: 3},
	}
	cfg := TmuxDriverConfig{Width: 60, Height: 6, CaptureStart: "leaf", Steps: []TmuxStep{step}}
	session := "parity-frame-capture-" + uniqueID()
	registerSession(session)
	t.Cleanup(func() { killSession(session) })
	program := `printf 'leaf B\n'; read key; printf '\033[2J\033[Hleaf C\n'; sleep 999`
	if out, err := exec.Command("tmux", tmuxArgs(createTmuxSessionArgs(session, "sh -c "+shellQuote(program), cfg)...)...).CombinedOutput(); err != nil {
		t.Fatalf("tmux new-session: %v: %s", err, out)
	}
	ctx := context.Background()

	watched, err := runTmuxStep(ctx, session, t.TempDir()+"/transcript", "pig", step)
	if err != nil {
		t.Fatal(err)
	}
	if !watched.Watched || watched.Samples < watched.MinSamples || watched.StaleCount != 0 {
		t.Fatalf("frame watch = %+v, want a watched step with at least %d samples and no forbidden frame", watched, watched.MinSamples)
	}

	reads := 0
	read := tmuxPaneReader(ctx, session, cfg)
	unstableOnce := func(escaped bool) (string, error) {
		reads++
		if reads == 2 {
			// The screen moves inside the first round, after its first plain capture.
			if out, err := exec.Command("tmux", tmuxArgs("send-keys", "-t", session, "Enter")...).CombinedOutput(); err != nil {
				t.Fatalf("send-keys: %v: %s", err, out)
			}
			for deadline := time.Now().Add(10 * time.Second); !strings.Contains(capturePaneVisible(ctx, session), "leaf C") && time.Now().Before(deadline); {
				time.Sleep(tmuxQuiescencePoll)
			}
		}
		return read(escaped)
	}
	plain, escaped, err := capturePairFrom(ctx, unstableOnce, cfg, captureUse{plain: true, escaped: true})
	if err != nil {
		t.Fatal(err)
	}
	if reads != 6 {
		t.Fatalf("the final capture read the pane %d times, want 6: the unstable round and one more", reads)
	}
	for kind, got := range map[string]string{"plain": plain, "escaped": escaped} {
		if !strings.Contains(got, "leaf C") {
			t.Fatalf("%s final capture %q does not show the settled screen", kind, got)
		}
	}
}
