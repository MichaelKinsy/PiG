//go:build parity

package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestObserveFinalFrame(t *testing.T) {
	frame := func(body string) string { return "\x1b[?2026h" + body + "\x1b[37;1H\x1b[?25l\x1b[?2026l" }
	cases := []struct {
		name         string
		transcript   string
		wantFound    bool
		wantTrailing int
	}{
		{name: "reply frame ends the stream", transcript: frame("user") + frame("ANSWER-END"), wantFound: true},
		{name: "cursor-only frames follow", transcript: frame("ANSWER-END") + frame("") + frame(""), wantFound: true, wantTrailing: 2},
		{name: "reply absent", transcript: frame("user")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "terminal-transcript.log")
			if err := os.WriteFile(path, []byte(tc.transcript), 0o600); err != nil {
				t.Fatal(err)
			}
			got := observeFinalFrame(path, TmuxFinalFrame{Text: "ANSWER-END", QuietMilliseconds: 1})
			if !got.Observed || got.Found != tc.wantFound || got.Trailing != tc.wantTrailing {
				t.Fatalf("observeFinalFrame = %+v, want found=%v trailing=%d", got, tc.wantFound, tc.wantTrailing)
			}
		})
	}
}

func TestEvaluateOutcome_NoFramesAfterReply(t *testing.T) {
	ok := Result{FinalFrame: FinalFrameResult{Observed: true, Found: true, Text: "R"}}
	extra := Result{FinalFrame: FinalFrameResult{Observed: true, Found: true, Text: "R", Trailing: 3, Tail: "x"}}
	for name, tc := range map[string]struct {
		pig, pi Result
		want    string
	}{
		"clean":      {ok, ok, ""},
		"pig frames": {extra, ok, "pig run 1: 3 frame(s) began after the reply text"},
		"pi frames":  {ok, extra, "pi run 1: 3 frame(s) began after the reply text"},
		"no step":    {Result{}, ok, "pig run 1: no_frames_after_reply needs a tmux step with final_frame"},
	} {
		t.Run(name, func(t *testing.T) {
			o := &ScenarioOutcome{
				Scenario: &Scenario{Name: name, Assert: AssertSpec{NoFramesAfterReply: true}},
				Pig:      SystemResults{System: "pig", Runs: []Result{tc.pig}},
				Pi:       SystemResults{System: "pi", Runs: []Result{tc.pi}},
			}
			EvaluateOutcome(o)
			switch {
			case tc.want == "" && len(o.Failures) != 0:
				t.Fatalf("unexpected failures: %v", o.Failures)
			case tc.want != "" && (len(o.Failures) != 1 || !strings.HasPrefix(o.Failures[0], tc.want)):
				t.Fatalf("failures = %q, want prefix %q", o.Failures, tc.want)
			}
		})
	}
}

// A numbered divergence over a repaint is pinned by the number of cursor-only frames each binary writes after the final_frame text (D618), since both
// binaries write other frames there while the terminal answers their start-up queries.
func TestObserveFinalFrameKeepsTheFramesAfterTheText(t *testing.T) {
	frame := func(body string) string { return "\x1b[?2026h" + body + "\x1b[37;1H\x1b[?25l\x1b[?2026l" }
	cursorOnly := "\x1b[?2026h\x1b[37;1H\x1b[?25l\x1b[?2026l"
	path := filepath.Join(t.TempDir(), "terminal-transcript.log")
	if err := os.WriteFile(path, []byte(frame("ready")+frame("\x1b[2;1Hx")+cursorOnly), 0o600); err != nil {
		t.Fatal(err)
	}
	got := observeFinalFrame(path, TmuxFinalFrame{Text: "ready", QuietMilliseconds: 1})
	if got.Trailing != 2 || got.CursorOnly() != 1 {
		t.Fatalf("observeFinalFrame = %+v, want two trailing frames of which one is cursor-only", got)
	}
}

func TestEvaluateOutcome_FinalFrameCursorOnlyPerBinary(t *testing.T) {
	cursorOnly := "\x1b[?2026h\x1b[37;1H\x1b[?25l\x1b[?2026l"
	rewrite := "\x1b[?2026h\x1b[2;1H\x1b[2Kx\x1b[37;1H\x1b[?25l\x1b[?2026l"
	result := func(frames string) Result {
		return Result{FinalFrame: FinalFrameResult{Observed: true, Found: true, Text: "R", Trailing: strings.Count(frames, "\x1b[?2026h"), Frames: frames}}
	}
	one := 1
	spec := AssertSpec{FinalFrameCursorOnlyPig: &one, FinalFrameCursorOnlyPi: &one}
	for name, tc := range map[string]struct {
		pig, pi Result
		want    string
	}{
		"both write one":              {result(cursorOnly), result(cursorOnly), ""},
		"other frames are not pinned": {result(rewrite + cursorOnly), result(cursorOnly + rewrite), ""},
		"pig writes a second one":     {result(rewrite + cursorOnly + cursorOnly), result(cursorOnly), "pig run 1: 2 cursor-only frame(s) began after the text \"R\", want 1"},
		"pig writes none":             {result(rewrite), result(cursorOnly), "pig run 1: 0 cursor-only frame(s) began after the text \"R\", want 1"},
		"pi writes none":              {result(cursorOnly), result(rewrite), "pi run 1: 0 cursor-only frame(s) began after the text \"R\", want 1"},
		"no step":                     {Result{}, result(cursorOnly), "pig run 1: final_frame_cursor_only_pig needs a tmux step with final_frame"},
	} {
		t.Run(name, func(t *testing.T) {
			o := &ScenarioOutcome{
				Scenario: &Scenario{Name: name, Assert: spec},
				Pig:      SystemResults{System: "pig", Runs: []Result{tc.pig}},
				Pi:       SystemResults{System: "pi", Runs: []Result{tc.pi}},
			}
			EvaluateOutcome(o)
			switch {
			case tc.want == "" && len(o.Failures) != 0:
				t.Fatalf("unexpected failures: %v", o.Failures)
			case tc.want != "" && (len(o.Failures) == 0 || !strings.HasPrefix(o.Failures[0], tc.want)):
				t.Fatalf("failures = %q, want first to have prefix %q", o.Failures, tc.want)
			}
		})
	}
}
