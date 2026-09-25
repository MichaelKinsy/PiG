package arcade

import (
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/piglets/standard/internal/pixel"
)

var sgr = regexp.MustCompile("\x1b\\[[0-9;]*m")

func cells(line string) int {
	n := 0
	for _, r := range sgr.ReplaceAllString(line, "") {
		n += pixel.CellWidth(r)
	}
	return n
}

func TestHUDLinesShareOneLayout(t *testing.T) {
	hud := HUD{Title: "GAME", Score: 42, High: 1234, Hints: "q quit", Status: " " + Dim("ready")}
	for _, width := range []int{1, 20, 80, 200} {
		lines := hud.Lines(width, true)
		if len(lines) != HUDLines {
			t.Fatalf("width %d: %d HUD lines", width, len(lines))
		}
		for i, line := range lines {
			if cells(line) != width {
				t.Fatalf("width %d line %d is %d cells", width, i, cells(line))
			}
		}
	}
	plain := sgr.ReplaceAllString(hud.Lines(80, true)[0], "")
	if !strings.HasPrefix(plain, " GAME  score 0042  high 1234  q quit") {
		t.Fatalf("title line = %q", plain)
	}
	if !strings.Contains(hud.Lines(80, true)[0], "\x1b[38;2;1;169;130mGAME") {
		t.Fatal("the title is not in the accent green")
	}
	over := hud
	over.State = Over
	if !strings.Contains(over.Lines(80, true)[0], red+"score 0042") {
		t.Fatal("a finished game's score is not red")
	}
	if strings.Contains(hud.Lines(80, false)[0], "38;2;") {
		t.Fatal("the HUD used 24-bit color without support")
	}
}

func TestDrawCardCentersTheTitleOrDeclines(t *testing.T) {
	var c pixel.Canvas
	c.Resize(80, 40)
	if !DrawCard(&c, "GAME OVER", "R RETRY", 1) {
		t.Fatal("the card did not fit an 80x40 canvas")
	}
	left, right := c.W, -1
	for y := range c.H {
		for x := range c.W {
			if c.At(x, y) == Accent {
				left, right = min(left, x), max(right, x)
			}
		}
	}
	if right < 0 || abs((left+right)/2-c.W/2) > 2 {
		t.Fatalf("title spans %d..%d, not centered in %d", left, right, c.W)
	}
	c.Resize(20, 10)
	if DrawCard(&c, "GAME OVER", "R RETRY", 1) {
		t.Fatal("the card drew on a canvas too small for it")
	}
}

func abs(v int) int { return max(v, -v) }

// The hill profiles are PiG Runner's original sawtooth lines.
func TestHillsMatchTheRunnerProfiles(t *testing.T) {
	for x := -80; x < 200; x++ {
		off := ((x % 40) + 40) % 40
		want := off / 5
		if off >= 20 {
			want = (40 - off) / 5
		}
		if FarHill(x) != want {
			t.Fatalf("FarHill(%d) = %d, want %d", x, FarHill(x), want)
		}
	}
	if SkyColor(0, 22) != SkyTop || SkyColor(11, 22) != SkyMid {
		t.Fatal("sky gradient endpoints moved")
	}
}
