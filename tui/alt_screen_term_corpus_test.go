package tui

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// The terminal-engine corpus is what test/term-engines replays through real terminal emulators (D205): every scenario below is played
// twice by the alternate-screen renderer, as variant A (scroll regions, the default) and as variant B (PIG_TUI_SCROLL_ROWS=0, Pi's
// bytes), and the bytes of each frame are recorded as one step. An engine must show the same screen after step n of A as after step
// n of B. The test always generates the corpus and checks that it exercises the feature; PIG_TERM_ENGINE_CORPUS_DIR names a directory
// it also writes the corpus to (make test-term-engines sets it). Nothing is committed: the corpus is regenerated from deterministic Go.

const termCorpusDirEnv = "PIG_TERM_ENGINE_CORPUS_DIR"

// termCorpusStep is one recorded frame: the bytes of the variant's stream up to End (and after the previous step's End) that the terminal
// receives after Resize, when set, changed its size to Resize[0] columns by Resize[1] rows.
type termCorpusStep struct {
	Resize []int
	End    int
}

type termCorpusVariant struct {
	File  string
	Steps []termCorpusStep
	data  []byte
}

type termCorpusScenario struct {
	Name     string
	Cols     int
	Rows     int
	Variants map[string]*termCorpusVariant
}

type termCorpusManifest struct {
	Scenarios []*termCorpusScenario
}

// termRecorder records the steps of one scenario's variant.
type termRecorder struct{ v *termCorpusVariant }

func (r *termRecorder) add(resize []int, out string) {
	r.v.data = append(r.v.data, out...)
	r.v.Steps = append(r.v.Steps, termCorpusStep{Resize: slices.Clone(resize), End: len(r.v.data)})
}

// termSession is a scenario's screen, whose frames are drawn only by frame.
type termSession struct {
	t   *testing.T
	rec *termRecorder
	out *strings.Builder
	ui  *TuiAltScreen
}

// frame draws a frame (after the screen changed size to resize, when set) and records its bytes as one step.
func (s *termSession) frame(resize ...int) {
	s.t.Helper()
	s.ui.Render()
	s.rec.add(resize, s.out.String())
	s.out.Reset()
}

func (s *termSession) resize(cols, rows int) {
	s.t.Helper()
	s.ui.SetFixedSize(cols, rows)
	s.frame(cols, rows)
}

// scrollBack scrolls the viewport by lines and records the frame, requiring that the viewport moved.
func (s *termSession) scrollBack(lines int) {
	s.t.Helper()
	before := s.ui.ViewportTop()
	s.ui.ScrollBy(lines)
	if s.ui.ViewportTop() == before {
		s.t.Fatalf("ScrollBy(%d) did not move the viewport from %d", lines, before)
	}
	s.frame()
}

// input sends data to the viewport as the driver does and records the frame, requiring that the viewport handled it and moved.
func (s *termSession) input(data string) {
	s.t.Helper()
	before := s.ui.ViewportTop()
	if !s.ui.HandleViewportInput(data) {
		s.t.Fatalf("the viewport did not handle %q", data)
	}
	if s.ui.ViewportTop() == before {
		s.t.Fatalf("%q did not move the viewport from %d", data, before)
	}
	s.frame()
}

type termScenario struct {
	name       string
	cols, rows int
	// dock is the component below the transcript, when the scenario has one.
	dock *termLines
	// unclipped makes the transcript render lines wider than the screen, which the renderer clips.
	unclipped bool
	play      func(s *termSession, text, dock *termLines)
}

// termLines is a component that renders its lines clipped to the width it is rendered at, each clipped line closed with a reset, as the
// components of a screen render theirs (a line wider than the screen is clipped by the renderer, which drops the line's closing SGR reset
// and leaves its attributes set for the rows after it: knownLeak below records what that does).
type termLines struct {
	lines     []string
	unclipped bool // renders its lines as they are, wider than the screen or not
}

func (c *termLines) Render(width int) []string {
	rows := make([]string, len(c.lines))
	for i, line := range c.lines {
		rows[i] = line
		if !c.unclipped && widthx.VisibleWidth(line) > width {
			rows[i] = widthx.TruncateToWidth(line, width, "", false) + "\x1b[0m"
		}
	}
	return rows
}

func (*termLines) Invalidate() {}

// termStyled is the text of line i, in a mix of colors and attributes.
func termStyled(i int, text string) string {
	switch i % 6 {
	case 0:
		return fmt.Sprintf("\x1b[3%dm%s\x1b[0m", 1+i%7, text)
	case 1:
		return fmt.Sprintf("\x1b[1;4%dm%s\x1b[0m", 1+i%7, text)
	case 2:
		return fmt.Sprintf("\x1b[38;2;%d;%d;%dm%s\x1b[39m", i*7%256, i*13%256, i*29%256, text)
	case 3:
		return fmt.Sprintf("\x1b[48;5;%dm\x1b[38;5;%dm%s\x1b[0m", 16+i%200, 232+i%24, text)
	case 4:
		return fmt.Sprintf("\x1b[3;9m%s\x1b[0m tail", text)
	}
	return text
}

// termFilled is text padded with filler to exactly width columns.
func termFilled(text string, width int) string {
	for lineWidth := widthx.VisibleWidth(text); lineWidth < width; lineWidth++ {
		text += "."
	}
	return text
}

var termCorpusScenarios = []termScenario{
	{name: "stream-follow", cols: 40, rows: 12, play: func(s *termSession, text, dock *termLines) {
		for i := range 150 {
			line := fmt.Sprintf("line %03d the quick brown fox jumps over", i)
			if i%9 == 4 {
				line = termFilled(fmt.Sprintf("line %03d full width ", i), 40)
			}
			text.lines = append(text.lines, termStyled(i, line))
			s.frame()
		}
	}},
	{name: "stream-tokens-dock", cols: 53, rows: 17, dock: &termLines{lines: []string{"\x1b[7m ready \x1b[0m"}}, play: func(s *termSession, text, dock *termLines) {
		rng := rand.New(rand.NewPCG(7, 205))
		text.lines = append(text.lines, "")
		for frame := range 160 {
			switch last := len(text.lines) - 1; rng.IntN(5) {
			case 0:
				text.lines = append(text.lines, termStyled(frame, fmt.Sprintf("para %d starts", frame)))
			case 1:
				for range 1 + rng.IntN(4) {
					text.lines = append(text.lines, termStyled(frame, strings.Repeat("word ", 1+rng.IntN(9))))
				}
			default:
				text.lines[last] += fmt.Sprintf(" tok%d", frame)
			}
			if frame%7 == 0 {
				dock.lines = []string{fmt.Sprintf("\x1b[7m working %d \x1b[0m", frame), "\x1b[2m> prompt\x1b[0m"}
			}
			s.frame()
		}
	}},
	{name: "wheel-pageup", cols: 60, rows: 14, play: func(s *termSession, text, dock *termLines) {
		for i := range 180 {
			text.lines = append(text.lines, termStyled(i, fmt.Sprintf("row %03d wheel and page keys", i)))
		}
		s.frame()
		for range 3 {
			s.input("\x1b[5~")
		}
		clock := 0.0
		previous := wheelClock
		wheelClock = func() float64 { clock += 40; return clock }
		defer func() { wheelClock = previous }()
		for range 12 {
			s.input("\x1b[<64;5;5M")
		}
		for range 5 {
			s.input("\x1b[<65;5;5M")
		}
		for range 4 {
			text.lines = append(text.lines, "streamed while scrolled back")
			s.frame()
		}
		s.scrollBack(-1)
		s.scrollBack(1)
		s.input("\x1b[6~")
		s.input("\x1b[5~")
		s.ui.ScrollToTop()
		s.frame()
		for range 6 {
			s.scrollBack(2)
		}
		s.ui.ScrollToBottom()
		s.frame()
		text.lines = append(text.lines, "following again")
		s.frame()
	}},
	{name: "resize-mid-stream", cols: 50, rows: 14, play: func(s *termSession, text, dock *termLines) {
		n := 0
		stream := func(count int) {
			for range count {
				text.lines = append(text.lines, termStyled(n, fmt.Sprintf("streamed %03d some text that may wrap at narrow widths", n)))
				n++
				s.frame()
			}
		}
		stream(30)
		s.resize(30, 10)
		stream(12)
		s.resize(70, 20)
		stream(12)
		s.scrollBack(-5)
		s.resize(50, 6)
		stream(10)
		s.scrollBack(-3)
		s.resize(50, 14)
		stream(20)
	}},
	{name: "wide-styled", cols: 31, rows: 11, play: func(s *termSession, text, dock *termLines) {
		samples := []string{
			"日本語のテキストが流れます",
			"한국어 텍스트와 日本語 mixed",
			"emoji 😀😃😄 and 🚀🛰️ mixed",
			"family 👨‍👩‍👧‍👦 zwj and flags 🇯🇵🇺🇸",
			"combining e\u0301 a\u0308 n\u0303 marks",
			"hearts ❤️ ✔️ ☎️ variation",
			"\x1b]8;;https://example.com/a\x1b\\link text\x1b]8;;\x1b\\ after",
			"\x1b[1;3;4;38;2;255;128;0;48;2;0;32;64mtruecolor bold italic\x1b[0m",
			"\x1b[7mreverse\x1b[27m \x1b[2mdim\x1b[22m \x1b[53moverline\x1b[55m",
			"",
			"abcdefghijklmnopqrstuvwxyz01234",
			"abcdefghijklmnopqrstuvwxyz0123日",
			"abcdefghijklmnopqrstuvwxyz012日本",
			"日本語日本語日本語日本語日本語日本語日本語日",
			"\x1b[41m full width red background          \x1b[0m",
		}
		for i := range 120 {
			line := samples[(i*7+i/5)%len(samples)]
			text.lines = append(text.lines, fmt.Sprintf("%02d ", i%100)+line)
			if i%11 == 3 {
				text.lines = append(text.lines, "", line, line)
			}
			s.frame()
			if i%30 == 29 {
				s.scrollBack(-6)
				s.scrollBack(3)
				s.ui.ScrollToBottom()
				s.frame()
			}
		}
	}},
	{name: "overlay-during-scroll", cols: 60, rows: 16, play: func(s *termSession, text, dock *termLines) {
		overlay := &termLines{lines: []string{"\x1b[1m+------------------------+\x1b[0m", "| overlay over the scroll |", "| 日本語 overlay row       |", "+------------------------+"}}
		var handle *OverlayHandle
		for i := range 90 {
			text.lines = append(text.lines, termStyled(i, fmt.Sprintf("transcript line %03d behind the overlay", i)))
			switch i {
			case 15:
				handle = s.ui.ShowOverlay(overlay, OverlaySpec{Anchor: "center", Width: &OverlayValue{Value: 30}}.Options())
			case 40:
				handle.Hide()
			case 50:
				handle = s.ui.ShowOverlay(overlay, OverlaySpec{Anchor: "bottom-right", Width: &OverlayValue{Value: 28}, NonCapturing: true}.Options())
			case 60:
				overlay.lines[1] = "| overlay changed in place |"
			case 75:
				handle.Hide()
			}
			s.frame()
			if i == 25 || i == 65 {
				s.scrollBack(-4)
				s.scrollBack(4)
			}
		}
	}},
	{name: "repeated-rows", cols: 36, rows: 13, play: func(s *termSession, text, dock *termLines) {
		for i := range 40 {
			text.lines = append(text.lines, "", "----------", "", fmt.Sprintf("item %d", i%3))
			if i%4 == 0 {
				text.lines = append(text.lines, "same", "same", "same", "same")
			}
			s.frame()
		}
		for i := range 20 {
			text.lines = slices.Delete(text.lines, i*3, i*3+2)
			s.frame()
			text.lines = slices.Insert(text.lines, i*5, "", "same")
			s.frame()
		}
	}},
}

// termKnownScenarios are findings: each is a minimal replay in which scrolling rows shows another screen than Pi's bytes in every engine,
// and test/term-engines requires it to keep doing so (an entry is deleted when the divergence is fixed). They are not part of the equality
// the other scenarios must hold.
var termKnownScenarios = []termScenario{
	// A row wider than the screen is clipped by the renderer, which drops the row's closing SGR reset along with the clipped text. The
	// attributes the row set stay on in the terminal and color the rows drawn after it, so which rows show them depends on which rows
	// each variant draws: Pi's bytes (B) draw every changed row, a scroll frame (A) moves the others.
	{name: "known-clipped-sgr-leak", cols: 24, rows: 8, unclipped: true, play: func(s *termSession, text, dock *termLines) {
		for i := range 40 {
			line := fmt.Sprintf("line %02d", i)
			if i%4 == 0 {
				line = "\x1b[3m" + line + " is wider than the screen"
			}
			text.lines = append(text.lines, line)
			s.frame()
		}
	}},
}

// termRandomMix is a seeded mix of the edits TestAltScreenScrolledFramesShowEveryRow makes: lines added, extended, deleted, inserted and
// changed (with wide text), the viewport scrolled, and the rows below the transcript changed.
func termRandomMix(seed uint64) termScenario {
	size := func() (*rand.Rand, int, int) {
		rng := rand.New(rand.NewPCG(seed, 205))
		return rng, 30 + rng.IntN(30), 6 + rng.IntN(20)
	}
	_, cols, rows := size()
	return termScenario{name: fmt.Sprintf("random-mix-%d", seed), cols: cols, rows: rows, dock: &termLines{lines: []string{"dock"}}, play: func(s *termSession, text, dock *termLines) {
		rng, _, _ := size() // each play starts the same stream
		for i := range rng.IntN(60) {
			text.lines = append(text.lines, fmt.Sprintf("line %d", i))
		}
		s.frame()
		styled := func(value string) string {
			switch kind := rng.Float64(); {
			case kind < 0.25:
				return ""
			case kind < 0.5:
				return "the same line"
			case kind < 0.75:
				return fmt.Sprintf("\x1b[4%d;1m%s\x1b[0m", rng.IntN(7), value)
			}
			return value
		}
		for step := range 80 {
			switch action := rng.Float64(); {
			case action < 0.35:
				for count := 1 + rng.IntN(4); count > 0; count-- {
					text.lines = append(text.lines, styled(fmt.Sprintf("added %d.%d.%d", seed, step, count)))
				}
			case action < 0.5 && len(text.lines) > 0:
				text.lines[len(text.lines)-1] += " more"
			case action < 0.6 && len(text.lines) > 0:
				at := rng.IntN(len(text.lines))
				text.lines = slices.Delete(text.lines, at, min(len(text.lines), at+1+rng.IntN(3)))
			case action < 0.7:
				text.lines = slices.Insert(text.lines, rng.IntN(len(text.lines)+1), styled(fmt.Sprintf("inserted %d", step)), "")
			case action < 0.8 && len(text.lines) > 0:
				text.lines[rng.IntN(len(text.lines))] = styled(fmt.Sprintf("changed %d %s", step, strings.Repeat("wide 日本語 ", rng.IntN(8))))
			case action < 0.9:
				s.ui.ScrollBy((1 + rng.IntN(5)) * []int{-1, 1}[rng.IntN(2)])
			default:
				dock.lines = []string{fmt.Sprintf("dock %d", step)}
				if rng.IntN(2) == 0 {
					dock.lines = append(dock.lines, "second row")
				}
			}
			s.frame()
		}
	}}
}

// playTermScenario plays sc once, as variant A (scroll regions) or, with scrollRows off, as variant B, recording its frames.
func playTermScenario(t *testing.T, sc termScenario, rec *termRecorder, scrollRows bool) {
	t.Helper()
	t.Setenv("PIG_TUI_SCROLL_ROWS", map[bool]string{true: "", false: "0"}[scrollRows])
	var out strings.Builder
	text, dock := &termLines{unclipped: sc.unclipped}, &termLines{}
	components := []Component{text}
	if sc.dock != nil {
		dock.lines = slices.Clone(sc.dock.lines)
		components = append(components, dock)
	}
	ui := newScrollTestScreen(t, &out, sc.cols, sc.rows, components...)
	session := &termSession{t: t, rec: rec, out: &out, ui: ui}
	rec.add(nil, out.String())
	out.Reset()
	sc.play(session, text, dock)
}

var termRegionRe = regexp.MustCompile(`\x1b\[\d+;\d+r`)

func TestAltScreenTermEngineCorpus(t *testing.T) {
	var manifest termCorpusManifest
	add := func(name string, cols, rows int) *termCorpusScenario {
		sc := &termCorpusScenario{Name: name, Cols: cols, Rows: rows, Variants: map[string]*termCorpusVariant{
			"A": {File: name + ".A.bin"}, "B": {File: name + ".B.bin"},
		}}
		manifest.Scenarios = append(manifest.Scenarios, sc)
		return sc
	}
	scenarios := slices.Concat(termCorpusScenarios, termKnownScenarios)
	for seed := range uint64(6) {
		scenarios = append(scenarios, termRandomMix(seed))
	}
	for _, sc := range scenarios {
		recorded := add(sc.name, sc.cols, sc.rows)
		t.Run(sc.name+"/A", func(t *testing.T) { playTermScenario(t, sc, &termRecorder{recorded.Variants["A"]}, true) })
		t.Run(sc.name+"/B", func(t *testing.T) { playTermScenario(t, sc, &termRecorder{recorded.Variants["B"]}, false) })
	}

	// The fixture's scenarios: the frames of the pinned Pi's TuiAltScreen run, replayed as A (scroll regions) and B (Pi's bytes, which the
	// replay also compares with Pi's). A scenario on a screen narrower or lower than 2 cells, or resized to one, is left out: emulators
	// do not give a terminal that size. So is one whose components render a line wider than the screen: the clipped line leaks its attributes
	// into the rows after it (known-clipped-sgr-leak), so its A and B differ by that, not by what D205 scrolls.
	oracle := map[string]map[int]*termCorpusScenario{"A": {}, "B": {}}
	clipped := map[int]bool{} // scenarios in which a component rendered a line wider than the screen
	for _, variant := range []string{"A", "B"} {
		t.Run("oracle/"+variant, func(t *testing.T) {
			tap := &oracleTap{
				recordOnly: true,
				start: func(scenario, cols, rows int, _ string, out string) {
					recorded := &termCorpusScenario{Name: fmt.Sprintf("oracle-%03d", scenario), Cols: cols, Rows: rows, Variants: map[string]*termCorpusVariant{variant: {File: fmt.Sprintf("oracle-%03d.%s.bin", scenario, variant)}}}
					oracle[variant][scenario] = recorded
					(&termRecorder{recorded.Variants[variant]}).add(nil, out)
				},
				step: func(scenario int, resize []int, overWide bool, out string) {
					clipped[scenario] = clipped[scenario] || overWide
					(&termRecorder{oracle[variant][scenario].Variants[variant]}).add(resize, out)
				},
			}
			if variant == "A" {
				checkAltScreenOracleIn(t, true, nil, tap)
			} else {
				checkAltScreenOracleIn(t, false, map[string]string{"PIG_TUI_SCROLL_ROWS": "0"}, tap)
			}
		})
	}
	leaking := 0
	for scenario := range len(oracle["A"]) {
		a, b := oracle["A"][scenario], oracle["B"][scenario]
		if a == nil || b == nil {
			t.Fatalf("oracle scenario %d was not replayed in both variants", scenario)
		}
		usable := a.Cols >= 2 && a.Rows >= 2
		for _, step := range a.Variants["A"].Steps {
			usable = usable && (step.Resize == nil || step.Resize[0] >= 2 && step.Resize[1] >= 2)
		}
		if !usable || clipped[scenario] {
			if usable {
				leaking++
			}
			continue
		}
		merged := add(a.Name, a.Cols, a.Rows)
		merged.Variants["A"], merged.Variants["B"] = a.Variants["A"], b.Variants["B"]
	}

	t.Logf("%d fixture scenarios render lines wider than the screen and are left out", leaking)

	// Every scenario has as many frames in A as in B, and the corpus exercises the feature: B (Pi's bytes) sets no scroll region, A sets
	// one in many frames, and the two write other bytes there.
	scrolledFrames, differing := 0, 0
	for _, sc := range manifest.Scenarios {
		a, b := sc.Variants["A"], sc.Variants["B"]
		if len(a.Steps) != len(b.Steps) || len(a.Steps) == 0 {
			t.Fatalf("%s: %d steps in A, %d in B", sc.Name, len(a.Steps), len(b.Steps))
		}
		if termRegionRe.Match(b.data) {
			t.Errorf("%s: variant B (PIG_TUI_SCROLL_ROWS=0) set a scroll region", sc.Name)
		}
		start := [2]int{}
		for i, stepA := range a.Steps {
			stepB := b.Steps[i]
			if !slices.Equal(stepA.Resize, stepB.Resize) {
				t.Fatalf("%s step %d: resize %v in A, %v in B", sc.Name, i, stepA.Resize, stepB.Resize)
			}
			frameA, frameB := a.data[start[0]:stepA.End], b.data[start[1]:stepB.End]
			if termRegionRe.Match(frameA) {
				scrolledFrames++
			}
			if !bytes.Equal(frameA, frameB) {
				differing++
			}
			start = [2]int{stepA.End, stepB.End}
		}
	}
	if len(manifest.Scenarios) < 100 || scrolledFrames < 300 || differing < scrolledFrames {
		t.Fatalf("the corpus has %d scenarios, %d scrolled frames and %d frames that differ between A and B", len(manifest.Scenarios), scrolledFrames, differing)
	}
	t.Logf("%d scenarios, %d scrolled frames, %d frames that differ between A and B", len(manifest.Scenarios), scrolledFrames, differing)

	dir := os.Getenv(termCorpusDirEnv)
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, sc := range manifest.Scenarios {
		for _, v := range sc.Variants {
			if err := os.WriteFile(filepath.Join(dir, v.File), v.data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	var plan strings.Builder
	for _, sc := range manifest.Scenarios {
		fmt.Fprintf(&plan, "scenario\t%s\t%d\t%d\n", sc.Name, sc.Cols, sc.Rows)
		for _, name := range []string{"A", "B"} {
			v := sc.Variants[name]
			fmt.Fprintf(&plan, "variant\t%s\t%s\n", name, v.File)
			for _, step := range v.Steps {
				if step.Resize != nil {
					fmt.Fprintf(&plan, "step\t%d\t%d\t%d\n", step.End, step.Resize[0], step.Resize[1])
				} else {
					fmt.Fprintf(&plan, "step\t%d\n", step.End)
				}
			}
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "plan.txt"), []byte(plan.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}
