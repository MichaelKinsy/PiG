package codingagent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

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

func TestArminFramesMatchPinnedPi(t *testing.T) {
	output, err := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/armin-oracle.mjs").Output()
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
					expected := " " + tui.ActiveTheme().FgText("accent", text) + strings.Repeat(" ", max(0, styledWidth-1-len([]rune(text))))
					if line != expected {
						t.Fatalf("color/padding width %d row %d: %q want %q", width, row, line, expected)
					}
				}
			}
		})
	}
}

func TestArminAnimationDisposalRejectsQueuedFrame(t *testing.T) {
	a := newArminComponent(arminTestRandom(2)) // Pi's non-terminating rain timer
	posted := make(chan func(), 1)
	rendered := false
	a.startAnimation(t.Context(), func(_ context.Context, fn func()) error { posted <- fn; return nil }, func() { rendered = true })
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
	a.startAnimation(t.Context(), func(ctx context.Context, fn func()) error {
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
