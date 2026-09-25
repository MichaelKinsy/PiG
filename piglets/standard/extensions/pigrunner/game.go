package pigrunner

import (
	"image/color"
	"math"
	"math/rand/v2"
	"slices"
	"time"

	standardlogin "github.com/MichaelKinsy/PiG/piglets/standard/extensions/piglogin"
	"github.com/MichaelKinsy/PiG/piglets/standard/internal/pixel"
)

// The difficulty curve follows the Chrome Dino runner
// (components/neterror/resources/dino_game: offline.ts normalModeConfig and
// defaultBaseConfig, trex.ts, obstacle.ts, horizon.ts). The game steps once
// per Dino frame (60 Hz). Speeds, gaps, and jump physics are Dino values in
// Dino pixels, converted to playfield pixels by dinoScale: the 14-pixel pig
// stands in for the 44-pixel T-Rex.
const (
	screenH  = 22
	groundY  = 19
	pigX     = 8
	frameHz  = 60
	tickRate = time.Second / frameHz

	dinoScale = 14.0 / 44

	// offline.ts normalModeConfig and defaultBaseConfig.
	dinoSpeed          = 6.0
	dinoAcceleration   = 0.001
	dinoMaxSpeed       = 13.0
	dinoGapCoefficient = 0.6
	dinoMaxGapFactor   = 1.5 // obstacle.ts maxGapCoefficient
	dinoMaxGroup       = 3   // maxObstacleLength
	dinoMaxDuplication = 2   // maxObstacleDuplication
	dinoClearFrames    = 180 // clearTime 3000 ms
	dinoSpeedDrop      = 3.0 // speedDropCoefficient
	// distance_meter.ts COEFFICIENT: score per Dino pixel run.
	dinoScorePerPixel = 0.025

	// trex.ts normal-mode physics, in Dino pixels per frame. The T-Rex jumps
	// at initialJumpVelocity - speed/10; once it rises maxJumpHeight above
	// its standing top (93 - 30 = 63 pixels), its rise is capped at
	// dropVelocity. A terminal reports no key release, so every jump is the
	// full held jump.
	dinoGravity      = 0.6
	dinoJumpVelocity = 10.0
	dinoDropVelocity = 5.0
	dinoCapHeight    = 63.0

	// dinoCanvasWidth is the Dino's 600-pixel canvas. Obstacles enter at the
	// right edge of the view or of a Dino-wide field, whichever is wider, so a
	// narrow terminal does not shorten the time to react.
	dinoCanvasWidth = 600

	// duckFrames holds a duck for 0.9 s, the terminal stand-in for holding
	// Down.
	duckFrames = 54
)

// Obstacle types, the Dino's CACTUS_SMALL, CACTUS_LARGE, and PTERODACTYL
// drawn as pies.
type obstacleType struct {
	kind          ObstacleKind
	label         string
	multipleSpeed float64
	minGap        float64
	minSpeed      float64
	speedOffset   float64
}

var obstacleTypes = []obstacleType{
	{kind: ObGround, label: "pie", multipleSpeed: 4, minGap: 120},
	{kind: ObGround, label: "pie-crust", multipleSpeed: 7, minGap: 120},
	{kind: ObAir, label: "flying-pie", multipleSpeed: 999, minGap: 150, minSpeed: 8.5, speedOffset: 0.8},
}

type Game struct {
	Width      int
	Variant    standardlogin.Variant
	Pig        Pig
	PigPalette map[byte]color.RGBA
	pigPal     *pixel.Palette
	PigRunA    []string
	PigRunB    []string
	PigDuck    []string
	Obstacles  []Obstacle
	Score      int
	HighScore  int
	Over       bool
	Paused     bool
	// Waiting holds the title screen: the game does not advance until Start.
	Waiting bool
	Tick    int
	// Speed is the Dino speed in Dino pixels per frame.
	Speed float64
	// Distance is the Dino distance run, in Dino pixels.
	Distance float64

	history []string
	spawned int
	rng     *rand.Rand
}

type Pig struct {
	// Y is the pig's feet row; VelY is in playfield pixels per frame,
	// negative upward.
	Y         float64
	VelY      float64
	Ducked    int
	speedDrop bool
	capped    bool
}

type ObstacleKind int

const (
	ObGround ObstacleKind = iota
	ObAir
)

type Obstacle struct {
	Kind  ObstacleKind
	X     float64
	Label string
	// Size is how many pies stand side by side.
	Size int
	// Gap is the Dino gap, in playfield pixels, to keep clear before the
	// next obstacle appears.
	Gap         float64
	SpeedOffset float64
	followed    bool
}

func NewGame(width, highScore int) *Game {
	return NewGameWithVariant(width, highScore, standardlogin.FindVariant(""))
}

func NewGameWithVariant(width, highScore int, variant standardlogin.Variant) *Game {
	if width < minWidth {
		width = minWidth
	}
	runA, runB, duck := runnerSpritesForVariant(variant)
	palette := pigPaletteForVariant(variant)
	return &Game{
		Width:      width,
		Variant:    variant,
		Pig:        Pig{Y: groundY},
		PigPalette: palette,
		pigPal:     pixel.NewPalette(palette),
		PigRunA:    runA,
		PigRunB:    runB,
		PigDuck:    duck,
		HighScore:  highScore,
		Speed:      dinoSpeed,
		rng:        rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), 0)),
	}
}

// fieldWidth is where obstacles enter, in playfield pixels.
func (g *Game) fieldWidth() float64 {
	return max(float64(g.Width), dinoCanvasWidth*dinoScale)
}

// pixelSpeed is the scroll speed in playfield pixels per frame.
func (g *Game) pixelSpeed() float64 { return g.Speed * dinoScale }

// Update advances the game by one Dino frame and reports whether it changed.
func (g *Game) Update() bool {
	if g.Over || g.Paused || g.Waiting {
		return false
	}
	g.Tick++
	g.Distance += g.Speed
	g.Score = int(math.Round(g.Distance * dinoScorePerPixel))
	if g.Speed < dinoMaxSpeed {
		g.Speed = min(g.Speed+dinoAcceleration, dinoMaxSpeed)
	}

	g.updatePig()

	speed := g.pixelSpeed()
	alive := g.Obstacles[:0]
	for i := range g.Obstacles {
		ob := &g.Obstacles[i]
		ob.X -= speed + ob.SpeedOffset*dinoScale
		if int(ob.X)+ob.width() > 0 {
			alive = append(alive, *ob)
		}
	}
	g.Obstacles = alive
	if g.Tick > dinoClearFrames {
		g.spawnObstacles()
	}

	if slices.ContainsFunc(g.Obstacles, g.collides) {
		g.Over = true
		g.HighScore = max(g.HighScore, g.Score)
	}
	return true
}

func (g *Game) updatePig() {
	p := &g.Pig
	if p.Ducked > 0 {
		p.Ducked--
	}
	if p.Y >= groundY && p.VelY == 0 {
		return
	}
	if p.speedDrop {
		p.Y += p.VelY * dinoSpeedDrop
	} else {
		p.Y += p.VelY
	}
	p.VelY += dinoGravity * dinoScale
	if !p.capped && groundY-p.Y >= dinoCapHeight*dinoScale {
		p.capped = true
		p.VelY = max(p.VelY, -dinoDropVelocity*dinoScale)
	}
	if p.Y >= groundY {
		*p = Pig{Y: groundY, Ducked: p.Ducked}
	}
}

// spawnObstacles adds the next obstacle once the last one has cleared its
// gap from the right edge, as the Dino horizon does.
func (g *Game) spawnObstacles() {
	if n := len(g.Obstacles); n > 0 {
		last := &g.Obstacles[n-1]
		if last.followed || last.X+float64(last.width())+last.Gap >= g.fieldWidth() {
			return
		}
		last.followed = true
	}
	g.spawnObstacle()
}

func (g *Game) spawnObstacle() {
	for {
		t := obstacleTypes[g.rng.IntN(len(obstacleTypes))]
		if g.duplicate(t.label) || g.Speed < t.minSpeed {
			continue
		}
		size := 1 + g.rng.IntN(dinoMaxGroup)
		if size > 1 && t.multipleSpeed > g.Speed {
			size = 1
		}
		ob := Obstacle{Kind: t.kind, X: g.fieldWidth(), Label: t.label, Size: size}
		if t.speedOffset != 0 {
			ob.SpeedOffset = t.speedOffset
			if g.rng.IntN(2) == 0 {
				ob.SpeedOffset = -t.speedOffset
			}
		}
		// obstacle.ts getGap, with the pie width in Dino pixels.
		widthDino := float64(ob.width()) / dinoScale
		minGap := math.Round(widthDino*g.Speed + t.minGap*dinoGapCoefficient)
		maxGap := math.Round(minGap * dinoMaxGapFactor)
		ob.Gap = (minGap + float64(g.rng.IntN(int(maxGap-minGap)+1))) * dinoScale
		g.Obstacles = append(g.Obstacles, ob)
		g.spawned++
		g.history = append(g.history, t.label)
		if len(g.history) > dinoMaxDuplication {
			g.history = g.history[1:]
		}
		return
	}
}

// duplicate reports whether label already filled the last
// maxObstacleDuplication spawns.
func (g *Game) duplicate(label string) bool {
	if len(g.history) < dinoMaxDuplication {
		return false
	}
	for _, previous := range g.history {
		if previous != label {
			return false
		}
	}
	return true
}

const obWidth = 10

func (ob Obstacle) width() int { return obWidth * max(ob.Size, 1) }

func (g *Game) collides(ob Obstacle) bool {
	obLeft := int(ob.X) + 1
	obRight := int(ob.X) + ob.width() - 1
	obTop, obBot := obBoundsY(ob)

	var pigLeft, pigRight int
	var pigTop, pigBot float64
	if ob.Kind == ObAir {
		// Head hitbox: generous enough to require ducking, but squished
		// mascot passes below the flying pie.
		pigLeft, pigRight = pigX+2, pigX+11
		pigBot = g.Pig.Y - 1
		if g.Pig.Ducked > 0 {
			pigTop = g.Pig.Y - 5
		} else {
			pigTop = g.Pig.Y - 11
		}
	} else {
		// Ground hitbox: only the lower portion of the mascot counts.
		// This matches runner-game practice: forgive visual overlap from
		// ears/cheeks, punish failure to clear the lower body.
		pigLeft, pigRight = pigX+3, pigX+10
		pigTop = g.Pig.Y - 3
		pigBot = g.Pig.Y
	}

	return pigRight > obLeft && pigLeft < obRight && pigBot > float64(obTop) && pigTop < float64(obBot)
}

func obBoundsY(ob Obstacle) (top, bottom int) {
	if ob.Kind == ObAir {
		return groundY - 13, groundY - 9
	}
	// Visual pie is 5px tall, but hitbox is lower/forgiving so a normal
	// jump clears even when the crust visually overlaps a little.
	return groundY - 2, groundY
}

// Jump starts a Dino jump from the ground: faster runs jump harder.
func (g *Game) Jump() {
	if g.Pig.Y >= groundY && g.Pig.VelY == 0 {
		g.Pig.VelY = -(dinoJumpVelocity + g.Speed/10) * dinoScale
		g.Pig.Ducked = 0
	}
}

// Duck ducks on the ground; in the air it drops the pig fast, like holding
// Down during a Dino jump.
func (g *Game) Duck() {
	switch {
	case g.Pig.Y >= groundY:
		g.Pig.Ducked = duckFrames
	case !g.Pig.speedDrop:
		g.Pig.speedDrop = true
		g.Pig.VelY = dinoScale
	}
}

// Start leaves the title screen.
func (g *Game) Start() { g.Waiting = false }

func (g *Game) TogglePause() {
	g.Paused = !g.Paused
}

func (g *Game) Resize(w int) {
	if w < minWidth {
		w = minWidth
	}
	g.Width = w
}
