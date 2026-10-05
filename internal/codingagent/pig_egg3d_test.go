package codingagent

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/piglogin"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/tui"
)

// pig3dOraclePixels is the model's bitmap as rows of block colors, or nil for an empty pixel.
func pig3dOraclePixels(model egg3dModel) [][]any {
	pixels := make([][]any, model.rows)
	for row := range pixels {
		pixels[row] = make([]any, model.columns)
	}
	for _, block := range model.blocks {
		pixels[int(block.home[1])][int(block.home[0])] = block.color
	}
	return pixels
}

// TestPig3dAnimationMatchesPinnedPi runs the pinned easter-egg-3d.ts with the 3D pig's model in place of Armin's (D87) and
// compares every frame with the Go port: the growth out of the center, the dust spreading from it, spin, starfield, hint,
// halo, the sliding puzzle over several cycles (its shuffle, the flight home and the hold), the exit that gathers moved
// blocks and its skip. Pi has no tests for this component.
func TestPig3dAnimationMatchesPinnedPi(t *testing.T) {
	puzzle := logoPuzzleStart
	cycle := logoPuzzleCycle
	entry := []float64{0, 16, 100, 250, 400, 700, 1000, 1400, 2000, 3000, 4000}
	for _, at := range []float64{0.01, 0.15, 0.31, 0.9, 1.7, 2.45, 3.3, 3.59, 3.75, 4.2, 4.55, 5.0} {
		entry = append(entry, (puzzle+at)*1000, (puzzle+cycle+at)*1000)
	}
	cases := []struct {
		name           string
		width, height  int
		variant        piglogin.Variant
		foreground, bg logoRgb
		appearance     string
		renderMs       []float64
		closeMs        float64
		closeKind      string
	}{
		{name: "dark, puzzle cycles, exit mid-shuffle", width: 100, height: 30, variant: piglogin.Default(), foreground: logoRgb{220, 220, 220}, bg: logoRgb{12, 14, 18}, appearance: "dark", renderMs: entry, closeMs: (puzzle + 2*cycle + 1.2) * 1000, closeKind: "key"},
		{name: "light halo and sheriff", width: 80, height: 24, variant: piglogin.FindVariant("sheriff"), foreground: logoRgb{30, 30, 30}, bg: logoRgb{250, 250, 245}, appearance: "light", renderMs: entry[:20], closeMs: (puzzle + 3.9) * 1000, closeKind: "click"},
		{name: "early exit while growing", width: 60, height: 18, variant: piglogin.FindVariant("kratos"), foreground: logoRgb{200, 200, 200}, bg: logoRgb{0, 0, 0}, appearance: "dark", renderMs: []float64{0, 100, 300, 600}, closeMs: 650, closeKind: "ctrl+c"},
		{name: "narrow screen hides the hint", width: 14, height: 8, variant: piglogin.Default(), foreground: logoRgb{220, 220, 220}, bg: logoRgb{20, 20, 20}, appearance: "dark", renderMs: []float64{0, 400, 1500, 2600, (puzzle + 0.5) * 1000}, closeMs: (puzzle + 0.6) * 1000, closeKind: "key"},
	}
	colors := tui.ActiveTheme().ColorValues()
	theme := map[string]logoRgb{"text": logoToRgb(colors["text"]), "muted": logoToRgb(colors["muted"]), "dim": logoToRgb(colors["dim"])}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clock := &logoTestClock{}
			clock.at(0)
			done := 0
			screen := logoTestScreen()
			a := newPig3dAnimation(func() int { return tc.height }, screen, tc.variant, tc.foreground, tc.bg, func() time.Time { return clock.now }, func() { done++ })
			var steps []logoOracleStep
			var want []any
			render := func() []string { return a.Render(tc.width) }
			for _, ms := range tc.renderMs {
				clock.at(ms)
				steps = append(steps, logoOracleStep{Ms: ms, Kind: "render"})
				want = append(want, render())
			}
			closeStep := func(ms float64) {
				clock.at(ms)
				switch tc.closeKind {
				case "key":
					steps = append(steps, logoOracleStep{Ms: ms, Kind: "key", Data: "\x1b"})
					a.HandleInput("\x1b")
					want = append(want, done)
				case "ctrl+c":
					steps = append(steps, logoOracleStep{Ms: ms, Kind: "key", Data: "\x03"})
					a.HandleInput("\x03")
					want = append(want, done)
				case "click":
					steps = append(steps, logoOracleStep{Ms: ms, Kind: "click", Type: "click"})
					result := a.HandleMouse(tui.TuiMouseEvent{Type: tui.MouseClick})
					want = append(want, map[string]any{"handled": result.Handled, "render": *result.Render}, done)
				}
			}
			clock.at(tc.closeMs - 1)
			steps = append(steps, logoOracleStep{Ms: tc.closeMs - 1, Kind: "key", Data: "q"}, logoOracleStep{Ms: tc.closeMs - 1, Kind: "click", Type: "press"})
			a.HandleInput("q")
			want = append(want, done)
			press := a.HandleMouse(tui.TuiMouseEvent{Type: tui.MousePress})
			want = append(want, map[string]any{"handled": press.Handled, "render": *press.Render}, done)
			closeStep(tc.closeMs)
			for _, after := range []float64{0, 60, 200, 330, 550, 700, 880, 1000, 1099, 1100, 1300} {
				ms := tc.closeMs + after
				clock.at(ms)
				steps = append(steps, logoOracleStep{Ms: ms, Kind: "render"})
				want = append(want, render())
			}
			steps = append(steps, logoOracleStep{Ms: tc.closeMs + 1300, Kind: "invalidate"})
			a.Invalidate()
			want = append(want, nil)
			steps = append(steps, logoOracleStep{Ms: tc.closeMs + 1300, Kind: "render"})
			want = append(want, render())
			closeStep(tc.closeMs + 1300)

			input := map[string]any{
				"upstream": pigversion.UpstreamVersion, "width": tc.width, "height": tc.height, "screen": screen,
				"foreground": tc.foreground, "background": tc.bg, "appearance": tc.appearance,
				"theme": theme, "colorMode": string(tui.ActiveTheme().ColorMode()),
				"columns": a.model.columns, "rows": a.model.rows, "pixels": pig3dOraclePixels(a.model),
				"steps": steps,
			}
			payload, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			command := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/pig3d-oracle.mjs")
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

// Pi's handleArminSaysHi plays the 3D Armin when playArmin3d can (fullscreen mode only) and otherwise shows the inline
// image (interactive-mode.ts, easter-egg-3d.lazy.ts playEasterEgg3d). PiG plays the 3D pig in fullscreen mode for
// /arminsayshi and /pigsayhi, does nothing while another overlay is open, and keeps the inline pig head inline.
func TestPig3dPlaysOnlyInFullscreen(t *testing.T) {
	pinHeaderTerminal(t, tui.TerminalColorModeTrueColor)
	t.Run("fullscreen plays the 3D pig", func(t *testing.T) {
		m, alt := newLogoClickMode(t, 100, 30)
		m.chatContainer = tui.NewContainer()
		m.handleArminSaysHi(t.Context())
		if len(m.arminComponents) != 0 || len(m.chatContainer.Children()) != 0 {
			t.Fatal("fullscreen /pigsayhi drew the inline pig head")
		}
		runOwnerTasks(t, m, func() bool { return m.logoAnimation != nil })
		a := m.logoAnimation
		if a.model.puzzleMoves == nil || a.model.origin || a.model.cameraDistance != 80 || !alt.HasOverlay() {
			t.Fatalf("fullscreen /pigsayhi played %+v, want the 3D pig growing out of the center", a.model)
		}
		// A second command while it plays starts nothing else.
		m.handleArminSaysHi(t.Context())
		if len(m.arminComponents) != 0 || m.logoAnimation != a {
			t.Fatal("a second /pigsayhi while the 3D pig plays changed what plays")
		}
		a.close()
		a.close()
		if m.logoAnimationPlaying || alt.HasOverlay() {
			t.Fatal("the 3D pig did not close")
		}
		m.disposeLogoAnimation()
	})
	t.Run("an open overlay plays nothing", func(t *testing.T) {
		m, alt := newLogoClickMode(t, 100, 30)
		m.chatContainer = tui.NewContainer()
		dialog := alt.OpenOverlay(tui.NewText("dialog"), tui.OverlaySpec{Anchor: "center"}.Options())
		m.handleArminSaysHi(t.Context())
		if m.logoAnimationPlaying || len(m.arminComponents) != 0 || len(m.chatContainer.Children()) != 0 {
			t.Fatal("/pigsayhi over an open overlay played or drew something")
		}
		dialog.Hide()
	})
	t.Run("inline keeps the flat pig head", func(t *testing.T) {
		m := newPendingDisplayHarness(t)
		m.chatContainer = tui.NewContainer()
		m.handleArminSaysHi(t.Context())
		if len(m.arminComponents) != 1 || m.logoAnimationPlaying {
			t.Fatal("inline /pigsayhi did not draw the inline pig head")
		}
		m.disposeArminComponents()
	})
}

var updatePig3dGolden = flag.Bool("update-pig3d-golden", false, "rewrite testdata/pig3d-frames.golden.json")

// TestPig3dFramesGolden pins frames of the default sprite's 3D pig: growing, spinning, mid-shuffle, flying home and leaving.
// The pinned-Pi comparison proves the engine; this golden catches a change to the pig's geometry or colors themselves.
// Regenerate with go test ./internal/codingagent -run TestPig3dFramesGolden -update-pig3d-golden and review the frames.
func TestPig3dFramesGolden(t *testing.T) {
	pinHeaderTerminal(t, tui.TerminalColorModeTrueColor)
	const width, height = 60, 20
	clock := &logoTestClock{}
	clock.at(0)
	a := newPig3dAnimation(func() int { return height }, logoTestScreen(), piglogin.Default(), logoRgb{220, 220, 220}, logoRgb{12, 14, 18}, func() time.Time { return clock.now }, func() {})
	frames := map[string][]string{}
	for _, at := range []float64{0.3, 1.4, 3.0, logoPuzzleStart + 1.5, logoPuzzleStart + logoShuffleEnd + 0.5} {
		clock.at(at * 1000)
		frames[fmt.Sprintf("%.3f", at)] = a.Render(width)
	}
	exit := logoPuzzleStart + 2.0
	clock.at(exit * 1000)
	a.close()
	clock.at((exit + 0.4) * 1000)
	frames[fmt.Sprintf("exit+0.4 at %.3f", exit)] = a.Render(width)
	got, err := json.MarshalIndent(frames, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	const path = "testdata/pig3d-frames.golden.json"
	if *updatePig3dGolden {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		gotLines, wantLines := strings.Split(string(got), "\n"), strings.Split(string(want), "\n")
		for i := range min(len(gotLines), len(wantLines)) {
			if gotLines[i] != wantLines[i] {
				t.Errorf("first difference at line %d:\n got %q\nwant %q", i+1, gotLines[i], wantLines[i])
				break
			}
		}
		t.Fatalf("3D pig frames differ from %s; rerun with -update-pig3d-golden after reviewing the change", path)
	}
}
