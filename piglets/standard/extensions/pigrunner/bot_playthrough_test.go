package pigrunner

import (
	"bytes"
	"math/rand/v2"
	"strings"
	"testing"

	standardlogin "github.com/MichaelKinsy/PiG/piglets/standard/extensions/piglogin"
)

// asciiFrame renders a compact, human-readable ASCII view of the live game
// state (not the truecolor half-block frame). P = pig, p = ducking pig,
// # = ground pie (jump it), ^ = flying pie (duck it), _ = ground line.
func asciiFrame(g *Game, cols int) string {
	const top, bottom = 4, groundY
	h := bottom - top + 1
	grid := make([][]byte, h)
	for i := range grid {
		grid[i] = bytes.Repeat([]byte{' '}, cols)
	}
	put := func(x, y int, ch byte) {
		r := y - top
		if r >= 0 && r < h && x >= 0 && x < cols {
			grid[r][x] = ch
		}
	}
	for x := range cols {
		put(x, groundY, '_')
	}
	for _, ob := range g.Obstacles {
		x := int(ob.X)
		if ob.Kind == ObAir {
			for k := range 3 {
				put(x+k, groundY-11, '^')
			}
		} else {
			for k := range 3 {
				put(x+k, groundY-1, '#')
			}
		}
	}
	py := int(g.Pig.Y)
	if g.Pig.Ducked > 0 {
		put(pigX, py, 'p')
	} else {
		put(pigX, py, 'P')
		put(pigX, py-1, 'P')
	}
	var b strings.Builder
	for _, row := range grid {
		b.WriteString(strings.TrimRight(string(row), " "))
		b.WriteByte('\n')
	}
	return b.String()
}

// botMove decides the next control from the live state: jump an approaching
// ground pie, duck an approaching flying pie. This is the same intent a human
// expresses with Up (jump) and Down (duck).
func botMove(g *Game) (jump, duck bool) {
	speed := g.pixelSpeed()
	best := 1 << 30
	var kind ObstacleKind
	for i := range g.Obstacles {
		dx := int(g.Obstacles[i].X) - pigX
		if dx > -5 && dx < best {
			best = dx
			kind = g.Obstacles[i].Kind
		}
	}
	if best == 1<<30 {
		return false, false
	}
	// Frames until the obstacle reaches the pig. A Dino jump stays above a
	// pie for about 30 frames, so jumping when a ground pie is 6-12 frames
	// out lands the pig back down only after even a three-pie group has
	// passed. Ducking just needs to be held across the flying pie's approach.
	ttr := float64(best) / speed
	if kind == ObGround {
		return ttr >= 6 && ttr <= 12, false
	}
	return false, best > -4 && best <= 30
}

// TestBotPlaythrough drives the real Game with a jump/duck bot, capturing ASCII
// frames as the score climbs. It proves the game is playable end to end: the
// bot dodges both ground pies (jump/up) and flying pies (duck/down) and the
// score rises. Deterministic via a fixed RNG seed.
func TestBotPlaythrough(t *testing.T) {
	g := NewGameWithVariant(64, 0, standardlogin.FindVariant(""))
	g.rng = rand.New(rand.NewPCG(20240607, 1))

	cols := 64
	// One minute of Dino frames: flying pies appear from speed 8.5, after
	// about 42 seconds.
	const maxTicks = 60 * frameHz
	captureAt := map[int]bool{240: true, 1200: true, 2400: true, 3000: true, 3500: true}
	jumps, ducks, cleared := 0, 0, 0
	prevObstacles := 0
	duckShotDone := false

	for tick := 1; tick <= maxTicks; tick++ {
		jump, duck := botMove(g)
		if jump {
			if g.Pig.Y >= groundY && g.Pig.VelY == 0 {
				jumps++
			}
			g.Jump()
		}
		if duck {
			g.Duck()
			ducks++
		}
		before := len(g.Obstacles)
		g.Update()
		// An obstacle that left the field (count dropped, not via collision)
		// was successfully dodged.
		if !g.Over && len(g.Obstacles) < before {
			cleared += before - len(g.Obstacles)
		}
		prevObstacles = len(g.Obstacles)
		_ = prevObstacles

		// Capture the first flying-pie duck so the down/dodge path is visible.
		airOverhead := false
		for i := range g.Obstacles {
			if g.Obstacles[i].Kind == ObAir {
				if dx := int(g.Obstacles[i].X) - pigX; dx > -3 && dx < 6 {
					airOverhead = true
				}
			}
		}
		if !duckShotDone && g.Pig.Ducked > 0 && airOverhead {
			duckShotDone = true
			t.Logf("\n--- DUCK: tick %d  score=%d  ducking under flying pie ---\n%s",
				tick, g.Score, asciiFrame(g, cols))
		}

		if captureAt[tick] || g.Over {
			t.Logf("\n--- tick %d  score=%d  highScore=%d  obstacles=%d ---\n%s",
				tick, g.Score, g.HighScore, len(g.Obstacles), asciiFrame(g, cols))
		}
		if g.Over {
			t.Fatalf("bot died at tick %d (score %d) after clearing %d obstacles (jumps=%d ducks=%d)",
				tick, g.Score, cleared, jumps, ducks)
		}
	}

	t.Logf("survived %d ticks: score=%d jumps=%d ducks=%d obstaclesCleared=%d",
		maxTicks, g.Score, jumps, ducks, cleared)
	if g.Score < 500 {
		t.Fatalf("score did not climb as expected: got %d, want >= 500", g.Score)
	}
	if cleared < 8 {
		t.Fatalf("bot cleared too few obstacles (%d); dodge unproven", cleared)
	}
	if jumps == 0 {
		t.Fatal("bot never jumped; up/jump path unexercised")
	}
	if ducks == 0 {
		t.Fatal("bot never ducked; down/duck path unexercised")
	}
}
