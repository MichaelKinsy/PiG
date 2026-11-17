//go:build parity

package runner

import (
	"slices"
	"strings"
	"testing"
)

// A render of the program: a frame (content free), then the cursor placement the renderer writes after it.
func frameWith(content string) string {
	return "\x1b[?2026h\x1b[0m" + content + "\x1b[?2026l"
}

const place = "\x1b[3A\x1b[6G\x1b[?25l"

func TestWriteKinds(t *testing.T) {
	tokens := tokenizeTranscript([]byte("\x1b[?2004h\x1b[>4;2m" + frameWith("a\r\nb") + "\x1b[1G\x1b[?25l" + "\x1b]0;t\a" + place + "\x1b[6G\x1b[?25l" + frameWith("c") + "\x1b[2A"))
	want := []string{"\x1b[?2004h", "\x1b[>4;2m", kindFrame, kindCursorPlacement, kindTitle, kindCursorPlacement, kindCursorPlacement, kindFrame, "\x1b[2A"}
	if got := writeKinds(tokens, false); !slices.Equal(got, want) {
		t.Fatalf("kinds = %q, want %q", got, want)
	}
}

func TestWriteKindsCollapsesFrameRuns(t *testing.T) {
	cp := "\x1b[?25l"
	frame := frameWith("x") + "\x1b[2A\x1b[3G" + cp
	tokens := func(parts ...string) []string { return tokenizeTranscript([]byte(strings.Join(parts, ""))) }
	for name, tc := range map[string]struct {
		tokens []string
		want   []string
	}{
		"one frame":                    {tokens(frame), []string{kindRenderRun}},
		"three frames in a row":        {tokens(frame, frame, frame), []string{kindRenderRun}},
		"a title splits two runs":      {tokens(frame, "\x1b]0;t\a", frame, frame), []string{kindRenderRun, kindTitle, kindRenderRun}},
		"a placement after a run":      {tokens(frame, frame, cp, cp), []string{kindRenderRun, kindCursorPlacement, kindCursorPlacement}},
		"a frame with no placement":    {tokens(frameWith("x"), frame), []string{kindFrame, kindRenderRun}},
		"a placement before the frame": {tokens(cp, frame), []string{kindCursorPlacement, kindRenderRun}},
	} {
		t.Run(name, func(t *testing.T) {
			if got := writeKinds(tc.tokens, true); !slices.Equal(got, tc.want) {
				t.Fatalf("kinds = %q, want %q", got, tc.want)
			}
		})
	}
}

// write_kinds compares what the program writes around its frames, in order and in number: a bare cursor placement more, a frame fewer or a placement before
// the frame it follows is a failure, and what a frame holds, a placement's column and the title's text are not compared.
func TestEvaluateSequenceWindowsWriteKinds(t *testing.T) {
	spec := SequenceWindowSpec{From: "\x1b[?2004h", WriteKinds: true}
	head := "\x1b[?2004h\x1b[>7u"
	base := head + frameWith("x") + place + "\x1b]0;pi - a\a" + frameWith("y") + place
	for name, tc := range map[string]struct {
		pig, pi string
		want    string
	}{
		"same kinds, other frame content, title and column": {head + frameWith("p") + "\x1b[1G\x1b[?25l" + "\x1b]0;pig - b\a" + frameWith("q") + place, base, ""},
		"one more standalone cursor placement in pig":       {base + "\x1b[6G\x1b[?25l", base, "differ at position 7 (pig wrote 8, pi wrote 7)"},
		"the extra placement after the title":               {head + frameWith("x") + place + "\x1b]0;t\a" + place + frameWith("y") + place, base, "differ at position 5"},
		"a frame fewer in pig":                              {head + frameWith("x") + place + "\x1b]0;t\a", base, "differ at position 5"},
		"the placement ahead of its frame":                  {head + place + frameWith("x") + "\x1b]0;t\a" + frameWith("y") + place, base, "differ at position 2"},
		"the title ahead of the placement":                  {head + frameWith("x") + "\x1b]0;t\a" + place + frameWith("y") + place, base, "differ at position 3"},
		"another mode set":                                  {"\x1b[?2004h\x1b[>5u" + frameWith("x") + place + "\x1b]0;t\a" + frameWith("y") + place, base, "differ at position 1"},
	} {
		t.Run(name, func(t *testing.T) {
			failures := outcomeOf(t, spec, tc.pig, tc.pi)
			switch {
			case tc.want == "" && len(failures) != 0:
				t.Fatalf("unexpected failures: %q", failures)
			case tc.want != "" && !slices.ContainsFunc(failures, func(f string) bool { return strings.Contains(f, tc.want) }):
				t.Fatalf("failures = %q, want one containing %q", failures, tc.want)
			}
		})
	}
	collapsing := SequenceWindowSpec{From: "\x1b[?2004h", WriteKinds: true, CollapseFrameRuns: true}
	runs := head + frameWith("x") + place + frameWith("y") + place + frameWith("z") + place
	if failures := outcomeOf(t, collapsing, runs, head+frameWith("x")+place); len(failures) != 0 {
		t.Fatalf("collapsed frame runs of three and one frame differ: %q", failures)
	}
	if failures := outcomeOf(t, collapsing, runs+place, head+frameWith("x")+place); len(failures) == 0 {
		t.Fatal("collapsed frame runs hide a cursor placement that follows no frame")
	}
	if err := (SequenceWindowSpec{From: "x", CollapseFrameRuns: true}).validate(); err == nil || !strings.Contains(err.Error(), "collapse_frame_runs") {
		t.Fatalf("collapse_frame_runs without write_kinds: %v", err)
	}
	if err := (SequenceWindowSpec{From: "x", WriteKinds: true, ErasuresOnly: true}).validate(); err == nil || !strings.Contains(err.Error(), "write_kinds") {
		t.Fatalf("write_kinds with erasures_only: %v", err)
	}
}
