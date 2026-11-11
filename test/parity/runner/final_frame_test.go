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
