package angrypigs

import (
	"math"
	"strconv"
)

// World geometry. The world is a fixed pixel field, independent of the
// terminal: x grows right, y grows up from the ground surface at y = 0.
// Structures sit on a grid of square cells.
const (
	cellSize = 6
	gridCols = worldW / cellSize
	gridRows = 12
	worldW   = 216
	// slingX and slingY are the pouch's rest position.
	slingX = 44.0
	slingY = 15.0
	// gravity is in pixels per second squared; maxSpeed is the launch speed
	// at 100% power in pixels per second.
	gravity  = 120.0
	maxSpeed = 165.0
	// stepSeconds is the fixed physics timestep.
	stepSeconds = 1.0 / 120
	pigRadius   = 3.5
	// hardHitSpeed is the impact speed that deals two damage instead of one.
	hardHitSpeed = 95.0
	// birdDrag is the speed kept after the pig flies through a bird.
	birdDrag = 0.8
	// shotHold is how long the camera stays on the target after a shot.
	shotHold = 1.1
	// introHold is how long a new level shows the fortress before the camera
	// returns to the slingshot.
	introHold   = 1.4
	birdPoints  = 500
	blockPoints = 50
	unusedPig   = 1000
	maxTrail    = 96
)

type cellKind uint8

const (
	cellEmpty cellKind = iota
	cellWood
	cellStone
	cellIce
	cellBird
)

// maxHits is how much damage each kind absorbs before it breaks.
var maxHits = [...]int{cellWood: 2, cellStone: 3, cellIce: 1, cellBird: 1}

type cell struct {
	kind cellKind
	hits int
	// drop is how far above its cell the block is still falling, in pixels,
	// and dropV its falling speed. Both are visual only.
	drop, dropV float64
	// fell counts the cells a cell fell during one settle.
	fell int
}

type point struct{ x, y float64 }

type game struct {
	level      int
	grid       [gridRows][gridCols]cell
	angle      int // degrees above the horizon
	power      int // percent
	pigsLeft   int
	flying     bool
	pig        point
	velocity   point
	spin       float64
	trail      [maxTrail]point
	trailLen   int
	score      int
	highScore  int
	levelDone  bool
	gameOver   bool
	lastEvent  string
	birdsTotal int

	// Presentation state advanced by the fixed step.
	time      float64
	steps     int
	camX      float64
	viewW     int
	hold      float64
	intro     float64
	particles particles
	popups    [8]popup
	banner    string
	shake     float64
}

// popup is a floating score label.
type popup struct {
	x, y  float64
	value int
	age   float64
}

const popupLife = 1.2

// levels place structures on the grid, right-aligned two cells from the world
// edge. Rows are top to bottom and end on the ground: W wood, S stone, I ice,
// B bird, space empty.
var levels = [][]string{
	{
		"  B  ",
		" WWW ",
		" W W ",
		" W W ",
	},
	{
		"   B     B ",
		"  SSS   III",
		"  W W   W W",
		" BW W   W W",
		" SSSSS  WWW",
	},
	{
		"      B      ",
		"     SSS     ",
		"  B  I I  B  ",
		" WWW I I WWW ",
		" W W W W W W ",
		"BW W W W W W ",
		"SSSSSSSSSSSS ",
	},
}

// levelPigs is the pig budget for each level.
var levelPigs = []int{4, 4, 5}

func newGame(viewW, highScore int) *game {
	g := &game{highScore: highScore, viewW: viewW}
	g.loadLevel(0)
	return g
}

func (g *game) resize(viewW int) {
	g.viewW = max(viewW, 1)
	g.camX = g.clampCam(g.camX)
}

func (g *game) loadLevel(level int) {
	g.level = level
	g.grid = [gridRows][gridCols]cell{}
	layout := levels[level]
	left := gridCols - 2 - len(layout[0])
	g.birdsTotal = 0
	for r, line := range layout {
		cy := len(layout) - 1 - r
		for c, ch := range line {
			kind := cellEmpty
			switch ch {
			case 'W':
				kind = cellWood
			case 'S':
				kind = cellStone
			case 'I':
				kind = cellIce
			case 'B':
				kind = cellBird
				g.birdsTotal++
			}
			if kind != cellEmpty {
				g.grid[cy][left+c] = cell{kind: kind, hits: maxHits[kind]}
			}
		}
	}
	g.angle, g.power = 45, 70
	g.pigsLeft = levelPigs[level]
	g.flying, g.levelDone, g.gameOver = false, false, false
	g.trailLen = 0
	g.particles.clear()
	g.popups = [len(g.popups)]popup{}
	g.banner = ""
	g.hold = 0
	g.intro = introHold
	g.camX = g.clampCam(worldW)
	g.lastEvent = "Level " + strconv.Itoa(level+1) + ": knock out every bird"
}

func (g *game) birdsLeft() int {
	count := 0
	for y := range g.grid {
		for x := range g.grid[y] {
			if g.grid[y][x].kind == cellBird {
				count++
			}
		}
	}
	return count
}

func (g *game) aim(deltaAngle, deltaPower int) {
	if g.flying {
		return
	}
	g.angle = min(max(g.angle+deltaAngle, 5), 85)
	g.power = min(max(g.power+deltaPower, 10), 100)
}

func (g *game) launchVelocity() point {
	speed := maxSpeed * float64(g.power) / 100
	radians := float64(g.angle) * math.Pi / 180
	return point{x: speed * math.Cos(radians), y: speed * math.Sin(radians)}
}

func (g *game) launch() {
	if g.flying || g.levelDone || g.gameOver || g.pigsLeft == 0 {
		return
	}
	g.pigsLeft--
	g.flying = true
	g.pig = g.pouch()
	g.velocity = g.launchVelocity()
	g.trailLen = 0
	g.intro, g.hold = 0, 0
	g.lastEvent = "Oink!"
	g.particles.burst(g.pig.x, g.pig.y, 6, dustKind, dustColor, 30, 0.5)
}

// step advances the game by one fixed physics step.
func (g *game) step() {
	const dt = stepSeconds
	g.time += dt
	g.steps++
	g.shake = max(g.shake-dt*3, 0)
	if g.flying {
		g.stepPig(dt)
	}
	g.stepFalling(dt)
	g.particles.step(dt)
	for i := range g.popups {
		if g.popups[i].age < popupLife {
			g.popups[i].age += dt
			g.popups[i].y += 14 * dt
		}
	}
	switch {
	case g.intro > 0:
		g.intro -= dt
	case g.hold > 0 && !g.flying:
		g.hold -= dt
	}
	target := g.cameraTarget()
	g.camX += (target - g.camX) * min(1, dt*5)
	if math.Abs(target-g.camX) < 0.05 {
		g.camX = target
	}
}

func (g *game) cameraTarget() float64 {
	switch {
	case g.flying:
		return g.clampCam(g.pig.x - float64(g.viewW)*0.4)
	case g.intro > 0:
		return g.clampCam(worldW)
	case g.hold > 0 || g.levelDone:
		return g.camX
	}
	return g.clampCam(0)
}

// clampCam keeps the view inside the world, or centers the world when the
// view is wider than it.
func (g *game) clampCam(x float64) float64 {
	if g.viewW >= worldW {
		return -float64(g.viewW-worldW) / 2
	}
	return min(max(x, 0), float64(worldW-g.viewW))
}

func (g *game) stepPig(dt float64) {
	g.velocity.y -= gravity * dt
	g.pig.x += g.velocity.x * dt
	g.pig.y += g.velocity.y * dt
	g.spin += g.velocity.x * dt * 0.08
	if g.steps%4 == 0 {
		g.addTrail(g.pig)
	}
	if g.pig.y-pigRadius <= 0 {
		g.pig.y = pigRadius
		g.particles.burst(g.pig.x, 0, 10, dustKind, dustColor, 40, 0.7)
		g.endShot("The pig landed.")
		return
	}
	if g.pig.x < -24 || g.pig.x > worldW+24 {
		g.endShot("The pig flew off the field.")
		return
	}
	g.collide()
}

func (g *game) addTrail(p point) {
	if g.trailLen < maxTrail {
		g.trail[g.trailLen] = p
		g.trailLen++
	}
}

// collide resolves the first cell the pig overlaps.
func (g *game) collide() {
	x0 := max(int((g.pig.x-pigRadius)/cellSize), 0)
	x1 := min(int((g.pig.x+pigRadius)/cellSize), gridCols-1)
	y0 := max(int((g.pig.y-pigRadius)/cellSize), 0)
	y1 := min(int((g.pig.y+pigRadius)/cellSize), gridRows-1)
	for cy := y0; cy <= y1; cy++ {
		for cx := x0; cx <= x1; cx++ {
			target := &g.grid[cy][cx]
			if target.kind == cellEmpty || !g.overlaps(cx, cy) {
				continue
			}
			centerX, centerY := float64(cx*cellSize)+cellSize/2, float64(cy*cellSize)+cellSize/2
			if target.kind == cellBird {
				*target = cell{}
				g.knockOutBird(centerX, centerY, "Bird knocked out!")
				g.velocity.x *= birdDrag
				g.velocity.y *= birdDrag
				g.settle()
				continue
			}
			speed := math.Hypot(g.velocity.x, g.velocity.y)
			damage := 1
			if speed >= hardHitSpeed {
				damage = 2
			}
			g.damage(cx, cy, damage)
			g.shake = min(speed/120, 1)
			g.particles.burst(g.pig.x, g.pig.y, 12, dustKind, dustColor, 45, 0.8)
			g.settle()
			g.endShot("Thud.")
			return
		}
	}
}

func (g *game) overlaps(cx, cy int) bool {
	left, bottom := float64(cx*cellSize), float64(cy*cellSize)
	nearX := min(max(g.pig.x, left), left+cellSize)
	nearY := min(max(g.pig.y, bottom), bottom+cellSize)
	return math.Hypot(g.pig.x-nearX, g.pig.y-nearY) < pigRadius
}

// damage hits a block; a broken block scores and bursts into debris.
func (g *game) damage(cx, cy, amount int) {
	target := &g.grid[cy][cx]
	target.hits -= amount
	if target.hits > 0 {
		g.particles.burst(float64(cx*cellSize)+3, float64(cy*cellSize)+3, 4, debrisKind, materialColor(target.kind), 35, 0.9)
		return
	}
	kind := target.kind
	*target = cell{}
	g.score += blockPoints
	g.particles.burst(float64(cx*cellSize)+3, float64(cy*cellSize)+3, 14, debrisKind, materialColor(kind), 60, 1.2)
}

func (g *game) knockOutBird(x, y float64, event string) {
	g.score += birdPoints
	g.lastEvent = event
	g.particles.burst(x, y, 12, featherKind, featherColor, 35, 1.6)
	g.particles.burst(x, y, 8, dustKind, puffColor, 25, 0.7)
	for i := range g.popups {
		if g.popups[i].age >= popupLife || g.popups[i].value == 0 {
			g.popups[i] = popup{x: x, y: y + 4, value: birdPoints}
			break
		}
	}
}

// settle drops unsupported blocks and birds until everything rests. A cell
// is supported by the ground or by a supported cell below it; a block over a
// one-cell gap is also supported when the blocks on both sides of it are, so
// lintels hold until a leg breaks. A bird that falls two or more cells is
// knocked out. Falling cells animate from where they were.
func (g *game) settle() {
	for {
		for moved := true; moved; {
			moved = false
			var supported [gridRows][gridCols]bool
			g.markSupported(&supported)
			for cy := 1; cy < gridRows; cy++ {
				for cx := range gridCols {
					c := &g.grid[cy][cx]
					if c.kind == cellEmpty || supported[cy][cx] || g.grid[cy-1][cx].kind != cellEmpty {
						continue
					}
					below := &g.grid[cy-1][cx]
					*below = *c
					below.fell++
					below.drop += cellSize
					*c = cell{}
					moved = true
				}
			}
		}
		knockedOut := false
		for cy := range gridRows {
			for cx := range gridCols {
				c := &g.grid[cy][cx]
				if c.kind == cellBird && c.fell >= 2 {
					*c = cell{}
					g.knockOutBird(float64(cx*cellSize)+3, float64(cy*cellSize)+3, "A bird took a tumble!")
					knockedOut = true
				}
				c.fell = 0
			}
		}
		if !knockedOut {
			return
		}
	}
}

func isBlock(kind cellKind) bool {
	return kind == cellWood || kind == cellStone || kind == cellIce
}

func (g *game) markSupported(supported *[gridRows][gridCols]bool) {
	for cy := range gridRows {
		for cx := range gridCols {
			if g.grid[cy][cx].kind != cellEmpty && (cy == 0 || supported[cy-1][cx]) {
				supported[cy][cx] = true
			}
		}
		for spanned := true; spanned; {
			spanned = false
			for cx := 1; cx < gridCols-1; cx++ {
				row := &g.grid[cy]
				if supported[cy][cx] || !isBlock(row[cx].kind) || !isBlock(row[cx-1].kind) || !isBlock(row[cx+1].kind) {
					continue
				}
				if supported[cy][cx-1] && supported[cy][cx+1] {
					supported[cy][cx] = true
					spanned = true
				}
			}
		}
	}
}

// stepFalling animates settled cells down to their resting place.
func (g *game) stepFalling(dt float64) {
	for cy := range gridRows {
		for cx := range gridCols {
			c := &g.grid[cy][cx]
			if c.drop <= 0 {
				continue
			}
			c.dropV += gravity * 1.5 * dt
			c.drop -= c.dropV * dt
			if c.drop <= 0 {
				c.drop, c.dropV = 0, 0
				g.particles.burst(float64(cx*cellSize)+3, float64(cy*cellSize), 3, dustKind, dustColor, 18, 0.4)
			}
		}
	}
}

func (g *game) endShot(event string) {
	g.flying = false
	g.hold = shotHold
	g.lastEvent = event
	if g.birdsLeft() == 0 {
		g.levelDone = true
		g.score += g.pigsLeft * unusedPig
		g.lastEvent = "Level clear! Press n for the next level."
		g.showBanner("LEVEL CLEAR!")
		if g.level == len(levels)-1 {
			g.gameOver = true
			g.lastEvent = "Every bird is down. You win! Press r to play again."
			g.showBanner("YOU WIN!")
		}
		return
	}
	if g.pigsLeft == 0 {
		g.gameOver = true
		g.lastEvent = "Out of pigs. Press r to try again."
		g.showBanner("GAME OVER")
	}
}

func (g *game) showBanner(text string) {
	g.banner = text
}

func (g *game) nextLevel() {
	if g.levelDone && g.level+1 < len(levels) {
		g.loadLevel(g.level + 1)
	}
}

func (g *game) restart() {
	g.score = 0
	g.loadLevel(0)
}

// animating reports whether the scene changes without input.
func (g *game) animating() bool {
	if g.flying || g.shake > 0 || g.particles.live > 0 || g.intro > 0 || g.hold > 0 {
		return true
	}
	for i := range g.popups {
		if g.popups[i].value != 0 && g.popups[i].age < popupLife {
			return true
		}
	}
	for cy := range gridRows {
		for cx := range gridCols {
			if g.grid[cy][cx].drop > 0 {
				return true
			}
		}
	}
	return math.Abs(g.cameraTarget()-g.camX) > 0
}

// previewEvery is how many physics steps apart the preview dots are.
const previewEvery = 6

// preview returns up to cap(dst) points of the aimed flight, one every
// previewEvery physics steps. It integrates exactly as a launched pig flies,
// from the pulled-back pouch, and stops where the pig would land or hit a
// structure.
func (g *game) preview(dst []point) []point {
	dst = dst[:0]
	if g.flying || g.levelDone || g.gameOver || g.pigsLeft == 0 {
		return dst
	}
	pos, vel := g.pouch(), g.launchVelocity()
	for step := 1; len(dst) < cap(dst); step++ {
		vel.y -= gravity * stepSeconds
		pos.x += vel.x * stepSeconds
		pos.y += vel.y * stepSeconds
		if pos.y-pigRadius <= 0 || pos.x < 0 || pos.x >= worldW {
			break
		}
		if cx, cy := int(pos.x/cellSize), int(pos.y/cellSize); cy < gridRows && g.grid[cy][cx].kind != cellEmpty {
			break
		}
		if step%previewEvery == 0 {
			dst = append(dst, pos)
		}
	}
	return dst
}

// maxPull is how far back, in pixels, full power pulls the pouch.
const maxPull = 14.0

// pouch is where the loaded pig sits: pulled back from the rest point,
// against the aim, by the chosen power. A launch starts here.
func (g *game) pouch() point {
	radians := float64(g.angle) * math.Pi / 180
	pull := maxPull * float64(g.power) / 100
	return point{x: slingX - math.Cos(radians)*pull, y: slingY - math.Sin(radians)*pull}
}

// drag aims like pulling the band with a mouse: the pouch follows the
// pointer up to maxPull from the rest point, the pull length sets the power,
// and the pig launches away from the pointer. It returns false while the
// game cannot aim.
func (g *game) drag(x, y float64) bool {
	if g.flying || g.levelDone || g.gameOver || g.pigsLeft == 0 {
		return false
	}
	dx, dy := slingX-x, slingY-y
	pull := min(math.Hypot(dx, dy), maxPull)
	g.power = min(max(int(math.Round(100*pull/maxPull)), 10), 100)
	if dx > 0 || dy > 0 {
		degrees := math.Atan2(dy, dx) * 180 / math.Pi
		g.angle = min(max(int(math.Round(degrees)), 5), 85)
	}
	return true
}
