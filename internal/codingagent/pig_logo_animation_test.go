package codingagent

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"maps"
	"math"
	"math/bits"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/piglogin"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/tui"
)

// logoTestClock is a manual clock for the animation.
type logoTestClock struct{ now time.Time }

func (c *logoTestClock) at(ms float64) {
	c.now = time.Unix(0, 0).Add(time.Duration(ms * float64(time.Millisecond)))
}

func newTestPigLogoAnimation(t *testing.T, clock *logoTestClock, rows int, options pigLogoAnimationOptions, variant piglogin.Variant, foreground, background logoRgb) (*pigLogoAnimation, *int) {
	t.Helper()
	clock.at(0)
	done := 0
	a := newPigLogoAnimation(func() int { return rows }, options, variant, foreground, background, func() time.Time { return clock.now }, func() { done++ })
	return a, &done
}

// logoTestScreen is a fullscreen frame with the PiG header (the pig head, as the header draws it) and lines that exercise
// the SGR parser: basic, bright, 256-color and truecolor colors in both syntaxes, dim, inverse, resets, an OSC 8 link, an
// APC, a cursor sequence, wide graphemes, box drawing and punctuation.
func logoTestScreen() []string {
	head := piglogin.HeadLines(piglogin.Default(), tui.TerminalColorModeTrueColor)
	beside := []string{
		" \x1b[2mv" + pigversion.Version + "\x1b[22m",
		" \x1b[38;5;244mescape\x1b[39m \x1b[90minterrupt\x1b[0m · \x1b[2;38;2;120;130;140mctrl+c\x1b[0m",
		" \x1b[2mPress ctrl+o to show full startup help.\x1b[0m",
	}
	screen := []string{""}
	for i, line := range head {
		if i < len(beside) {
			line += beside[i]
		}
		screen = append(screen, " "+line)
	}
	return append(screen,
		"\x1b[44;97m user message with a background \x1b[49;39m tail",
		"\x1b[7m inverse \x1b[27m \x1b[7;31;42m both \x1b[0m",
		"\x1b[38:2::255:100:50mcolon truecolor\x1b[38:5:33m colon 256\x1b[0m",
		"\x1b]8;;https://example.com\x07link\x1b]8;;\x07 and \x1b_pi:c\x1b\\apc \x1b[2Kcursor",
		"日本語の文字 wide 🙂 emoji",
		"┌──────┐ .,:;'`-_· ok",
		"\x1b[48;5;236m\x1b[38;5;196m256 bg and fg\x1b[0m",
		"\x1b[1;4;3m bold underline italic \x1b[0m",
		"plain text after everything",
	)
}

type logoOracleStep struct {
	Ms      float64      `json:"ms"`
	Kind    string       `json:"kind"`
	Data    string       `json:"data,omitempty"`
	Type    string       `json:"type,omitempty"`
	Offsets [][3]float64 `json:"offsets,omitempty"`
	Colors  []logoRgb    `json:"colors,omitempty"`
}

type logoOracleCase struct {
	name           string
	width, height  int
	logoColumn     int
	logoRow        int
	textMark       bool
	variant        piglogin.Variant
	foreground, bg logoRgb
	appearance     string
	renderMs       []float64
	closeMs        float64
	closeKind      string
}

// TestPigLogoAnimationMatchesPinnedPi runs the pinned pi-logo-animation.ts with the pig's blocks and geometry and compares
// every frame with the Go port: dissolve, flight, spin, starfield, hint, halo, the running pig's moved blocks, the exit and
// its skip. Pi has no tests for this component (no file under packages/coding-agent/test references it).
func TestPigLogoAnimationMatchesPinnedPi(t *testing.T) {
	entry := []float64{0, 16, 50, 100, 150, 200, 260, 350, 500, 700, 900, 1100, 1300, 1450, 1700, 2000, 2600, 3200, 4000, 4399, 4400, 4500, 4700, 5000, 5300, 6100, 7000, 8000, 8900, 9300, 9700, 10300, 12000}
	cases := []logoOracleCase{
		{name: "dark full timeline", width: 100, height: 30, logoColumn: 1, logoRow: 1, variant: piglogin.Default(), foreground: logoRgb{220, 220, 220}, bg: logoRgb{12, 14, 18}, appearance: "dark", renderMs: entry, closeMs: 10500, closeKind: "key"},
		{name: "light halo and sheriff", width: 80, height: 24, logoColumn: 1, logoRow: 1, variant: piglogin.FindVariant("sheriff"), foreground: logoRgb{30, 30, 30}, bg: logoRgb{250, 250, 245}, appearance: "light", renderMs: entry[:24], closeMs: 5200, closeKind: "click"},
		{name: "early exit during the flight", width: 60, height: 18, logoColumn: 3, logoRow: 2, variant: piglogin.FindVariant("pink"), foreground: logoRgb{200, 200, 200}, bg: logoRgb{0, 0, 0}, appearance: "dark", renderMs: []float64{0, 100, 300, 600}, closeMs: 650, closeKind: "ctrl+c"},
		{name: "narrow screen hides the hint", width: 14, height: 8, logoColumn: 1, logoRow: 1, variant: piglogin.Default(), foreground: logoRgb{220, 220, 220}, bg: logoRgb{20, 20, 20}, appearance: "dark", renderMs: []float64{0, 400, 1500, 2600, 3000}, closeMs: 3100, closeKind: "key"},
		{name: "text mark and kratos, exit while running", width: 90, height: 26, logoColumn: 1, logoRow: 1, textMark: true, variant: piglogin.FindVariant("kratos"), foreground: logoRgb{220, 220, 220}, bg: logoRgb{12, 14, 18}, appearance: "dark", renderMs: []float64{0, 700, 4400, 4500, 4560, 4700, 6000}, closeMs: 6100, closeKind: "key"},
	}
	colors := tui.ActiveTheme().ColorValues()
	theme := map[string]logoRgb{"text": logoToRgb(colors["text"]), "muted": logoToRgb(colors["muted"]), "dim": logoToRgb(colors["dim"])}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clock := &logoTestClock{}
			options := pigLogoAnimationOptions{screen: logoTestScreen(), logoColumn: tc.logoColumn, logoRow: tc.logoRow, clearColumns: piglogin.HeadCells, clearRows: piglogin.HeadRows}
			if tc.textMark {
				options.clearColumns, options.clearRows = piglogin.TextMarkWidth, 1
			}
			a, done := newTestPigLogoAnimation(t, clock, tc.height, options, tc.variant, tc.foreground, tc.bg)
			var steps []logoOracleStep
			var want []any
			offsetsAt := func(ms float64) [][3]float64 {
				if ms/1000 < logoPuzzleStart {
					return nil
				}
				return a.blockOffsets(ms / 1000)
			}
			// The blocks keep their own colors; the running pig is PiG's layer over Pi's frame (TestPigLogoRunCycle).
			colorsNow := func() []logoRgb { return nil }
			render := func() []string { return a.renderFrame(tc.width, nil) }
			for _, ms := range tc.renderMs {
				clock.at(ms)
				steps = append(steps, logoOracleStep{Ms: ms, Kind: "render", Offsets: offsetsAt(ms), Colors: colorsNow()})
				want = append(want, render())
			}
			closeMs := tc.closeMs
			closeStep := func(ms float64) {
				clock.at(ms)
				switch tc.closeKind {
				case "key":
					steps = append(steps, logoOracleStep{Ms: ms, Kind: "key", Data: "\x1b", Offsets: offsetsAt(ms)})
					a.HandleInput("\x1b")
					want = append(want, *done)
				case "ctrl+c":
					steps = append(steps, logoOracleStep{Ms: ms, Kind: "key", Data: "\x03", Offsets: offsetsAt(ms)})
					a.HandleInput("\x03")
					want = append(want, *done)
				case "click":
					steps = append(steps, logoOracleStep{Ms: ms, Kind: "click", Type: "click", Offsets: offsetsAt(ms)})
					result := a.HandleMouse(tui.TuiMouseEvent{Type: tui.MouseClick})
					want = append(want, map[string]any{"handled": result.Handled, "render": *result.Render}, *done)
				}
			}
			// Keys and mouse events that do not close the animation.
			clock.at(closeMs - 1)
			steps = append(steps, logoOracleStep{Ms: closeMs - 1, Kind: "key", Data: "q"}, logoOracleStep{Ms: closeMs - 1, Kind: "click", Type: "press"})
			a.HandleInput("q")
			want = append(want, *done)
			press := a.HandleMouse(tui.TuiMouseEvent{Type: tui.MousePress})
			want = append(want, map[string]any{"handled": press.Handled, "render": *press.Render}, *done)
			closeStep(closeMs)
			for _, after := range []float64{0, 60, 200, 330, 550, 700, 880, 1000, 1099, 1100, 1300} {
				ms := closeMs + after
				clock.at(ms)
				steps = append(steps, logoOracleStep{Ms: ms, Kind: "render", Colors: colorsNow()})
				want = append(want, render())
			}
			steps = append(steps, logoOracleStep{Ms: closeMs + 1300, Kind: "invalidate"})
			a.Invalidate()
			want = append(want, nil)
			steps = append(steps, logoOracleStep{Ms: closeMs + 1300, Kind: "render", Colors: colorsNow()})
			want = append(want, render())
			// A second close skips the exit animation.
			closeStep(closeMs + 1300)

			input := map[string]any{
				"upstream": pigversion.UpstreamVersion, "width": tc.width, "height": tc.height,
				"screen": options.screen, "logoColumn": tc.logoColumn, "logoRow": tc.logoRow,
				"foreground": tc.foreground, "background": tc.bg, "appearance": tc.appearance,
				"theme": theme, "colorMode": string(tui.ActiveTheme().ColorMode()),
				"blocks": oracleBlocks(a.blocks), "radius": pigLogoRadius,
				"centerX": pigLogoCenterX, "centerY": pigLogoCenterY, "pixel": pigLogoPixel,
				"headCells": piglogin.HeadCells, "headRows": piglogin.HeadRows,
				"clearColumns": options.clearColumns, "clearRows": options.clearRows,
				"steps": steps,
			}
			payload, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			command := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/pig-logo-animation-oracle.mjs")
			command.Stdin = bytes.NewReader(payload)
			var stderr bytes.Buffer
			command.Stderr = &stderr
			output, err := command.Output()
			if err != nil {
				t.Fatalf("pinned Pi oracle: %v\n%s", err, stderr.String())
			}
			var got []any
			if err := json.Unmarshal(output, &got); err != nil {
				t.Fatal(err)
			}
			wantJSON, err := json.Marshal(want)
			if err != nil {
				t.Fatal(err)
			}
			var normalizedWant []any
			if err := json.Unmarshal(wantJSON, &normalizedWant); err != nil {
				t.Fatal(err)
			}
			if len(got) != len(normalizedWant) {
				t.Fatalf("oracle returned %d results, want %d", len(got), len(normalizedWant))
			}
			for i := range got {
				g, _ := json.Marshal(got[i])
				w, _ := json.Marshal(normalizedWant[i])
				if !bytes.Equal(g, w) {
					t.Errorf("step %d (%s at %v ms) differs from Pi:\n%s", i, steps[min(i, len(steps)-1)].Kind, steps[min(i, len(steps)-1)].Ms, describeLogoDiff(got[i], normalizedWant[i]))
				}
			}
		})
	}
}

func oracleBlocks(blocks []logoBlock) []map[string]any {
	result := make([]map[string]any, len(blocks))
	for i, block := range blocks {
		result[i] = map[string]any{"home": block.home, "color": block.color}
	}
	return result
}

func describeLogoDiff(got, want any) string {
	gotLines, ok1 := got.([]any)
	wantLines, ok2 := want.([]any)
	if !ok1 || !ok2 || len(gotLines) != len(wantLines) {
		return fmt.Sprintf("pi=%v\ngo=%v", got, want)
	}
	var out strings.Builder
	for row := range gotLines {
		if gotLines[row] != wantLines[row] {
			fmt.Fprintf(&out, "row %d:\n pi=%q\n go=%q\n", row, gotLines[row], wantLines[row])
		}
	}
	return out.String()
}

// newLogoClickMode is a fullscreen interactive mode showing the built-in header, as Run mounts it. It renders the header in
// the active theme's color mode, so a test that clicks the head pins truecolor first (pinHeaderTerminal).
func newLogoClickMode(t *testing.T, width, height int) (*InteractiveMode, *tui.TuiAltScreen) {
	t.Helper()
	isolatePigHome(t)
	km := &KeybindingsManager{definitions: appKeybindingDefinitions, ordered: appKeybindingOrder, platform: tui.HostKeybindingPlatform()}
	km.rebuild()
	var output synchronizedOutput
	alt := tui.NewTuiAltScreenWithOutput(&output, width, height, tui.TuiAltScreenOptions{})
	alt.SetCopyOnSelect(false)
	m := &InteractiveMode{
		opts:          InteractiveOptions{LoginVisible: true},
		keybindings:   km,
		extHeader:     newSpecialLinesComponent(nil),
		tuiInst:       alt,
		altScreen:     alt,
		editor:        tui.NewEditor(),
		isIdle:        true,
		chatContainer: tui.NewContainer(),
		uiTaskCh:      make(chan func(), 64),
	}
	m.backgroundCtx, m.backgroundCancel = context.WithCancel(t.Context())
	// Scheduled renders run on the owner loop, as Run's dispatchScheduledRender delivers them.
	ctx := m.backgroundCtx
	alt.SetRenderDispatcher(func(render func()) {
		select {
		case m.uiTaskCh <- render:
		case <-ctx.Done():
		}
	})
	t.Cleanup(func() {
		m.backgroundCancel()
		m.disposeLogoAnimation()
		m.backgroundTasks.Wait()
		alt.Stop()
	})
	m.restoreBuiltInHeader()
	alt.Add(tui.NewContainer(m.headerContainer(), m.chatContainer))
	alt.Start()
	alt.Render()
	return m, alt
}

// runOwnerTasks runs owner-loop tasks until done reports true.
func runOwnerTasks(t *testing.T, m *InteractiveMode, done func() bool) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for !done() {
		select {
		case task := <-m.uiTaskCh:
			task()
		case <-deadline:
			t.Fatal("owner-loop work did not finish")
		}
	}
}

// headLogoOptions are the options of a click on the header's pig head at (1, 1).
func headLogoOptions() pigLogoAnimationOptions {
	return pigLogoAnimationOptions{logoColumn: 1, logoRow: 1, clearColumns: piglogin.HeadCells, clearRows: piglogin.HeadRows}
}

func clickCell(t *testing.T, m *InteractiveMode, column, row int) {
	t.Helper()
	for _, sequence := range []string{fmt.Sprintf("\x1b[<0;%d;%dM", column+1, row+1), fmt.Sprintf("\x1b[<0;%d;%dm", column+1, row+1)} {
		if err := m.dispatchKey(t.Context(), sequence); err != nil {
			t.Fatal(err)
		}
	}
}

// interactive-mode.ts BuiltInHeader.handleMouse and pi-logo-animation(.lazy).ts: a click on the header logo opens the
// fullscreen animation, escape plays the exit, and the overlay closes and returns focus once the pig has landed.
func TestClickingTheHeaderPigPlaysTheAnimation(t *testing.T) {
	pinHeaderTerminal(t, tui.TerminalColorModeTrueColor)
	m, alt := newLogoClickMode(t, 100, 30)
	// The header starts on the second screen row (after the spacer); the pig head is its cells 1 to HeadCells on its first
	// HeadRows lines.
	clickCell(t, m, 1+piglogin.HeadCells, 1)
	if m.logoAnimationPlaying {
		t.Fatal("a click on the version started the animation")
	}
	clickCell(t, m, 3, 2)
	if !m.logoAnimationPlaying {
		t.Fatal("a click on the pig did not start the animation")
	}
	runOwnerTasks(t, m, func() bool { return m.logoAnimation != nil })
	if !alt.HasOverlay() || alt.FocusedComponent() != tui.Component(m.logoAnimation) {
		t.Fatal("the animation is not a focused overlay")
	}
	animation := m.logoAnimation
	if o := animation.options; o.logoColumn != 1 || o.logoRow != 1 || o.clearColumns != piglogin.HeadCells || o.clearRows != piglogin.HeadRows {
		t.Fatalf("logo = (%d, %d) %dx%d, want the head at (1, 1)", o.logoColumn, o.logoRow, o.clearColumns, o.clearRows)
	}
	head := piglogin.HeadLines(piglogin.Default(), tui.TerminalColorModeTrueColor)
	if len(animation.options.screen) != 30 || !strings.HasPrefix(stripANSITest(animation.options.screen[1]), " "+stripANSITest(head[0])) {
		t.Fatalf("the captured screen is not the rendered frame: %q", animation.options.screen[:3])
	}
	// A second click on the header while it plays does nothing: the drawn overlay takes the mouse.
	alt.Render()
	clickCell(t, m, 3, 2)
	if m.logoAnimation != animation || animation.exit == nil {
		t.Fatal("a click on the overlay did not start the exit")
	}
	if err := m.dispatchKey(t.Context(), "\x1b"); err != nil {
		t.Fatal(err)
	}
	runOwnerTasks(t, m, func() bool { return !m.logoAnimationPlaying })
	if alt.HasOverlay() || m.logoAnimation != nil {
		t.Fatal("skipping the exit left the overlay open")
	}
	select {
	case <-animation.timerDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the frame timer kept running")
	}
}

// interactive-mode.ts BuiltInHeader.handleMouse accepts a click on every cell of the logo's two lines after the padding
// column (x 1 to 4 of Pi's 4-cell logo). D87 widens that to every cell of PiG's pig head (x 1 to piglogin.HeadCells on its
// piglogin.HeadRows lines), so each of its cells plays the animation and the padding column, the gap before the text, the
// spacer above and the line below do not.
func TestEveryCellOfTheHeaderPigPlaysTheAnimation(t *testing.T) {
	pinHeaderTerminal(t, tui.TerminalColorModeTrueColor)
	m, alt := newLogoClickMode(t, 100, 30)
	below := 1 + piglogin.HeadRows
	for _, cell := range [][2]int{{0, 1}, {0, below - 1}, {1 + piglogin.HeadCells, 1}, {1 + piglogin.HeadCells, below - 1}, {1, below}, {piglogin.HeadCells, below}, {1, 0}} {
		clickCell(t, m, cell[0], cell[1])
		if m.logoAnimationPlaying {
			t.Fatalf("a click on screen cell %v outside the pig played the animation", cell)
		}
	}
	for row := 1; row < below; row++ {
		for column := 1; column <= piglogin.HeadCells; column++ {
			// A quick second press on the same word selects it (tui-alt-screen.ts getClickCount); a press on the blank
			// spacer line between keeps each click a single click.
			clickCell(t, m, 99, 0)
			clickCell(t, m, column, row)
			if !m.logoAnimationPlaying {
				t.Fatalf("a click on screen cell (%d, %d) of the pig did not play the animation", column, row)
			}
			runOwnerTasks(t, m, func() bool { return m.logoAnimation != nil })
			// Close twice: the second close skips the exit, so the overlay hides and the next click can play.
			m.logoAnimation.close()
			m.logoAnimation.close()
			if m.logoAnimationPlaying || alt.HasOverlay() {
				t.Fatal("skipping the exit left the animation playing")
			}
			alt.Render()
		}
	}
}

// Pi sets onLogoClick only on its built-in header when it draws the logo (interactive-mode.ts:1058), and an extension's
// setHeader replaces that component. PiG's pig is clickable only while the built-in header draws the sprite.
func TestHeaderPigClickNeedsTheBuiltInSprite(t *testing.T) {
	pinHeaderTerminal(t, tui.TerminalColorModeTrueColor)
	t.Run("extension header", func(t *testing.T) {
		m, _ := newLogoClickMode(t, 100, 30)
		ui := &ExtUIContext{m: m}
		ui.SetHeader([]string{"custom header", "second line"})
		m.tuiInst.Render()
		clickCell(t, m, 3, 2)
		if m.logoAnimationPlaying {
			t.Fatal("a replaced header played the animation")
		}
		// setHeader(undefined) restores the built-in header and its pig.
		ui.SetHeader(nil)
		m.tuiInst.Render()
		clickCell(t, m, 3, 2)
		if !m.logoAnimationPlaying {
			t.Fatal("the restored built-in header did not play the animation")
		}
	})
	// Pi draws no logo in Apple Terminal (supportsPiLogo) and sets no onLogoClick there; the text mark stands in for its
	// wordmark.
	t.Run("text mark in Apple Terminal", func(t *testing.T) {
		previous := supportsHalfBlockMark
		supportsHalfBlockMark = func() bool { return false }
		t.Cleanup(func() { supportsHalfBlockMark = previous })
		m, _ := newLogoClickMode(t, 100, 30)
		for column := 1; column <= piglogin.HeadCells; column++ {
			for row := 1; row <= piglogin.HeadRows; row++ {
				clickCell(t, m, column, row)
			}
		}
		if m.logoAnimationPlaying {
			t.Fatal("the Apple Terminal text mark played the animation")
		}
	})
	t.Run("regular mode", func(t *testing.T) {
		m := newHeaderMode(t)
		m.handleBuiltInHeaderMouse(tui.TuiMouseEvent{Type: tui.MouseClick, X: 2, Y: 0, ScreenX: 3, ScreenY: 1})
		if m.logoAnimationPlaying {
			t.Fatal("the main-screen renderer played the animation")
		}
	})
	t.Run("press and other buttons' events", func(t *testing.T) {
		m, _ := newLogoClickMode(t, 100, 30)
		for _, event := range []tui.TuiMouseEventType{tui.MousePress, tui.MouseRelease, tui.MouseMove, tui.MouseWheel} {
			if result := m.handleBuiltInHeaderMouse(tui.TuiMouseEvent{Type: event, X: 2, Y: 0, ScreenX: 3, ScreenY: 1}); result != nil || m.logoAnimationPlaying {
				t.Fatalf("%s on the pig was handled", event)
			}
		}
		for _, cell := range [][2]int{{0, 0}, {piglogin.HeadCells + 1, 0}, {2, piglogin.HeadRows}} {
			if result := m.handleBuiltInHeaderMouse(tui.TuiMouseEvent{Type: tui.MouseClick, X: cell[0], Y: cell[1]}); result != nil {
				t.Fatalf("a click at %v outside the pig was handled", cell)
			}
		}
	})
}

// Pi draws its logo in every color mode and width, so it is clickable there. PiG's text mark stands in for it where the
// head cannot be drawn (no truecolor, or too narrow), and a click on its 4 cells, and only there, plays the animation.
func TestClickingTheTextMarkPlaysTheAnimation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		width int
		mode  tui.TerminalColorMode
	}{{"256 colors", 100, tui.TerminalColorMode256}, {"narrow", 24, tui.TerminalColorModeTrueColor}} {
		t.Run(tc.name, func(t *testing.T) {
			pinHeaderTerminal(t, tc.mode)
			m, alt := newLogoClickMode(t, tc.width, 30)
			if line := stripANSITest(alt.GetScreenLines()[1]); !strings.HasPrefix(line, " PiG. v") {
				t.Fatalf("the header does not draw the text mark: %q", line)
			}
			for _, cell := range [][2]int{{0, 1}, {1 + piglogin.TextMarkWidth, 1}, {1, 2}} {
				clickCell(t, m, cell[0], cell[1])
				if m.logoAnimationPlaying {
					t.Fatalf("a click on %v outside the text mark played the animation", cell)
				}
			}
			for column := 1; column <= piglogin.TextMarkWidth; column++ {
				// A quick second press on the same word selects it (tui-alt-screen.ts getClickCount); a press on the blank
				// spacer line between keeps each click a single click.
				clickCell(t, m, tc.width-1, 0)
				clickCell(t, m, column, 1)
				if !m.logoAnimationPlaying {
					t.Fatalf("a click on cell %d of the text mark did not play the animation", column)
				}
				runOwnerTasks(t, m, func() bool { return m.logoAnimation != nil })
				if o := m.logoAnimation.options; o.logoColumn != 1 || o.logoRow != 1 || o.clearColumns != piglogin.TextMarkWidth || o.clearRows != 1 {
					t.Fatalf("logo = (%d, %d) %dx%d, want the text mark at (1, 1)", o.logoColumn, o.logoRow, o.clearColumns, o.clearRows)
				}
				m.logoAnimation.close()
				m.logoAnimation.close()
				alt.Render()
			}
		})
	}
}

// pi-logo-animation.lazy.ts returns while an overlay is open, playPiLogoAnimation while one plays, and it shows nothing
// when an overlay opened while it waited for the terminal's colors.
func TestHeaderPigClickIgnoredWhileAnOverlayOrTheAnimationIsOpen(t *testing.T) {
	pinHeaderTerminal(t, tui.TerminalColorModeTrueColor)
	m, alt := newLogoClickMode(t, 100, 30)
	other := alt.OpenOverlay(tui.NewText("dialog"), tui.OverlaySpec{Anchor: "center"}.Options())
	m.playPigLogoAnimation(1, 1, piglogin.HeadCells, piglogin.HeadRows)
	if m.logoAnimationPlaying {
		t.Fatal("played over an open overlay")
	}
	other.Hide()

	m.playPigLogoAnimation(1, 1, piglogin.HeadCells, piglogin.HeadRows)
	m.playPigLogoAnimation(1, 1, piglogin.HeadCells, piglogin.HeadRows)
	runOwnerTasks(t, m, func() bool { return m.logoAnimation != nil })
	first := m.logoAnimation
	select {
	case task := <-m.uiTaskCh:
		task()
		if m.logoAnimation != first {
			t.Fatal("a second play replaced the animation")
		}
	case <-time.After(300 * time.Millisecond):
	}

	m2, alt2 := newLogoClickMode(t, 100, 30)
	m2.playPigLogoAnimation(1, 1, piglogin.HeadCells, piglogin.HeadRows)
	dialog := alt2.OpenOverlay(tui.NewText("dialog"), tui.OverlaySpec{Anchor: "center"}.Options())
	runOwnerTasks(t, m2, func() bool { return !m2.logoAnimationPlaying })
	if m2.logoAnimation != nil || alt2.FocusedComponent() == nil {
		t.Fatal("the animation opened over a newer overlay")
	}
	dialog.Hide()
}

// playPiLogoAnimation fades toward the terminal's reported default colors, and falls back to the theme's text color and
// black or white by the theme's appearance.
func TestPigLogoAnimationColors(t *testing.T) {
	pinHeaderTerminal(t, tui.TerminalColorModeTrueColor)
	m, _ := newLogoClickMode(t, 100, 30)
	screen := m.altScreen.GetScreenLines()
	m.logoAnimationPlaying = true
	m.showPigLogoAnimation(m.tuiInst, screen, headLogoOptions(), tui.TerminalColorsResult{Colors: tui.TerminalColors{
		Foreground: &tui.RgbColor{R: 1, G: 2, B: 3}, Background: &tui.RgbColor{R: 4, G: 5, B: 6},
	}})
	if a := m.logoAnimation; a == nil || a.foreground != (logoRgb{1, 2, 3}) || a.background != (logoRgb{4, 5, 6}) {
		t.Fatalf("reported colors not used: %+v", m.logoAnimation)
	}
	m.disposeLogoAnimation()
	m.tuiInst.(*tui.TuiAltScreen).Stop()

	m, _ = newLogoClickMode(t, 100, 30)
	m.logoAnimationPlaying = true
	m.showPigLogoAnimation(m.tuiInst, nil, headLogoOptions(), tui.TerminalColorsResult{})
	theme := tui.ActiveTheme()
	wantBackground := logoRgb{255, 255, 255}
	if theme.Appearance() == "dark" {
		wantBackground = logoRgb{0, 0, 0}
	}
	if a := m.logoAnimation; a == nil || a.foreground != logoToRgb(theme.ColorValues()["text"]) || a.background != wantBackground {
		t.Fatalf("fallback colors: %+v", m.logoAnimation)
	}
	m.disposeLogoAnimation()

	// A failed query shows nothing and lets the next click play.
	m, alt := newLogoClickMode(t, 100, 30)
	m.logoAnimationPlaying = true
	m.showPigLogoAnimation(m.tuiInst, nil, headLogoOptions(), tui.TerminalColorsResult{Err: fmt.Errorf("write failed")})
	if m.logoAnimationPlaying || alt.HasOverlay() {
		t.Fatal("a failed color query showed the animation")
	}
}

// The frame timer stops the animation once the exit finished, or once nothing rendered it for a second; Dispose cancels
// and joins it without finishing.
func TestPigLogoAnimationTimer(t *testing.T) {
	start := func(t *testing.T) (*pigLogoAnimation, *logoTestClock, *int, chan func(), *int) {
		clock := &logoTestClock{}
		a, done := newTestPigLogoAnimation(t, clock, 24, pigLogoAnimationOptions{screen: logoTestScreen(), logoColumn: 1, logoRow: 1, clearColumns: piglogin.HeadCells, clearRows: piglogin.HeadRows}, piglogin.Default(), logoRgb{200, 200, 200}, logoRgb{0, 0, 0})
		posted := make(chan func())
		renders := 0
		a.startTimer(t.Context(), func(task func()) { go task() }, func(ctx context.Context, fn func()) error {
			select {
			case posted <- fn:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}, func() { renders++ })
		t.Cleanup(a.Dispose)
		return a, clock, done, posted, &renders
	}
	t.Run("exit", func(t *testing.T) {
		a, clock, done, posted, renders := start(t)
		clock.at(500)
		a.Render(80)
		(<-posted)()
		if *renders != 1 || *done != 0 {
			t.Fatalf("renders=%d done=%d", *renders, *done)
		}
		a.close()
		clock.at(500 + 1099)
		a.Render(80)
		(<-posted)()
		if *done != 0 {
			t.Fatal("finished before the exit ended")
		}
		clock.at(500 + 1100)
		(<-posted)()
		if *done != 1 || *renders != 2 {
			t.Fatalf("exit end: renders=%d done=%d", *renders, *done)
		}
		select {
		case <-a.timerDone:
		case <-time.After(5 * time.Second):
			t.Fatal("timer kept running after finish")
		}
		a.close()
		if *done != 1 {
			t.Fatal("finish ran twice")
		}
	})
	t.Run("not rendered", func(t *testing.T) {
		a, clock, done, posted, _ := start(t)
		a.Render(80)
		clock.at(1000)
		(<-posted)()
		if *done != 0 {
			t.Fatal("stopped after exactly one second")
		}
		clock.at(1001)
		(<-posted)()
		if *done != 1 {
			t.Fatal("an unrendered animation kept running")
		}
	})
	t.Run("dispose", func(t *testing.T) {
		a, _, done, posted, renders := start(t)
		fn := <-posted
		a.Dispose()
		a.Dispose()
		fn()
		if *done != 0 || *renders != 0 {
			t.Fatal("a disposed animation ran a queued tick")
		}
		select {
		case <-a.timerDone:
		default:
			t.Fatal("Dispose did not join the timer")
		}
	})
}

// PiG's art (D87): the animation's object is the header's pig head in the sprite's colors, never Pi's logo.
func TestPigLogoAnimationDrawsThePigNotPisLogo(t *testing.T) {
	for _, variant := range piglogin.Variants {
		blocks := pigLogoBlocks(variant)
		pixels := piglogin.HeadPixels(variant)
		if len(blocks) != len(pixels) || len(blocks) == 0 {
			t.Fatalf("%s: %d blocks for %d pixels", variant.ID, len(blocks), len(pixels))
		}
		for i, pixel := range pixels {
			want := logoRgb{float64(pixel.Color.R), float64(pixel.Color.G), float64(pixel.Color.B)}
			if blocks[i].home != (logoCell3{float64(pixel.X), float64(pixel.Y), 0}) || blocks[i].color != want {
				t.Fatalf("%s block %d = %+v, want pixel %+v", variant.ID, i, blocks[i], pixel)
			}
		}
		clock := &logoTestClock{}
		a, _ := newTestPigLogoAnimation(t, clock, 30, pigLogoAnimationOptions{screen: logoTestScreen(), logoColumn: 1, logoRow: 1, clearColumns: piglogin.HeadCells, clearRows: piglogin.HeadRows}, variant, logoRgb{220, 220, 220}, logoRgb{0, 0, 0})
		for _, ms := range []float64{0, 700, 2000, 5000, 6700} {
			clock.at(ms)
			assertNoPiLogo(t, fmt.Sprintf("%s at %v ms", variant.ID, ms), a.Render(100))
		}
	}
	// At the start the 3D pig covers exactly the header head's HeadCells by HeadRows cells.
	clock := &logoTestClock{}
	a, _ := newTestPigLogoAnimation(t, clock, 30, pigLogoAnimationOptions{screen: logoTestScreen(), logoColumn: 1, logoRow: 1, clearColumns: piglogin.HeadCells, clearRows: piglogin.HeadRows}, piglogin.Default(), logoRgb{220, 220, 220}, logoRgb{0, 0, 0})
	frame := a.Render(100)
	head := piglogin.HeadFor(piglogin.Default())
	for row := 1; row <= piglogin.HeadRows; row++ {
		line := []rune(stripANSITest(frame[row]))
		for column := 1; column <= piglogin.HeadCells; column++ {
			opaque := head[(row-1)*2][column-1] != '.' || head[(row-1)*2+1][column-1] != '.'
			braille := line[column] > 0x2800 && line[column] <= 0x28ff
			if opaque != braille {
				t.Fatalf("row %d column %d = %q, want the pig's braille exactly where the head draws a pixel", row, column, line[column])
			}
		}
		if line[0] != ' ' || line[piglogin.HeadCells+1] != ' ' {
			t.Fatalf("row %d: the pig spills out of its cells: %q", row, string(line[:piglogin.HeadCells+2]))
		}
	}
}

// The running pig (D87): it runs in place on Pi's puzzle timeline in place of the head, ray cast in braille like the
// head, through four frames whose legs move and whose body bobs; it turns to face the camera and back to the head's spin,
// the head is back for Pi's return and hold, and the exit drops the runner.
func TestPigLogoRunCycle(t *testing.T) {
	clock := &logoTestClock{}
	const width, height = 120, 30
	a, _ := newTestPigLogoAnimation(t, clock, height, headLogoOptions(), piglogin.Default(), logoRgb{220, 220, 220}, logoRgb{0, 0, 0})
	for cycle := range 3 {
		base := logoPuzzleStart + float64(cycle)*logoPuzzleCycle
		if a.runner(base-0.01) != nil || a.runner(base+logoShuffleEnd+0.01) != nil {
			t.Fatalf("cycle %d: the pig runs outside Pi's shuffle", cycle)
		}
		first, middle, last := a.runner(base+0.01), a.runner(base+logoShuffleEnd/2), a.runner(base+logoShuffleEnd-0.01)
		if first == nil || middle == nil || last == nil {
			t.Fatalf("cycle %d: the pig does not run through Pi's shuffle", cycle)
		}
		// It dissolves in and out over pigRunFade while it turns from the head's spin to the camera and back, so the
		// object never jumps.
		if first.facing > 0.01 || last.facing > 0.01 || middle.facing != 1 || first.mix != first.facing || middle.mix != 1 {
			t.Fatalf("cycle %d: facing %v, %v, %v, want 0, 1, 0", cycle, first.facing, middle.facing, last.facing)
		}
		if half := a.runner(base + pigRunFade/2); math.Abs(half.mix-0.5) > 1e-9 {
			t.Fatalf("cycle %d: the dissolve is %v of the way at its middle", cycle, half.mix)
		}
		// Facing, the pig is square to the camera at the head's nearest whole turn; while it turns it is never more than
		// half a turn from the head.
		for _, time := range []float64{base + 0.01, base + pigRunFade/2, base + logoShuffleEnd/2, base + logoShuffleEnd - pigRunFade/2, base + logoShuffleEnd - 0.01} {
			head := a.pose(width, height, 1, time)
			pig := a.runner(time).pose(head)
			if math.Abs(pig.yaw-head.yaw) > math.Pi+1e-9 {
				t.Fatalf("cycle %d at %.2f s: the pig's yaw %v is far from the head's %v", cycle, time, pig.yaw, head.yaw)
			}
		}
		if pig := middle.pose(a.pose(width, height, 1, base+logoShuffleEnd/2)); math.Abs(math.Remainder(pig.yaw, math.Pi*2)) > 1e-9 || pig.pitch != 0 || pig.roll != 0 {
			t.Fatalf("cycle %d: the running pig does not face the camera: %+v", cycle, pig)
		}
		// The legs cycle through the four frames, one per pigRunStep.
		for frame := range pigRunFrames {
			r := a.runner(base + (float64(frame)+0.5)*pigRunStep)
			if !slices.EqualFunc(r.grid, pigRunPixels("pig-default", frame), bytes.Equal) {
				t.Fatalf("cycle %d: frame %d is not the run cycle's", cycle, frame)
			}
		}
	}
	// While the pig runs the frame is the ray-cast braille pig, wider than the head; afterwards the head is back.
	objectColumns := func(frame []string) int {
		widest := 0
		for _, line := range frame {
			first, last := -1, -1
			for column, r := range []rune(stripANSITest(line)) {
				if r > 0x2800 && r <= 0x28ff && bits.OnesCount(uint(r-0x2800)) > 1 {
					if first < 0 {
						first = column
					}
					last = column
				}
			}
			widest = max(widest, last-first+1)
		}
		return widest
	}
	clock.at((logoPuzzleStart + logoShuffleEnd/2) * 1000)
	running := a.Render(width)
	if strings.ContainsAny(strings.Join(running, ""), "▀▄") {
		t.Fatal("the running pig is drawn in half blocks, not braille")
	}
	head := objectColumns(a.renderFrame(width, nil))
	if got := objectColumns(running); got < 2*head {
		t.Fatalf("the running pig is %d columns wide, want at least twice the head's %d", got, head)
	}
	clock.at((logoPuzzleStart + logoShuffleEnd + 0.5) * 1000)
	if back := objectColumns(a.Render(width)); back >= 2*head || back == 0 {
		t.Fatalf("the head does not come back after the run: %d columns", back)
	}
	// The dissolves never leave the screen empty: the dots shown are the head's and the pig's, traded one for one.
	litDots := func() int {
		total := 0
		for _, cell := range a.logo.bits {
			total += bits.OnesCount8(cell)
		}
		return total
	}
	litAt := func(time float64) int {
		clock.at(time * 1000)
		a.Render(width)
		return litDots()
	}
	floor := litAt(logoPuzzleStart-0.01) * 8 / 10
	for _, window := range []float64{logoPuzzleStart, logoPuzzleStart + logoShuffleEnd - pigRunFade} {
		for step := range 20 {
			time := window + pigRunFade*float64(step)/19
			lit := litAt(time)
			// Each shape alone at this time, in its pose.
			a.renderFrame(width, nil)
			headDots := litDots()
			pigDots := headDots
			if r := a.runner(time); r != nil {
				whole := *r
				whole.mix = 1
				a.renderFrame(width, &whole)
				pigDots = litDots()
			}
			if want := min(headDots, pigDots) * 8 / 10; lit < want {
				t.Fatalf("at %.3f s the dissolve shows %d dots, want at least %d (head %d, pig %d)", time, lit, want, headDots, pigDots)
			}
		}
	}
	// Mid-dissolve both shapes show, each in part.
	clock.at((logoPuzzleStart + pigRunFade/2) * 1000)
	if r := a.runner(a.elapsed()); r == nil || r.mix <= 0.2 || r.mix >= 0.8 {
		t.Fatalf("mid-dissolve runner %+v", r)
	}
	// Esc during the run dissolves the pig back into the head over pigRunFade while Pi's reverse exit plays.
	clock.at((logoPuzzleStart + 1) * 1000)
	a.HandleInput("\x1b")
	if r := a.runner(a.elapsed()); r == nil || r.mix != 1 {
		t.Fatalf("the exit starts without the running pig: %+v", r)
	}
	clock.at((logoPuzzleStart + 1 + pigRunFade/2) * 1000)
	if r := a.runner(a.elapsed()); r == nil || math.Abs(r.mix-0.5) > 1e-9 {
		t.Fatalf("the exit's dissolve is not halfway at its middle: %+v", r)
	}
	if lit := litDots(); lit == 0 {
		t.Fatal("the exit's dissolve shows nothing")
	}
	a.Render(width)
	if lit := litDots(); lit < floor/2 {
		t.Fatalf("the exit's dissolve shows %d dots", lit)
	}
	clock.at((logoPuzzleStart + 1 + pigRunFade) * 1000)
	if a.runner(a.elapsed()) != nil {
		t.Fatal("the pig keeps running after the exit's dissolve")
	}
	// The legs move between frames and the body bobs.
	for frame := range pigRunFrames {
		next := (frame + 1) % len(pigRunFrames)
		if pigRunFrames[frame].reach == pigRunFrames[next].reach {
			t.Fatalf("no leg moves between frames %d and %d", frame, next)
		}
	}
	if pigRunFrames[0].lift == pigRunFrames[1].lift {
		t.Fatal("the body does not bob")
	}
	for _, row := range pigRunBody {
		if len(row) != pigRunWidth {
			t.Fatalf("body row %q is not %d pixels", row, pigRunWidth)
		}
	}
}

var updateRunGolden = flag.Bool("update-run", false, "rewrite the running pig goldens")

// Every sprite runs as a recognizable pig in its own colors (D87): testdata/pig-run/<id>.golden pins its four frames as
// pixels and the colors of their symbols. The body takes the sprite's body color (or its head's most common color), the eye is white with
// a dark pupil, and characters keep their accessories.
func TestPigRunFramesGolden(t *testing.T) {
	for _, variant := range piglogin.Variants {
		var b strings.Builder
		colors := pigRunPalette(variant)
		used := map[byte]bool{}
		for frame := range pigRunFrames {
			fmt.Fprintf(&b, "frame %d\n", frame)
			for _, row := range pigRunPixels(variant.ID, frame) {
				b.Write(row)
				b.WriteByte('\n')
				for _, symbol := range row {
					used[symbol] = symbol != '.'
				}
			}
		}
		for _, symbol := range slices.Sorted(maps.Keys(used)) {
			if used[symbol] {
				c := colors[symbol]
				fmt.Fprintf(&b, "%c #%02X%02X%02X\n", symbol, int(c[0]), int(c[1]), int(c[2]))
			}
		}
		path := filepath.Join("testdata", "pig-run", variant.ID+".golden")
		if *updateRunGolden {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if b.String() != string(want) {
			t.Errorf("%s: running pig differs from %s (go test -run TestPigRunFramesGolden -update-run)", variant.ID, path)
		}
		palette := piglogin.MascotPalette(variant)
		if variant.ID != "kratos" {
			if body := palette['P']; colors['P'] != (logoRgb{float64(body.R), float64(body.G), float64(body.B)}) {
				t.Errorf("%s: running body %v, want the sprite's %v", variant.ID, colors['P'], body)
			}
		}
		if colors['O'] == colors['P'] || colors['W'] == colors['K'] {
			t.Errorf("%s: the outline or the eye is invisible", variant.ID)
		}
		// Every frame draws the whole pig: ears, eye, snout and four hooves.
		for frame := range pigRunFrames {
			grid := pigRunPixels(variant.ID, frame)
			joined := string(bytes.Join(grid, []byte("\n")))
			for _, symbol := range "WKO" {
				if !strings.ContainsRune(joined, symbol) {
					t.Errorf("%s frame %d has no %c", variant.ID, frame, symbol)
				}
			}
			if variant.ID == "pig-default" && (!strings.Contains(joined, "s") || !strings.Contains(joined, "e")) {
				t.Errorf("frame %d has no snout or ears", frame)
			}
		}
	}
	kratos := pigRunPalette(piglogin.FindVariant("kratos"))
	skin := piglogin.MascotPalette(piglogin.FindVariant("kratos"))['E']
	if kratos['P'] != (logoRgb{float64(skin.R), float64(skin.G), float64(skin.B)}) {
		t.Fatalf("kratos runs with body %v, want the skin %v", kratos['P'], skin)
	}
	vader := pigRunPalette(piglogin.FindVariant("darth-vader"))
	if vader['O'] == vader['P'] || vader['b'] != (logoRgb{0xD8, 0, 0}) {
		t.Fatalf("vader's outline %v or red %v is lost", vader['O'], vader['b'])
	}
}

// TestPigRunStrip writes a PNG strip of every sprite's four run frames when PIG_RUN_STRIP names the output file: the
// braille dots the animation draws on a 100 by 30 screen, cropped to the pig.
func TestPigRunStrip(t *testing.T) {
	path := os.Getenv("PIG_RUN_STRIP")
	if path == "" {
		t.Skip("PIG_RUN_STRIP is not set")
	}
	const width, height, scale = 100, 30, 3
	const cropX, cropY, cropW, cropH = 22, 6, 56, 18
	cellW, cellH := cropW*2*scale+12, cropH*4*scale+12
	variants := piglogin.Variants
	img := image.NewRGBA(image.Rect(0, 0, cellW*len(pigRunFrames), cellH*len(variants)))
	draw.Draw(img, img.Bounds(), &image.Uniform{color.RGBA{0x0c, 0x0e, 0x12, 0xff}}, image.Point{}, draw.Src)
	for row, variant := range variants {
		clock := &logoTestClock{}
		a, _ := newTestPigLogoAnimation(t, clock, height, headLogoOptions(), variant, logoRgb{220, 220, 220}, logoRgb{12, 14, 18})
		for frame := range pigRunFrames {
			clock.at((logoPuzzleStart + 1.2 + (float64(frame)+0.5)*pigRunStep) * 1000)
			a.Render(width)
			drawLogoDots(img, &a.logo, width, cropX, cropY, cropW, cropH, scale, frame*cellW+6, row*cellH+6)
		}
	}
	writeTestPNG(t, path, img)
}

// drawLogoDots draws the raster's braille dots in a crop of the screen, scale pixels per dot, at (left, top).
func drawLogoDots(img *image.RGBA, logo *logoRaster, width, cropX, cropY, cropW, cropH, scale, left, top int) {
	for cy := range cropH {
		for cx := range cropW {
			index := (cropY+cy)*width + cropX + cx
			dots := logo.bits[index]
			if dots == 0 {
				continue
			}
			n := float32(logo.counts[index])
			rgba := color.RGBA{uint8(logo.rgb[index*3] / n), uint8(logo.rgb[index*3+1] / n), uint8(logo.rgb[index*3+2] / n), 0xff}
			for bit, mask := range logoDotBits {
				if dots&mask == 0 {
					continue
				}
				x0 := left + (cx*2+bit%2)*scale
				y0 := top + (cy*4+bit/2)*scale
				draw.Draw(img, image.Rect(x0, y0, x0+scale-1, y0+scale-1), &image.Uniform{rgba}, image.Point{}, draw.Src)
			}
		}
	}
}

// TestPigRunTransitionStrip writes a PNG of the dissolves between the head and the running pig when PIG_RUN_TRANSITION
// names the output file: rows are the head into the pig, the pig back into the head, and Esc during the run; columns
// step through pigRunFade.
func TestPigRunTransitionStrip(t *testing.T) {
	path := os.Getenv("PIG_RUN_TRANSITION")
	if path == "" {
		t.Skip("PIG_RUN_TRANSITION is not set")
	}
	const width, height, scale, columns = 100, 30, 2, 7
	const cropX, cropY, cropW, cropH = 22, 4, 56, 22
	cellW, cellH := cropW*2*scale+12, cropH*4*scale+12
	img := image.NewRGBA(image.Rect(0, 0, cellW*columns, cellH*3))
	draw.Draw(img, img.Bounds(), &image.Uniform{color.RGBA{0x0c, 0x0e, 0x12, 0xff}}, image.Point{}, draw.Src)
	starts := []float64{logoPuzzleStart, logoPuzzleStart + logoShuffleEnd - pigRunFade, logoPuzzleStart + 1.5}
	for row, start := range starts {
		clock := &logoTestClock{}
		a, _ := newTestPigLogoAnimation(t, clock, height, headLogoOptions(), piglogin.Default(), logoRgb{220, 220, 220}, logoRgb{12, 14, 18})
		if row == 2 {
			clock.at(start * 1000)
			a.Render(width)
			a.HandleInput("\x1b")
		}
		for column := range columns {
			clock.at((start + pigRunFade*float64(column)/(columns-1)) * 1000)
			a.Render(width)
			drawLogoDots(img, &a.logo, width, cropX, cropY, cropW, cropH, scale, column*cellW+6, row*cellH+6)
		}
	}
	writeTestPNG(t, path, img)
}

func writeTestPNG(t *testing.T, path string, img image.Image) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, img); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

// The running pig turns between the head's spin and the camera without blowing up (D87). Its ends come no closer to the
// camera than the head's, so through the run, its dissolves and an Esc during it, the ray-cast object never covers more
// cells than the face-on pig and never reaches the screen's edge; and it turns the short way, never farther from facing
// the camera than the head is.
func TestPigRunTurnStaysInFrame(t *testing.T) {
	for _, size := range [][2]int{{100, 30}, {80, 40}, {60, 40}, {200, 60}} {
		width, height := size[0], size[1]
		clock := &logoTestClock{}
		a, _ := newTestPigLogoAnimation(t, clock, height, headLogoOptions(), piglogin.Default(), logoRgb{220, 220, 220}, logoRgb{0, 0, 0})
		object := func() (cells, left, right, top, bottom int) {
			left, top = width, height
			right, bottom = -1, -1
			for row, line := range a.Render(width) {
				for column, r := range []rune(stripANSITest(line)) {
					// Stars are single dots; the object's cells hold more.
					if r > 0x2800 && r <= 0x28ff && bits.OnesCount(uint(r-0x2800)) > 1 {
						cells++
						left, right = min(left, column), max(right, column)
						top, bottom = min(top, row), max(bottom, row)
					}
				}
			}
			return cells, left, right, top, bottom
		}
		clock.at((logoPuzzleStart + logoShuffleEnd/2) * 1000)
		faceOn, _, _, _, _ := object()
		frame := float64(logoFrameInterval.Milliseconds())
		for ms := 0.0; ms < logoShuffleEnd*1000; ms += frame {
			at := logoPuzzleStart + ms/1000
			clock.at(at * 1000)
			if r := a.runner(at); r != nil {
				head := a.pose(width, height, a.flyProgress(at), at)
				if got, limit := math.Abs(math.Remainder(r.pose(head).yaw, math.Pi*2)), math.Abs(math.Remainder(head.yaw, math.Pi*2)); got > limit+1e-9 {
					t.Fatalf("%dx%d at %.0f ms into the run: the pig is %.2f rad from facing the camera, the head %.2f", width, height, ms, got, limit)
				}
			}
			cells, left, right, top, bottom := object()
			if cells > faceOn*11/10 || left <= 0 || right >= width-1 || top <= 0 || bottom >= height-2 {
				t.Fatalf("%dx%d at %.0f ms into the run: %d cells (face-on %d) in columns %d-%d, rows %d-%d", width, height, ms, cells, faceOn, left, right, top, bottom)
			}
		}
		clock.at((logoPuzzleStart + logoShuffleEnd/2) * 1000)
		a.HandleInput("\x1b")
		for ms := 0.0; ms <= pigRunFade*1000; ms += frame {
			clock.at((logoPuzzleStart+logoShuffleEnd/2)*1000 + ms)
			if cells, left, right, top, bottom := object(); cells > faceOn*11/10 || right >= width-1 || top < 0 || bottom >= height-2 || left < 0 {
				t.Fatalf("%dx%d at %.0f ms into the exit: %d cells (face-on %d) in columns %d-%d, rows %d-%d", width, height, ms, cells, faceOn, left, right, top, bottom)
			}
		}
	}
}

// The ray caster holds more than 255 faces (D87): the running pig has more, so a dot hit by a face past 255 keeps that
// face's color. Pi's Uint8Array would wrap the face index and shade the dot as another face.
func TestPigLogoRasterHoldsMoreThan255Faces(t *testing.T) {
	const width, height, side = 120, 40, 20
	var boxes []logoBox
	for row := range side {
		for column := range side {
			// Separate blocks, so no face is hidden by a neighbor.
			x, y := float64(column)*0.5-5, float64(row)*0.5-5
			boxes = append(boxes, logoBox{min: [3]float64{x, y, -0.1}, max: [3]float64{x + 0.3, y + 0.3, 0.1}, color: logoRgb{40, 40, 40}})
		}
	}
	boxes[len(boxes)-1].color = logoRgb{255, 0, 0}
	var raster logoRaster
	raster.render(width, height, logoPose{centerX: width, centerY: height * 2, scale: 6}, boxes, logoRgb{}, 0)
	if len(raster.faces) <= 256 {
		t.Fatalf("%d faces; the test needs more than 256", len(raster.faces))
	}
	red := false
	for cell := range width * height {
		if n := float32(raster.counts[cell]); n > 0 && raster.rgb[cell*3]/n > 4*raster.rgb[cell*3+1]/n+50 {
			red = true
		}
	}
	if !red {
		t.Fatal("the last block, past face 255, is not drawn in its own color")
	}
}
