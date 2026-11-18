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
			got := observeFinalFrame(path, TmuxFinalFrame{Text: "ANSWER-END"})
			if !got.Observed || got.Found != tc.wantFound || got.Trailing != tc.wantTrailing {
				t.Fatalf("observeFinalFrame = %+v, want found=%v trailing=%d", got, tc.wantFound, tc.wantTrailing)
			}
		})
	}
}

func TestEvaluateOutcome_NoFramesAfterReply(t *testing.T) {
	ok := Result{FinalFrame: FinalFrameResult{Observed: true, Found: true, Text: "R"}}
	extra := Result{FinalFrame: FinalFrameResult{Observed: true, Found: true, Text: "R", Trailing: 3, Tail: "x"}}
	absent := Result{FinalFrame: FinalFrameResult{Observed: true, Text: "R"}}
	for name, tc := range map[string]struct {
		pig, pi Result
		want    string
	}{
		"clean":      {ok, ok, ""},
		"pig frames": {extra, ok, "pig run 1: 3 frame(s) began after the reply text"},
		"pi frames":  {ok, extra, "pi run 1: 3 frame(s) began after the reply text"},
		"no step":    {Result{}, ok, "pig run 1: no_frames_after_reply needs a tmux step with final_frame"},
		// A transcript without the reply is an error on either side, never "no frame after the reply".
		"pig reply absent": {absent, ok, "pig run 1: the reply text \"R\" is not in the raw transcript"},
		"pi reply absent":  {ok, absent, "pi run 1: the reply text \"R\" is not in the raw transcript"},
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

// The frames of a repaint are pinned by the number of cursor-only frames each binary writes after the final_frame text, since both
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

// A step that names a write log is read from it, the bytes the renderers wrote, and not from the terminal transcript: PiG's screen writer (D624)
// does not send the terminal a frame that changes nothing, so the transcript of a writer-mode run lacks the cursor-only frame the write log holds.
func TestFinalFrameIsReadFromTheWriteLogAStepNames(t *testing.T) {
	frame := func(body string) string { return "\x1b[?2026h" + body + "\x1b[37;1H\x1b[?25l\x1b[?2026l" }
	cursorOnly := "\x1b[?2026h\x1b[37;1H\x1b[?25l\x1b[?2026l"
	dir := t.TempDir()
	transcript, writeLog := filepath.Join(dir, "terminal-transcript.log"), filepath.Join(dir, "renderer-writes.log")
	if err := os.WriteFile(transcript, []byte(frame("ready")+frame("\x1b[2;1Hx")), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(writeLog, []byte(frame("ready")+frame("\x1b[2;1Hx")+cursorOnly), 0o600); err != nil {
		t.Fatal(err)
	}
	expand := func(s string) string { return strings.ReplaceAll(s, "{{TEMP}}", dir) }
	plain := TmuxFinalFrame{Text: "ready"}
	if source := finalFrameSource(transcript, plain, expand); source != transcript {
		t.Fatalf("a step without a write log reads %q, want the transcript %q", source, transcript)
	}
	if got := observeFinalFrame(finalFrameSource(transcript, plain, expand), plain); got.CursorOnly() != 0 {
		t.Fatalf("the transcript holds %d cursor-only frame(s) after the text, want 0", got.CursorOnly())
	}
	named := TmuxFinalFrame{Text: "ready", WriteLog: "{{TEMP}}/renderer-writes.log"}
	if source := finalFrameSource(transcript, named, expand); source != writeLog {
		t.Fatalf("a step with a write log reads %q, want %q", source, writeLog)
	}
	if got := observeFinalFrame(finalFrameSource(transcript, named, expand), named); !got.Found || got.CursorOnly() != 1 {
		t.Fatalf("the write log gave %+v, want the text found and one cursor-only frame after it", got)
	}
	// A write log that was never written (a binary that ignored PI_TUI_WRITE_LOG) is a missing text, which the comparators report, not a count of 0.
	missing := TmuxFinalFrame{Text: "ready", WriteLog: "{{TEMP}}/absent.log"}
	if got := observeFinalFrame(finalFrameSource(transcript, missing, expand), missing); got.Found {
		t.Fatalf("a missing write log gave %+v, want the text not found", got)
	}
}

// A write log that is missing or empty on either side fails the comparators that read it; it is never a count of 0 or a skipped check.
func TestEvaluateOutcome_AMissingOrEmptyWriteLogFails(t *testing.T) {
	frame := func(body string) string { return "\x1b[?2026h" + body + "\x1b[37;1H\x1b[?25l\x1b[?2026l" }
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "full.log"), []byte(frame("R")), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "empty.log"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	expand := func(s string) string { return strings.ReplaceAll(s, "{{TEMP}}", dir) }
	observe := func(name string) Result {
		spec := TmuxFinalFrame{Text: "R", WriteLog: "{{TEMP}}/" + name}
		return Result{FinalFrame: observeFinalFrame(finalFrameSource(filepath.Join(dir, "terminal-transcript.log"), spec, expand), spec)}
	}
	zero := 0
	for _, log := range []string{"absent.log", "empty.log"} {
		for _, side := range []string{"pig", "pi"} {
			for name, assert := range map[string]AssertSpec{
				"no_frames_after_reply":   {NoFramesAfterReply: true},
				"final_frame_cursor_only": {FinalFrameCursorOnlyPig: &zero, FinalFrameCursorOnlyPi: &zero},
			} {
				t.Run(log+"/"+side+"/"+name, func(t *testing.T) {
					pig, pi := observe("full.log"), observe("full.log")
					if side == "pig" {
						pig = observe(log)
					} else {
						pi = observe(log)
					}
					o := &ScenarioOutcome{
						Scenario: &Scenario{Name: name, Assert: assert},
						Pig:      SystemResults{System: "pig", Runs: []Result{pig}},
						Pi:       SystemResults{System: "pi", Runs: []Result{pi}},
					}
					EvaluateOutcome(o)
					if len(o.Failures) != 1 || !strings.HasPrefix(o.Failures[0], side+" run 1: ") || !strings.HasSuffix(o.Failures[0], `"R" is not in the write log (PI_TUI_WRITE_LOG)`) {
						t.Fatalf("failures = %q, want one for %s that says the text is not in the write log", o.Failures, side)
					}
				})
			}
		}
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
