//go:build parity

package runner

import (
	"bytes"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
)

// startupWindow is the named window of `window = "startup"`: from the first byte the program wrote to the first frame (CSI ? 2026 h), where Pi's and PiG's terminal mode setup ends and the first render begins.
const startupWindow = "startup"

// SequenceWindowSpec is one transcript_sequences_equal block: the ordered escape sequences and text runs the two binaries wrote between two points of the terminal transcript must be the same list, compared token by token, with no byte normalised.
//
// The window runs from the start of From (the stream's beginning when empty) to the start of Until (the stream's end when empty): both are the first occurrence of the text, Until searched after From. `window = "startup"` is the shorthand for the stream's beginning up to the first frame, CSI ? 2026 h, and is the only name; it replaces From and Until.
type SequenceWindowSpec struct {
	Window string `toml:"window"`
	From   string `toml:"from"`
	Until  string `toml:"until"`
	// Floating names the sequences whose place in the window depends on timing (a reply the terminal sends, a render the program throttles) and not on the program's order. Each is left out of the compared lists, must occur in the whole transcript of every run of both binaries, and is declared with the evidence that it floats.
	Floating []FloatingSequence `toml:"floating"`
	// ErasuresOnly narrows the comparison to the sequences that erase the display (CSI Ps J: ESC[2J clears the screen, ESC[3J the terminal's scrollback), their number and their order. It is for a window whose other bytes differ for reasons that are not its contract, such as the run's random directory in a footer, when the contract is whether the program wipes the screen or the scrollback. It does not combine with Floating.
	ErasuresOnly bool `toml:"erasures_only"`
	// WriteKinds reduces the window to the ordered kinds of the program's writes (writeKinds): each synchronized frame, each standalone cursor placement and each title is one entry, whatever it carries, and every other sequence stands for itself. It is for a window whose frames cannot be byte-equal (the run's random directory in the footer) and whose contract is what the program writes around them: a bare cursor placement a render with nothing to change adds, a frame more or fewer. Every window with it compares the kinds in order and their number.
	WriteKinds bool `toml:"write_kinds"`
	// CollapseFrameRuns, with WriteKinds, counts each maximal run of frames that each end in their own cursor placement as one entry: the number of frames of the run is not compared. It is declared only where Pi's frame count differs from run to run with the time a terminal reply arrives (a floating declaration says which), and every placement that does not follow a frame still is.
	CollapseFrameRuns bool `toml:"collapse_frame_runs"`
}

// FloatingSequence declares one sequence kind that is left out of a window's comparison: exactly one of Sequence (one complete escape sequence, byte for byte) or OSC (every operating system command with that number, whatever its text and terminator). A prefix of a sequence, a list of them or a pattern is not a declaration.
type FloatingSequence struct {
	Sequence string `toml:"sequence"`
	OSC      *int   `toml:"osc"`
	// Reason says what makes the sequence's place depend on timing.
	Reason string `toml:"reason"`
	// EvidenceRuns is how many Pi runs were recorded, and EvidencePositions the distinct token positions in the complete transcript (counted from 0, text runs included) at which the sequence was first seen in them. A sequence recorded at one position does not float: the declaration would hide a difference, and is refused. The smallest recorded position is a bound: either binary writing the sequence earlier fails.
	EvidenceRuns      int   `toml:"evidence_runs"`
	EvidencePositions []int `toml:"evidence_positions"`
}

// bounds returns the markers the window starts and ends at: empty for the stream's beginning and end.
func (w SequenceWindowSpec) bounds() (from, until string) {
	if w.Window == startupWindow {
		return "", string(beginSynchronizedOutput)
	}
	return w.From, w.Until
}

// title names the window in a failure message.
func (w SequenceWindowSpec) title() string {
	from, until := w.bounds()
	if w.Window != "" {
		return fmt.Sprintf("window %q", w.Window)
	}
	if w.ErasuresOnly {
		return fmt.Sprintf("window from %q until %q (erase sequences only)", from, until)
	}
	if w.WriteKinds {
		return fmt.Sprintf("window from %q until %q (write kinds)", from, until)
	}
	return fmt.Sprintf("window from %q until %q", from, until)
}

// validate is the scenario schema's check of one window.
func (w SequenceWindowSpec) validate() error {
	switch {
	case w.Window != "" && w.Window != startupWindow:
		return fmt.Errorf("transcript_sequences_equal window %q is unknown: the only name is %q", w.Window, startupWindow)
	case w.Window != "" && (w.From != "" || w.Until != ""):
		return fmt.Errorf("transcript_sequences_equal window %q replaces from and until", w.Window)
	case w.Window == "" && w.From == "" && w.Until == "":
		return fmt.Errorf("transcript_sequences_equal needs a window name or a from or until marker: the whole stream is not a stable comparison")
	}
	if w.CollapseFrameRuns && !w.WriteKinds {
		return fmt.Errorf("transcript_sequences_equal %s: collapse_frame_runs belongs to write_kinds", w.title())
	}
	if w.ErasuresOnly && w.WriteKinds {
		return fmt.Errorf("transcript_sequences_equal %s: erasures_only and write_kinds are two reductions of the window; name one", w.title())
	}
	if w.ErasuresOnly && len(w.Floating) > 0 {
		return fmt.Errorf("transcript_sequences_equal %s: erasures_only compares the erase sequences alone, and floating names sequences left out of a full comparison", w.title())
	}
	for _, f := range w.Floating {
		if err := f.validate(); err != nil {
			return fmt.Errorf("transcript_sequences_equal %s: %w", w.title(), err)
		}
	}
	for i, f := range w.Floating {
		if slices.ContainsFunc(w.Floating[:i], func(earlier FloatingSequence) bool { return earlier.label() == f.label() }) {
			return fmt.Errorf("transcript_sequences_equal %s: floating %s is declared twice", w.title(), f.label())
		}
	}
	return nil
}

func (f FloatingSequence) label() string {
	if f.OSC != nil {
		return "osc " + strconv.Itoa(*f.OSC)
	}
	return fmt.Sprintf("%q", f.Sequence)
}

func (f FloatingSequence) validate() error {
	switch {
	case (f.Sequence == "") == (f.OSC == nil):
		return fmt.Errorf("floating needs exactly one of sequence and osc")
	case f.OSC != nil && *f.OSC < 0:
		return fmt.Errorf("floating osc %d is not an operating system command number", *f.OSC)
	case f.Sequence != "" && !isOneSequence(f.Sequence):
		return fmt.Errorf("floating sequence %q is not exactly one complete escape sequence: a floating declaration names one sequence kind, never a prefix or a list", f.Sequence)
	case strings.TrimSpace(f.Reason) == "":
		return fmt.Errorf("floating %s requires a reason", f.label())
	case f.EvidenceRuns < 2:
		return fmt.Errorf("floating %s requires evidence_runs: the recorded Pi runs, at least 2", f.label())
	case len(distinct(f.EvidencePositions)) < 2:
		return fmt.Errorf("floating %s was recorded at %d distinct position(s): a sequence that does not move does not float, and its place is compared", f.label(), len(distinct(f.EvidencePositions)))
	}
	for _, p := range f.EvidencePositions {
		if p < 0 {
			return fmt.Errorf("floating %s has the negative evidence position %d", f.label(), p)
		}
	}
	return nil
}

func distinct(values []int) []int {
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	return slices.Compact(sorted)
}

// isOneSequence reports that s is exactly one complete escape sequence.
func isOneSequence(s string) bool {
	if s == "" || s[0] != 0x1b {
		return false
	}
	n, _ := escapeSequenceLength([]byte(s))
	return n == len(s)
}

// earliest is the smallest recorded position: a floating sequence moves later with timing, never ahead of where Pi first wrote it.
func (f FloatingSequence) earliest() int {
	if len(f.EvidencePositions) == 0 {
		return 0
	}
	return slices.Min(f.EvidencePositions)
}

// matches reports that token is a sequence of the kind f names.
func (f FloatingSequence) matches(token string) bool {
	if f.OSC == nil {
		return token == f.Sequence
	}
	rest, ok := strings.CutPrefix(token, "\x1b]")
	if !ok {
		return false
	}
	digits := strings.TrimLeft(rest, "0123456789")
	number := rest[:len(rest)-len(digits)]
	if number == "" || (digits != "" && digits[0] != ';' && digits[0] != 0x07 && digits[0] != 0x1b) {
		return false
	}
	n, err := strconv.Atoi(number)
	return err == nil && n == *f.OSC
}

// tokenizeTranscript splits a terminal stream into its escape sequences and the runs of other bytes between them, in order. A sequence the stream ends in the middle of is one token.
func tokenizeTranscript(raw []byte) []string {
	var tokens []string
	for i := 0; i < len(raw); {
		if raw[i] != 0x1b {
			n := bytes.IndexByte(raw[i:], 0x1b)
			if n < 0 {
				n = len(raw) - i
			}
			tokens = append(tokens, string(raw[i:i+n]))
			i += n
			continue
		}
		n, _ := escapeSequenceLength(raw[i:])
		if n == 0 {
			n = len(raw) - i
		}
		tokens = append(tokens, string(raw[i:i+n]))
		i += n
	}
	return tokens
}

// SequenceWindowResult is what one window saw in a run's finished transcript.
type SequenceWindowResult struct {
	// Err says why the window could not be cut: a marker the transcript does not hold.
	Err string
	// Tokens is the window's sequences and text runs in order, without the floating ones.
	Tokens []string
	// FloatingAt is, for each of the window's floating declarations, the position (token index, text runs counted, in the whole transcript) of the first sequence of its kind, or -1 when the transcript holds none.
	FloatingAt []int
}

// observeSequenceWindows reads the finished transcript at path (finishTranscript has returned nil) and cuts each window out of it.
func observeSequenceWindows(path string, windows []SequenceWindowSpec) []SequenceWindowResult {
	raw, err := os.ReadFile(path)
	out := make([]SequenceWindowResult, len(windows))
	if err != nil {
		for i := range out {
			out[i].Err = err.Error()
		}
		return out
	}
	whole := tokenizeTranscript(raw)
	for i, w := range windows {
		out[i] = observeSequenceWindow(raw, whole, w)
	}
	return out
}

// isDisplayErase reports that token is an erase-in-display sequence, CSI Ps J with Ps absent, 0, 1, 2 or 3: ESC[2J clears the screen and ESC[3J the terminal's scrollback.
func isDisplayErase(token string) bool {
	parameter, ok := strings.CutPrefix(token, "\x1b[")
	if !ok {
		return false
	}
	parameter, ok = strings.CutSuffix(parameter, "J")
	return ok && (parameter == "" || len(parameter) == 1 && parameter[0] >= '0' && parameter[0] <= '3')
}

func observeSequenceWindow(raw []byte, whole []string, w SequenceWindowSpec) SequenceWindowResult {
	from, until := w.bounds()
	start, end := 0, len(raw)
	if from != "" {
		if start = bytes.Index(raw, []byte(from)); start < 0 {
			return SequenceWindowResult{Err: fmt.Sprintf("the start marker %q is not in the transcript (%d bytes)", from, len(raw))}
		}
	}
	if until != "" {
		after := start + len(from)
		at := bytes.Index(raw[after:], []byte(until))
		if at < 0 {
			return SequenceWindowResult{Err: fmt.Sprintf("the end marker %q is not in the transcript after byte %d (%d bytes)", until, after, len(raw))}
		}
		end = after + at
	}
	result := SequenceWindowResult{FloatingAt: make([]int, len(w.Floating))}
	for i, f := range w.Floating {
		result.FloatingAt[i] = slices.IndexFunc(whole, f.matches)
	}
	for _, token := range tokenizeTranscript(raw[start:end]) {
		if (!w.ErasuresOnly || isDisplayErase(token)) && !slices.ContainsFunc(w.Floating, func(f FloatingSequence) bool { return f.matches(token) }) {
			result.Tokens = append(result.Tokens, token)
		}
	}
	if w.WriteKinds {
		result.Tokens = writeKinds(result.Tokens, w.CollapseFrameRuns)
	}
	return result
}

// evaluateSequenceWindows compares, for every run pair of every window a scenario declares, the two binaries' ordered sequences, and holds every floating declaration to its presence in both binaries' transcripts.
func evaluateSequenceWindows(o *ScenarioOutcome, a AssertSpec) {
	for w, spec := range a.TranscriptSequencesEqual {
		for run := range min(len(o.Pig.Runs), len(o.Pi.Runs)) {
			pig, pi := o.Pig.Runs[run], o.Pi.Runs[run]
			if pig.Err != nil || pi.Err != nil {
				continue
			}
			names := [2]string{"pig", "pi"}
			var sides [2]SequenceWindowResult
			complete := true
			for s, r := range [2]Result{pig, pi} {
				if w >= len(r.Sequences) {
					o.Failures = append(o.Failures, fmt.Sprintf("transcript_sequences_equal %s run %d: %s run holds no transcript window", spec.title(), run+1, names[s]))
					complete = false
					continue
				}
				if sides[s] = r.Sequences[w]; sides[s].Err != "" {
					o.Failures = append(o.Failures, fmt.Sprintf("transcript_sequences_equal %s run %d: %s: %s", spec.title(), run+1, names[s], sides[s].Err))
					complete = false
				}
			}
			if !complete {
				continue
			}
			for s, name := range names {
				for i, f := range spec.Floating {
					switch at := sides[s].FloatingAt[i]; {
					case at < 0:
						o.Failures = append(o.Failures, fmt.Sprintf("transcript_sequences_equal %s run %d: %s never wrote the floating %s: a declared floating sequence must occur in every run of both binaries", spec.title(), run+1, name, f.label()))
					case at < f.earliest():
						o.Failures = append(o.Failures, fmt.Sprintf("transcript_sequences_equal %s run %d: %s wrote the floating %s at position %d, earlier than the earliest position recorded for Pi (%d): floating lets a sequence arrive later than the program's order, not earlier", spec.title(), run+1, name, f.label(), at, f.earliest()))
					}
				}
			}
			if msg := firstSequenceDifference(sides[0].Tokens, sides[1].Tokens); msg != "" {
				o.Failures = append(o.Failures, fmt.Sprintf("transcript_sequences_equal %s run %d: %s", spec.title(), run+1, msg))
			}
		}
	}
}

// firstSequenceDifference names the first position at which the two lists differ, with the tokens around it, or returns "".
func firstSequenceDifference(pig, pi []string) string {
	i := 0
	for i < len(pig) && i < len(pi) && pig[i] == pi[i] {
		i++
	}
	if i == len(pig) && i == len(pi) {
		return ""
	}
	around := func(tokens []string) string {
		return fmt.Sprintf("%q", tokens[max(0, i-2):min(len(tokens), i+3)])
	}
	return fmt.Sprintf("the sequences differ at position %d (pig wrote %d, pi wrote %d); around it pig %s, pi %s", i, len(pig), len(pi), around(pig), around(pi))
}
