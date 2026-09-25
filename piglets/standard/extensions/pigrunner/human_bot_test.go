package pigrunner

import (
	"math/rand/v2"
	"testing"

	standardlogin "github.com/MichaelKinsy/PiG/piglets/standard/extensions/piglogin"
)

// humanBot plays with human limits: it sees the game reactionFrames late,
// and each jump lands anywhere within jitterFrames of its intended frame.
type humanBot struct {
	seen    []sighting
	pending int
	rng     *rand.Rand
}

type sighting struct {
	distance, speed float64
	kind            ObstacleKind
	ok              bool
}

const (
	reactionFrames = 12 // 200 ms
	jitterFrames   = 3  // ±50 ms
	jumpLeadFrames = 6
)

func newHumanBot(seed uint64) *humanBot {
	return &humanBot{seen: make([]sighting, reactionFrames+1), pending: -1, rng: rand.New(rand.NewPCG(seed, 99))}
}

func look(g *Game) sighting {
	best := sighting{distance: 1e9}
	for _, ob := range g.Obstacles {
		front := float64(pigX + 10)
		if ob.Kind == ObAir {
			front = pigX + 11
		}
		d := ob.X + 1 - front
		if d+float64(ob.width()) > -2 && d < best.distance {
			best = sighting{distance: d, speed: g.pixelSpeed(), kind: ob.Kind, ok: true}
		}
	}
	return best
}

func (b *humanBot) act(g *Game, frame int) {
	copy(b.seen, b.seen[1:])
	b.seen[reactionFrames] = look(g)
	s := b.seen[0]
	if b.pending < 0 && s.ok && g.Pig.Y >= groundY {
		// The bot knows it sees late and leads the obstacle by that much.
		ttr := s.distance/s.speed - reactionFrames
		switch {
		case s.kind == ObGround && ttr <= jumpLeadFrames+jitterFrames && ttr > jumpLeadFrames-8:
			b.pending = frame + b.rng.IntN(2*jitterFrames+1)
		case s.kind == ObAir && ttr <= 14 && ttr > -4 && g.Pig.Ducked == 0:
			g.Duck()
		}
	}
	if b.pending >= 0 && frame >= b.pending {
		g.Jump()
		b.pending = -1
	}
}

// A human-like player dies within a bounded score on every seed: difficulty
// keeps rising until the speed cap, and scores cannot run away. The same
// player survives the opening, so the game stays fair.
func TestHumanLikeBotDiesWithinABoundedScore(t *testing.T) {
	const (
		minScore = 300
		maxScore = 20000
	)
	for seed := uint64(1); seed <= 8; seed++ {
		g := NewGameWithVariant(120, 0, standardlogin.FindVariant(""))
		g.rng = rand.New(rand.NewPCG(seed, 7))
		bot := newHumanBot(seed)
		for frame := 0; !g.Over && g.Score <= maxScore; frame++ {
			bot.act(g, frame)
			g.Update()
		}
		t.Logf("seed %d: died at score %d after %d s", seed, g.Score, g.Tick/frameHz)
		if !g.Over || g.Score < minScore || g.Score > maxScore {
			t.Fatalf("seed %d: over %v at score %d, want a death between %d and %d", seed, g.Over, g.Score, minScore, maxScore)
		}
	}
}
