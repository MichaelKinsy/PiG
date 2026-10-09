package codingagent

// pi: packages/coding-agent/src/modes/interactive/components/armin.ts

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

func arminTestRandom(effect int) func() float64 {
	first, state := true, uint32(12345)
	return func() float64 {
		if first {
			first = false
			return (float64(effect) + 0.5) / float64(len(arminEffects))
		}
		state = state*1664525 + 1013904223
		return float64(state) / 4294967296
	}
}

func plainArminLines(lines []string) []string {
	result := make([]string, len(lines))
	for i, line := range lines {
		result[i] = widthx.StripAnsi(line)
	}
	return result
}

// arminXBM encodes the pig head as armin.ts stores its image: XBM rows of ceil(width/8) bytes, least significant bit
// first, 1 for background and 0 for foreground. The pinned source reads these bits in place of Armin's.
func arminXBM() []int {
	bytesPerRow := (arminWidth + 7) / 8
	bits := make([]int, arminHeight*bytesPerRow)
	for i := range bits {
		bits[i] = 0xff
	}
	for y, row := range arminImage {
		for x := range arminWidth {
			if row[x] == '#' {
				bits[y*bytesPerRow+x/8] &^= 1 << (x % 8)
			}
		}
	}
	return bits
}

// Pi: packages/coding-agent/src/modes/interactive/components/armin.ts:84 (ArminComponent.invalidate).
func TestArminFramesMatchPinnedPi(t *testing.T) {
	image, err := json.Marshal(map[string]any{
		"upstream": pigversion.UpstreamVersion, "width": arminWidth, "height": arminHeight, "bits": arminXBM(), "label": arminLabel,
	})
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/armin-oracle.mjs", string(image)).Output()
	if err != nil {
		t.Fatalf("pinned Pi oracle: %v", err)
	}
	var oracle []struct {
		Effect              string
		Interval            int
		Initial, Final      [][]string
		Hashes              []string
		Completed, Disposed bool
	}
	if err := json.Unmarshal(output, &oracle); err != nil {
		t.Fatal(err)
	}
	if len(oracle) != len(arminEffects) {
		t.Fatalf("effects: got %d want %d", len(oracle), len(arminEffects))
	}
	for effect, want := range oracle {
		t.Run(want.Effect, func(t *testing.T) {
			a := newArminComponent(arminTestRandom(effect))
			if a.effect != want.Effect || a.frameInterval() != time.Duration(want.Interval)*time.Millisecond {
				t.Fatal("effect or Node interval differs")
			}
			widths := []int{0, 1, 12, 31, 32, 80}
			for i, width := range widths {
				if got := plainArminLines(a.Render(width)); !slices.Equal(got, want.Initial[i]) {
					t.Fatalf("initial width %d: got %q want %q", width, got, want.Initial[i])
				}
			}
			var done bool
			for frame, hash := range want.Hashes {
				done = a.tickEffect()
				a.gridVersion++
				got := fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(plainArminLines(a.Render(80)), "\n"))))
				if got != hash {
					t.Fatalf("frame %d: hash %s want %s", frame+1, got, hash)
				}
				if done && frame != len(want.Hashes)-1 {
					t.Fatalf("stopped early at frame %d", frame+1)
				}
			}
			if done != want.Completed || !want.Disposed {
				t.Fatalf("completion: %v want %v", done, want.Completed)
			}
			for i, width := range widths {
				a.Invalidate()
				got := a.Render(width)
				if !slices.Equal(plainArminLines(got), want.Final[i]) {
					t.Fatalf("final width %d differs", width)
				}
				// Pi invalidates by setting cachedWidth=0, so rendering width zero
				// immediately afterwards reuses the last cached (80-cell) frame.
				styledWidth := width
				if width == 0 {
					styledWidth = 80
				}
				// Apply the real active theme to Pi's unstyled cells, including its untruncated label.
				for row, line := range got {
					text := strings.TrimRight(want.Final[i][row][1:], " ")
					if row < arminDisplayHeight {
						end := styledWidth - 1
						if end < 0 {
							end = max(0, arminWidth+end)
						}
						end = min(end, arminWidth)
						text = string(a.currentGrid[row][:end])
					}
					expected := " " + tui.ActiveTheme().Fg("accent", text) + strings.Repeat(" ", max(0, styledWidth-1-len([]rune(text))))
					if line != expected {
						t.Fatalf("color/padding width %d row %d: %q want %q", width, row, line, expected)
					}
				}
			}
		})
	}
}

// Pi: packages/coding-agent/src/modes/interactive/components/armin.ts:381 (ArminComponent.dispose).
func TestArminAnimationDisposalRejectsQueuedFrame(t *testing.T) {
	a := newArminComponent(arminTestRandom(2)) // Pi's non-terminating rain timer
	posted := make(chan func(), 1)
	rendered := false
	a.startAnimation(func(_ context.Context, fn func()) error { posted <- fn; return nil }, func() { rendered = true })
	fn := <-posted
	a.Dispose()
	a.Dispose()
	fn()
	if rendered || a.gridVersion != 0 {
		t.Fatal("disposed component accepted a late frame")
	}
	select {
	case <-a.animationDone:
	default:
		t.Fatal("timer worker not joined")
	}
}

func TestArminAnimationPublishesFramesAndStops(t *testing.T) {
	a := newArminComponent(arminTestRandom(5))
	posted := make(chan func())
	renders := 0
	a.startAnimation(func(ctx context.Context, fn func()) error {
		select {
		case posted <- fn:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}, func() { renders++ })
	defer a.Dispose()
	for {
		select {
		case fn := <-posted:
			fn()
		case <-a.animationDone:
			if renders != 9 || a.currentGrid != a.finalGrid {
				t.Fatalf("glitch completion: %d renders", renders)
			}
			return
		case <-t.Context().Done():
			t.Fatal("animation did not finish")
		}
	}
}

func BenchmarkArminFrameRender(b *testing.B) {
	a := newArminComponent(arminTestRandom(2))
	container := tui.NewContainer(a)
	b.ReportAllocs()
	for b.Loop() {
		a.tickEffect()
		a.gridVersion++
		a.BaseComponent.Invalidate()
		container.Render(100)
	}
}

// TestArminSaysHiDrawsThePigHead pins PiG's art (D87): once settled, /arminsayshi shows the pig head and "pigsayhi", never
// Armin's image or label.
func TestArminSaysHiDrawsThePigHead(t *testing.T) {
	want := []string{
		" ▄▄                         ▄▄ ",
		" █ ▀▀▄▄                 ▄▄▀▀ █ ",
		" █     ▀▀▄▄         ▄▄▀▀     █ ",
		" █         ▀▀▀▀▀▀▀▀▀         █ ",
		" █                           █ ",
		"█                             █",
		"█      ▄██▄         ▄██▄      █",
		"█      ▀██▀         ▀██▀      █",
		"█                             █",
		"█       ▄▄▀▀▀▀▀▀▀▀▀▀▀▄▄       █",
		"█      █    ▄▄   ▄▄    █      █",
		"█      █   ▀██▀ ▀██▀   █      █",
		"█       ▀▄▄         ▄▄▀       █",
		" █         ▀▀▀▀▀▀▀▀▀         █ ",
		"  █           ▄ ▄           █  ",
		"   ▀▀▄▄        ▀        ▄▄▀▀   ",
		"       ▀▀▀▄▄▄▄▄▄▄▄▄▄▄▀▀▀       ",
	}
	for effect := range arminEffects {
		a := newArminComponent(arminTestRandom(effect))
		for range 600 {
			if a.tickEffect() {
				break
			}
		}
		a.gridVersion++
		got := plainArminLines(a.Render(40))
		if len(got) != len(want)+1 {
			t.Fatalf("%s: %d lines, want %d", a.effect, len(got), len(want)+1)
		}
		for row, line := range want {
			if got[row] != " "+line+strings.Repeat(" ", 40-1-len([]rune(line))) {
				t.Fatalf("%s row %d: %q, want %q", a.effect, row, got[row], line)
			}
		}
		if label := got[len(want)]; label != " pigsayhi"+strings.Repeat(" ", 40-9) {
			t.Fatalf("%s label: %q", a.effect, label)
		}
		if strings.Contains(strings.Join(got, "\n"), "ARMIN") {
			t.Fatalf("%s shows Armin's label", a.effect)
		}
	}
}

// Pi: packages/coding-agent/src/modes/interactive/components/armin.ts:74-82 constructor(ui) picks an effect, builds the grids, initializes the effect and starts the animation, which calls ui.requestRender() on every frame. Go runs each frame on the ui's owner loop (tui.TUI.PostToOwner).
func TestNewArminComponentStartsItsAnimationOnTheUI(t *testing.T) {
	ui := tui.NewWithOutput(io.Discard, 40, 10)
	posted := make(chan func())
	ui.SetOwnerDispatcher(func(ctx context.Context, fn func()) error {
		select {
		case posted <- fn:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	renders := &renderCountingTUI{TUI: ui}
	a := NewArminComponent(renders)
	defer a.Dispose()
	if a.effect == "" || a.finalGrid == a.currentGrid {
		t.Fatalf("constructor left the component uninitialized: effect=%q", a.effect)
	}
	select {
	case fn := <-posted:
		if renders.renders.Load() != 0 || a.gridVersion != 0 {
			t.Fatal("the frame ran before the owner loop took it")
		}
		fn()
	case <-time.After(5 * time.Second):
		t.Fatal("constructor did not start the animation through the ui's owner dispatcher")
	}
	if renders.renders.Load() != 1 {
		t.Fatalf("a frame requested %d renders from the ui, want 1", renders.renders.Load())
	}
	if a.gridVersion != 1 {
		t.Fatalf("gridVersion = %d, want one tick", a.gridVersion)
	}
}

type renderCountingTUI struct {
	tui.TUI
	renders atomic.Int32
}

func (r *renderCountingTUI) RequestRender(...bool) { r.renders.Add(1) }

// The interactive mode installs its main-loop poster as the renderer's owner dispatcher, so an Armin frame built from the
// renderer runs on the main loop.
func TestInstallRenderDispatcherRoutesOwnerWorkToTheMainLoop(t *testing.T) {
	m := newPendingDisplayHarness(t)
	m.installRenderDispatcher()
	ran := false
	done := make(chan error, 1)
	go func() { done <- m.tuiInst.PostToOwner(t.Context(), func() { ran = true }) }()
	select {
	case fn := <-m.uiTaskCh:
		if ran {
			t.Fatal("work ran off the main loop")
		}
		fn()
	case <-time.After(5 * time.Second):
		t.Fatal("work did not reach the main loop queue")
	}
	if err := <-done; err != nil || !ran {
		t.Fatalf("err=%v ran=%v", err, ran)
	}
}

// A ui without an owner loop runs each frame inline on the timer goroutine (tui.TUI.PostToOwner), while the renderer draws the
// component on its own goroutine: the animation state must not be read and written unguarded (merge-train race gate).
func TestArminRenderWhileTheAnimationRunsInlineIsRaceFree(t *testing.T) {
	ui := tui.NewWithOutput(io.Discard, 80, 24)
	for effect := range arminEffects {
		a := newArminComponent(arminTestRandom(effect))
		a.startAnimation(ui.PostToOwner, func() {})
		deadline := time.Now().Add(150 * time.Millisecond)
		for time.Now().Before(deadline) {
			a.Render(80)
			a.Invalidate()
		}
		a.Dispose()
	}
}
