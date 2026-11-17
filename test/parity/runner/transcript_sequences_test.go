//go:build parity

package runner

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// syntheticFrame is a render as both binaries write one: a synchronized-output frame.
const syntheticFrame = "\x1b[?2026h\x1b[1;1H\x1b[2K> \x1b[?2026l"

func TestTokenizeTranscript(t *testing.T) {
	for name, tc := range map[string]struct {
		raw  string
		want []string
	}{
		"empty":               {"", nil},
		"csi and osc":         {"\x1b[?2004h\x1b]0;title\a\x1b]7501;?\x1b\\\x1b[c", []string{"\x1b[?2004h", "\x1b]0;title\a", "\x1b]7501;?\x1b\\", "\x1b[c"}},
		"text between":        {"\x1b[?25l\r\nhello\x1b[0m", []string{"\x1b[?25l", "\r\nhello", "\x1b[0m"}},
		"short sequence":      {"\x1b7a\x1b8", []string{"\x1b7", "a", "\x1b8"}},
		"unfinished at end":   {"a\x1b[?20", []string{"a", "\x1b[?20"}},
		"unfinished osc":      {"\x1b]0;tit", []string{"\x1b]0;tit"}},
		"lone ESC at the end": {"a\x1b", []string{"a", "\x1b"}},
	} {
		t.Run(name, func(t *testing.T) {
			if got := tokenizeTranscript([]byte(tc.raw)); !slices.Equal(got, tc.want) {
				t.Fatalf("tokens = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSequenceWindowBounds(t *testing.T) {
	raw := []byte("\x1b[?2004h\x1b[?1049h" + syntheticFrame + "\x1b[?25l" + syntheticFrame)
	for name, tc := range map[string]struct {
		spec SequenceWindowSpec
		want []string
		err  string
	}{
		"startup ends before the first frame": {SequenceWindowSpec{Window: "startup"}, []string{"\x1b[?2004h", "\x1b[?1049h"}, ""},
		"from a marker to the end":            {SequenceWindowSpec{From: "\x1b[?25l"}, []string{"\x1b[?25l", "\x1b[?2026h", "\x1b[1;1H", "\x1b[2K", "> ", "\x1b[?2026l"}, ""},
		"between markers":                     {SequenceWindowSpec{From: "\x1b[?1049h", Until: "\x1b[?25l"}, []string{"\x1b[?1049h", "\x1b[?2026h", "\x1b[1;1H", "\x1b[2K", "> ", "\x1b[?2026l"}, ""},
		// Until is looked for after From: a marker both start and end at is not an empty window.
		"until after from": {SequenceWindowSpec{From: "\x1b[?2026h", Until: "\x1b[?2026h"}, []string{"\x1b[?2026h", "\x1b[1;1H", "\x1b[2K", "> ", "\x1b[?2026l", "\x1b[?25l"}, ""},
		"missing start":    {SequenceWindowSpec{From: "\x1b[?9999h"}, nil, "the start marker"},
		"missing end":      {SequenceWindowSpec{Window: "", From: "\x1b[?2004h", Until: "\x1b[?9999h"}, nil, "the end marker"},
	} {
		t.Run(name, func(t *testing.T) {
			got := observeSequenceWindow(raw, tokenizeTranscript(raw), tc.spec)
			if tc.err != "" {
				if !strings.Contains(got.Err, tc.err) {
					t.Fatalf("Err = %q, want it to contain %q", got.Err, tc.err)
				}
				return
			}
			if got.Err != "" || !slices.Equal(got.Tokens, tc.want) {
				t.Fatalf("window = %q (%q), want %q", got.Tokens, got.Err, tc.want)
			}
		})
	}
}

func intp(n int) *int { return &n }

func TestFloatingMatchesOneKind(t *testing.T) {
	for name, tc := range map[string]struct {
		f     FloatingSequence
		token string
		want  bool
	}{
		"exact sequence":                     {FloatingSequence{Sequence: "\x1b[>4;2m"}, "\x1b[>4;2m", true},
		"another parameter":                  {FloatingSequence{Sequence: "\x1b[>4;2m"}, "\x1b[>4;1m", false},
		"osc 0 with BEL":                     {FloatingSequence{OSC: intp(0)}, "\x1b]0;\xcf\x80 - title\a", true},
		"osc 0 with ST":                      {FloatingSequence{OSC: intp(0)}, "\x1b]0;title\x1b\\", true},
		"osc 0 is not osc 10":                {FloatingSequence{OSC: intp(0)}, "\x1b]10;?\a", false},
		"osc 4 is not osc 4 of the palette?": {FloatingSequence{OSC: intp(4)}, "\x1b]4;1;?\a", true},
		"osc 7501":                           {FloatingSequence{OSC: intp(7501)}, "\x1b]7501;?\x1b\\", true},
		"osc without a payload":              {FloatingSequence{OSC: intp(0)}, "\x1b]0\a", true},
		"a csi is not an osc":                {FloatingSequence{OSC: intp(0)}, "\x1b[0m", false},
		"text is not an osc":                 {FloatingSequence{OSC: intp(0)}, "0;title", false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := tc.f.matches(tc.token); got != tc.want {
				t.Fatalf("matches(%q) = %v, want %v", tc.token, got, tc.want)
			}
		})
	}
}

func goodFloating() FloatingSequence {
	return FloatingSequence{Sequence: "\x1b[>4;2m", Reason: "Pi answers its Kitty query late", EvidenceRuns: 20, EvidencePositions: []int{35, 204}}
}

func TestSequenceWindowValidation(t *testing.T) {
	good := SequenceWindowSpec{Window: "startup", Floating: []FloatingSequence{goodFloating(), {OSC: intp(0), Reason: "the title follows the same reply", EvidenceRuns: 20, EvidencePositions: []int{36, 205}}}}
	if err := good.validate(); err != nil {
		t.Fatalf("a recorded window is refused: %v", err)
	}
	with := func(mutate func(*FloatingSequence)) SequenceWindowSpec {
		f := goodFloating()
		mutate(&f)
		return SequenceWindowSpec{Window: "startup", Floating: []FloatingSequence{f}}
	}
	for name, tc := range map[string]struct {
		spec SequenceWindowSpec
		want string
	}{
		"unknown window":          {SequenceWindowSpec{Window: "exit"}, "is unknown"},
		"window and markers":      {SequenceWindowSpec{Window: "startup", From: "x"}, "replaces from and until"},
		"the whole stream":        {SequenceWindowSpec{}, "whole stream"},
		"a prefix":                {with(func(f *FloatingSequence) { f.Sequence = "\x1b[" }), "not exactly one complete escape sequence"},
		"a prefix of a CSI":       {with(func(f *FloatingSequence) { f.Sequence = "\x1b[>4;" }), "not exactly one complete escape sequence"},
		"a list":                  {with(func(f *FloatingSequence) { f.Sequence = "\x1b[>4;2m\x1b[?u" }), "not exactly one complete escape sequence"},
		"text":                    {with(func(f *FloatingSequence) { f.Sequence = "Working" }), "not exactly one complete escape sequence"},
		"an unfinished osc":       {with(func(f *FloatingSequence) { f.Sequence = "\x1b]0;title" }), "not exactly one complete escape sequence"},
		"sequence and osc":        {with(func(f *FloatingSequence) { f.OSC = intp(0) }), "exactly one of sequence and osc"},
		"neither":                 {with(func(f *FloatingSequence) { f.Sequence = "" }), "exactly one of sequence and osc"},
		"negative osc":            {with(func(f *FloatingSequence) { f.Sequence, f.OSC = "", intp(-1) }), "not an operating system command number"},
		"no reason":               {with(func(f *FloatingSequence) { f.Reason = " " }), "requires a reason"},
		"a single run":            {with(func(f *FloatingSequence) { f.EvidenceRuns = 1 }), "evidence_runs"},
		"one recorded position":   {with(func(f *FloatingSequence) { f.EvidencePositions = []int{35, 35, 35} }), "does not float"},
		"no recorded position":    {with(func(f *FloatingSequence) { f.EvidencePositions = nil }), "does not float"},
		"a negative position":     {with(func(f *FloatingSequence) { f.EvidencePositions = []int{-1, 35} }), "negative evidence position"},
		"the same floating twice": {SequenceWindowSpec{Window: "startup", Floating: []FloatingSequence{goodFloating(), goodFloating()}}, "declared twice"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := tc.spec.validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validate() = %v, want an error containing %q", err, tc.want)
			}
		})
	}
	scenario := &Scenario{Name: "s", Driver: "cli-mode", Covers: []string{"x"}, Assert: AssertSpec{TranscriptSequencesEqual: []SequenceWindowSpec{good}}}
	if err := validate(scenario); err == nil || !strings.Contains(err.Error(), "requires interactive-tmux") {
		t.Fatalf("a transcript window on a driver without a transcript: %v", err)
	}
	scenario.Driver = "interactive-tmux"
	if err := validate(scenario); err != nil {
		t.Fatal(err)
	}
}

// outcomeOf evaluates one window over a pig and a pi transcript, each read from a file as a run's finished transcript is.
func outcomeOf(t *testing.T, spec SequenceWindowSpec, pig, pi string) []string {
	t.Helper()
	read := func(label, stream string) Result {
		path := filepath.Join(t.TempDir(), label+".log")
		if err := os.WriteFile(path, []byte(stream), 0o644); err != nil {
			t.Fatal(err)
		}
		return Result{Sequences: observeSequenceWindows(path, []SequenceWindowSpec{spec})}
	}
	o := &ScenarioOutcome{
		Scenario: &Scenario{Name: "sequences", Assert: AssertSpec{TranscriptSequencesEqual: []SequenceWindowSpec{spec}}},
		Pig:      SystemResults{System: "pig", Runs: []Result{read("pig", pig)}},
		Pi:       SystemResults{System: "pi", Runs: []Result{read("pi", pi)}},
	}
	EvaluateOutcome(o)
	return o.Failures
}

func TestEvaluateSequenceWindows(t *testing.T) {
	startup := SequenceWindowSpec{Window: "startup"}
	a := "\x1b[?2004h\x1b[>7u\x1b[?1049h"
	for name, tc := range map[string]struct {
		spec    SequenceWindowSpec
		pig, pi string
		want    string
	}{
		"same sequences":         {startup, a + syntheticFrame, a + syntheticFrame, ""},
		"bytes after the window": {startup, a + syntheticFrame + "\x1b[?25h", a + syntheticFrame, ""},
		"reordered":              {startup, "\x1b[>7u\x1b[?2004h\x1b[?1049h" + syntheticFrame, a + syntheticFrame, "differ at position 0 (pig wrote 3, pi wrote 3)"},
		"one more in pig":        {startup, a + "\x1b[?25l" + syntheticFrame, a + syntheticFrame, "differ at position 3 (pig wrote 4, pi wrote 3)"},
		"a parameter differs":    {startup, "\x1b[?2004h\x1b[>7u\x1b[?1049l" + syntheticFrame, a + syntheticFrame, "differ at position 2"},
		"a text run differs":     {startup, a + "x" + syntheticFrame, a + "y" + syntheticFrame, "differ at position 3"},
		"no first frame in pig":  {startup, a, a + syntheticFrame, "pig: the end marker"},
		"no first frame in pi":   {startup, a + syntheticFrame, a, "pi: the end marker"},
		"a window between marks": {SequenceWindowSpec{From: "\x1b[>7u", Until: "\x1b[?1049h"}, "\x1b[?2004h\x1b[>7u\x1b[?1049h", "\x1b[>7u\x1b[?1049h", ""},
		"a mark only pig writes": {SequenceWindowSpec{From: "\x1b[>7u", Until: "\x1b[?1049h"}, a, "\x1b[?2004h\x1b[?1049h", "pi: the start marker"},
	} {
		t.Run(name, func(t *testing.T) {
			failures := outcomeOf(t, tc.spec, tc.pig, tc.pi)
			switch {
			case tc.want == "" && len(failures) != 0:
				t.Fatalf("unexpected failures: %q", failures)
			case tc.want != "" && (len(failures) != 1 || !strings.Contains(failures[0], tc.want)):
				t.Fatalf("failures = %q, want one containing %q", failures, tc.want)
			}
		})
	}
}

func TestEvaluateSequenceWindowsFloating(t *testing.T) {
	spec := SequenceWindowSpec{Window: "startup", Floating: []FloatingSequence{
		{Sequence: "\x1b[>4;2m", Reason: "r", EvidenceRuns: 20, EvidencePositions: []int{3, 9}},
		{OSC: intp(0), Reason: "r", EvidenceRuns: 20, EvidencePositions: []int{4, 10}},
	}}
	base := "\x1b[?2004h\x1b[>7u\x1b[?1049h"
	keys, title := "\x1b[>4;2m", "\x1b]0;\xcf\x80 - dir-1\a"
	for name, tc := range map[string]struct {
		pig, pi string
		want    string
	}{
		"inside on one side, after the frame on the other": {base + keys + title + syntheticFrame, base + syntheticFrame + keys + title, ""},
		"between the same sequences, titles differ":        {base + keys + "\x1b]0;a\a" + syntheticFrame, base + keys + "\x1b]0;b\a" + syntheticFrame, ""},
		"pig never writes the title":                       {base + keys + syntheticFrame, base + keys + title + syntheticFrame, "pig never wrote the floating osc 0"},
		"pi never writes the keys":                         {base + keys + title + syntheticFrame, base + title + syntheticFrame, "pi never wrote the floating \"\\x1b[>4;2m\""},
		// The floating keys written ahead of the recorded positions is a difference of order, not of timing.
		"pig writes the keys before the recorded position": {"\x1b[>4;2m" + base + title + syntheticFrame, base + syntheticFrame + keys + title, "wrote the floating \"\\x1b[>4;2m\" at position 0, earlier than the earliest position recorded for Pi (3)"},
		"the rest still differs":                           {"\x1b[?25l" + base + keys + title + syntheticFrame, base + keys + title + syntheticFrame, "differ at position 0"},
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
}

// erasures_only compares the erase-in-display sequences of the window and nothing else: a terminal that is wiped (ESC[2J, ESC[3J) where the other is not, or in another
// order, differs; the frames, titles and text around them do not count.
func TestEvaluateSequenceWindowsErasuresOnly(t *testing.T) {
	spec := SequenceWindowSpec{From: "/new", ErasuresOnly: true}
	wipe := "\x1b[2J\x1b[H\x1b[3J"
	typed := "> /new\x1b[?2026h"
	for name, tc := range map[string]struct {
		pig, pi string
		want    string
	}{
		"neither wipes, the frames differ": {typed + "\x1b[19A\r\x1b]0;a\aX", typed + "\x1b[12A\rY", ""},
		"both wipe":                        {typed + wipe + "rows", typed + "rows" + wipe, ""},
		"before the window is not counted": {wipe + typed + "x", typed + "x", ""},
		"only pig wipes":                   {typed + wipe + "rows", typed + "rows", "differ at position 0 (pig wrote 2, pi wrote 0)"},
		"only pi wipes":                    {typed + "rows", typed + wipe + "rows", "differ at position 0 (pig wrote 0, pi wrote 2)"},
		"pig drops the scrollback erase":   {typed + "\x1b[2J\x1b[H", typed + wipe, "differ at position 1 (pig wrote 1, pi wrote 2)"},
		"the screen erase alone":           {typed + "\x1b[2J", typed + "\x1b[3J", "differ at position 0"},
		"a clear of the line is not one":   {typed + "\x1b[2K\x1b[K", typed, ""},
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
	if err := (SequenceWindowSpec{From: "/new", ErasuresOnly: true, Floating: []FloatingSequence{goodFloating()}}).validate(); err == nil || !strings.Contains(err.Error(), "erasures_only") {
		t.Fatalf("erasures_only with a floating declaration: %v", err)
	}
}

// The recorded startup transcripts (see the fixtures): Pi's three recorded forms agree once the declared floating pair is left out, and differ without the declaration; the comparator reports PiG's recorded startup as the difference from Pi's it is.
func TestRecordedStartupTranscripts(t *testing.T) {
	floating := []FloatingSequence{
		{Sequence: recordedPiLateKeys, Reason: "Pi writes it when the terminal answers its Kitty query: before the first frame in 9 of 20 recorded runs, after it in 11", EvidenceRuns: 20, EvidencePositions: []int{35, 204}},
		{OSC: intp(0), Reason: "the window title follows it", EvidenceRuns: 20, EvidencePositions: []int{36, 205}},
	}
	declared := SequenceWindowSpec{Window: "startup", Floating: floating}
	undeclared := SequenceWindowSpec{Window: "startup"}
	late := recordedPiStartup + syntheticFrame + recordedPiLateKeys + recordedPiTitleOne
	inside := recordedPiStartup + recordedPiLateKeys + recordedPiTitleTwo + syntheticFrame
	if failures := outcomeOf(t, declared, inside, late); len(failures) != 0 {
		t.Errorf("Pi inside the window against Pi after the first frame: %q", failures)
	}
	failures := outcomeOf(t, undeclared, inside, late)
	if len(failures) != 1 || !strings.Contains(failures[0], "differ at position 35 (pig wrote 37, pi wrote 35)") {
		t.Errorf("without the declaration the two Pi forms agree: %q", failures)
	}
	// PiG's recorded startup is not Pi's: its order differs from the first byte, and its CSI > 4 ; 2 m is at position 6, where Pi never wrote one in 20 runs, so the floating declaration does not excuse it.
	failures = outcomeOf(t, declared, recordedPigStartup+syntheticFrame, late)
	for _, want := range []string{"differ at position 0 (pig wrote 37, pi wrote 35)", "pig wrote the floating \"\\x1b[>4;2m\" at position 6, earlier than the earliest position recorded for Pi (35)"} {
		if !slices.ContainsFunc(failures, func(f string) bool { return strings.Contains(f, want) }) {
			t.Errorf("PiG's recorded startup against Pi's: %q lacks %q", failures, want)
		}
	}
}
