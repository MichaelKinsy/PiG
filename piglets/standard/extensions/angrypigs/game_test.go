package angrypigs

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"math"
	"net"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	standardlogin "github.com/MichaelKinsy/PiG/piglets/standard/extensions/piglogin"
	"github.com/MichaelKinsy/PiG/piglets/standard/internal/arcade"
	"github.com/MichaelKinsy/PiG/piglets/standard/internal/pixel"
)

var sgrPattern = regexp.MustCompile("\x1b\\[[0-9;]*m")

func visibleWidth(line string) int {
	width := 0
	for _, r := range sgrPattern.ReplaceAllString(line, "") {
		width += pixel.CellWidth(r)
	}
	return width
}

func defaultPalette() *pixel.Palette {
	return pixel.NewPalette(standardlogin.MascotPalette(standardlogin.FindVariant("")))
}

// emptyField returns a game with no structures.
func emptyField() *game {
	g := newGame(80, 0)
	g.grid = [gridRows][gridCols]cell{}
	g.intro = 0
	return g
}

func place(g *game, cx, cy int, kind cellKind) {
	g.grid[cy][cx] = cell{kind: kind, hits: maxHits[kind]}
	if kind == cellBird {
		g.birdsTotal++
	}
}

func fly(g *game) {
	for step := 0; step < 4000 && g.flying; step++ {
		g.step()
	}
}

// flatShot flies the pig level from x toward the right at speed, with gravity
// cancelled, to isolate collision handling.
func flatShot(g *game, x, y, speed float64) {
	g.flying = true
	g.pig = point{x: x, y: y}
	g.velocity = point{x: speed}
	for step := 0; step < 2000 && g.flying; step++ {
		g.velocity.y = 0
		g.step()
	}
}

func TestAimStaysInBoundsAndFreezesDuringFlight(t *testing.T) {
	g := newGame(80, 0)
	g.aim(1000, 1000)
	if g.angle != 85 || g.power != 100 {
		t.Fatalf("aim upper bound = %d°/%d%%", g.angle, g.power)
	}
	g.aim(-1000, -1000)
	if g.angle != 5 || g.power != 10 {
		t.Fatalf("aim lower bound = %d°/%d%%", g.angle, g.power)
	}
	g.launch()
	g.aim(10, 10)
	if g.angle != 5 || g.power != 10 {
		t.Fatal("aim changed while the pig was flying")
	}
}

func TestLaunchSpendsAPigOnlyWhenReady(t *testing.T) {
	g := newGame(80, 0)
	g.launch()
	if !g.flying || g.pigsLeft != levelPigs[0]-1 {
		t.Fatalf("launch: flying %v, pigs %d", g.flying, g.pigsLeft)
	}
	g.launch()
	if g.pigsLeft != levelPigs[0]-1 {
		t.Fatal("a second launch during flight spent another pig")
	}
}

func TestBirdHitScoresAndBlockHitEndsTheShot(t *testing.T) {
	g := emptyField()
	place(g, 15, 0, cellBird)
	place(g, 22, 0, cellWood)
	flatShot(g, 60, 5, 80)
	if g.grid[0][15].kind != cellEmpty || g.score < birdPoints {
		t.Fatalf("bird not knocked out: score %d", g.score)
	}
	if g.flying {
		t.Fatal("the shot did not end at the wooden block")
	}
	if got := g.grid[0][22]; got.kind != cellWood || got.hits != maxHits[cellWood]-1 {
		t.Fatalf("a slow hit on wood = %+v, want one damage", got)
	}
	if !g.levelDone || g.banner != "LEVEL CLEAR!" {
		t.Fatalf("clearing the only bird: levelDone %v, banner %q", g.levelDone, g.banner)
	}
}

func TestHardHitsDealDoubleDamage(t *testing.T) {
	g := emptyField()
	place(g, 20, 0, cellWood)
	place(g, 30, 0, cellBird)
	flatShot(g, 100, 5, 130)
	if g.grid[0][20].kind != cellEmpty || g.score != blockPoints {
		t.Fatalf("a hard hit left wood %+v, score %d", g.grid[0][20], g.score)
	}
	g = emptyField()
	place(g, 20, 0, cellStone)
	place(g, 30, 0, cellBird)
	flatShot(g, 100, 5, 130)
	if got := g.grid[0][20]; got.kind != cellStone || got.hits != 1 {
		t.Fatalf("a hard hit on stone = %+v, want one hit left", got)
	}
	flatShot(g, 100, 5, 60)
	if g.grid[0][20].kind != cellEmpty {
		t.Fatal("the last hit did not break the cracked stone")
	}
}

func TestIceShattersOnAnyHit(t *testing.T) {
	g := emptyField()
	place(g, 20, 0, cellIce)
	place(g, 30, 0, cellBird)
	flatShot(g, 100, 5, 40)
	if g.grid[0][20].kind != cellEmpty || g.particles.live == 0 {
		t.Fatal("ice did not shatter into debris")
	}
}

func TestSettleDropsBlocksAndTumblingBirdsAreKnockedOut(t *testing.T) {
	g := emptyField()
	place(g, 10, 3, cellWood)
	place(g, 12, 2, cellBird)
	place(g, 12, 1, cellWood)
	place(g, 20, 0, cellBird)
	g.settle()
	if g.grid[0][10].kind != cellWood || g.grid[3][10].kind != cellEmpty {
		t.Fatal("an unsupported block did not fall to the ground")
	}
	if g.grid[0][10].drop != 3*cellSize {
		t.Fatalf("fallen block drop = %v, want %d pixels to animate", g.grid[0][10].drop, 3*cellSize)
	}
	if g.grid[1][12].kind != cellBird || g.score != 0 {
		t.Fatal("a bird that fell one cell was knocked out")
	}
	g.grid[0][12] = cell{}
	g.grid[1][12] = cell{}
	place(g, 12, 3, cellBird)
	g.settle()
	if g.birdsLeft() != 1 || g.score != birdPoints {
		t.Fatalf("a bird that fell three cells: birds %d, score %d", g.birdsLeft(), g.score)
	}
	for range 240 {
		g.step()
	}
	if g.grid[0][10].drop != 0 {
		t.Fatal("the fall animation did not finish")
	}
}

// Built levels stand on their own: settling a fresh level moves nothing.
func TestEveryLevelStandsUntilHit(t *testing.T) {
	for level := range levels {
		g := newGame(80, 0)
		g.loadLevel(level)
		before := g.grid
		g.settle()
		if g.grid != before || g.score != 0 {
			t.Fatalf("level %d collapsed without a hit", level+1)
		}
	}
}

// A lintel over a one-cell gap holds while both of its neighbors stand, and
// falls with whatever rests on it when a leg breaks.
func TestLintelsHoldUntilALegBreaks(t *testing.T) {
	g := emptyField()
	for _, p := range [][2]int{{10, 0}, {12, 0}, {10, 1}, {11, 1}, {12, 1}} {
		place(g, p[0], p[1], cellWood)
	}
	place(g, 11, 2, cellBird)
	g.settle()
	if g.grid[1][11].kind != cellWood || g.grid[2][11].kind != cellBird {
		t.Fatal("the lintel or its bird fell while both legs stood")
	}
	g.grid[0][12] = cell{}
	g.settle()
	if g.grid[0][11].kind != cellWood || g.grid[0][12].kind != cellWood || g.grid[2][11].kind != cellEmpty {
		t.Fatal("the lintel did not collapse after a leg broke")
	}
	if g.grid[1][11].kind != cellBird || g.score != 0 {
		t.Fatal("the bird should ride the lintel down one cell and survive")
	}
}

func TestLevelFlowBonusNextLevelWinAndRestart(t *testing.T) {
	g := emptyField()
	place(g, 20, 0, cellBird)
	g.launch()
	g.grid[0][20] = cell{}
	g.endShot("test")
	if !g.levelDone || g.score != (levelPigs[0]-1)*unusedPig {
		t.Fatalf("level clear: done %v, score %d", g.levelDone, g.score)
	}
	g.nextLevel()
	if g.level != 1 || g.levelDone || g.pigsLeft != levelPigs[1] {
		t.Fatalf("next level = %d, done %v, pigs %d", g.level, g.levelDone, g.pigsLeft)
	}
	g.nextLevel()
	if g.level != 1 {
		t.Fatal("n skipped an unfinished level")
	}
	g.loadLevel(len(levels) - 1)
	for cy := range gridRows {
		for cx := range gridCols {
			if g.grid[cy][cx].kind == cellBird {
				g.grid[cy][cx] = cell{}
			}
		}
	}
	g.endShot("test")
	if !g.gameOver || g.banner != "YOU WIN!" {
		t.Fatalf("clearing the last level: over %v, banner %q", g.gameOver, g.banner)
	}
	g.restart()
	if g.score != 0 || g.level != 0 || g.gameOver {
		t.Fatalf("restart = score %d, level %d, over %v", g.score, g.level, g.gameOver)
	}
}

func TestRunningOutOfPigsEndsTheGame(t *testing.T) {
	g := newGame(80, 0)
	g.pigsLeft = 1
	g.launch()
	g.endShot("test")
	if !g.gameOver || g.banner != "GAME OVER" {
		t.Fatalf("last pig spent: over %v, banner %q", g.gameOver, g.banner)
	}
	g.launch()
	if g.flying {
		t.Fatal("launched after the game ended")
	}
}

// Every level must be clearable with its pig budget: a greedy player that
// picks the best aimed shot for each pig clears all three levels. The world is
// fixed, so this holds at every terminal size.
func TestEveryLevelIsClearable(t *testing.T) {
	for level := range levels {
		g := newGame(80, 0)
		g.loadLevel(level)
		for g.pigsLeft > 0 && !g.levelDone {
			bestAngle, bestPower, bestBirds, bestScore := 0, 0, g.birdsLeft()+1, -1
			for angle := 5; angle <= 85; angle += 2 {
				for power := 10; power <= 100; power += powerStep {
					trial := *g
					trial.angle, trial.power = angle, power
					trial.launch()
					fly(&trial)
					if left := trial.birdsLeft(); left < bestBirds || left == bestBirds && trial.score > bestScore {
						bestAngle, bestPower, bestBirds, bestScore = angle, power, left, trial.score
					}
				}
			}
			g.angle, g.power = bestAngle, bestPower
			g.launch()
			fly(g)
		}
		if !g.levelDone {
			t.Fatalf("level %d: %d birds left after %d pigs", level+1, g.birdsLeft(), levelPigs[level])
		}
	}
}

// Pulling further back launches harder: launch speed rises strictly with
// power at every angle, and the pouch sits further from its rest point.
func TestPowerRaisesLaunchSpeedMonotonically(t *testing.T) {
	g := newGame(80, 0)
	for _, angle := range []int{5, 30, 45, 60, 85} {
		g.angle = angle
		previousSpeed, previousPull := 0.0, 0.0
		for power := 10; power <= 100; power++ {
			g.power = power
			v := g.launchVelocity()
			speed := math.Hypot(v.x, v.y)
			p := g.pouch()
			pull := math.Hypot(p.x-slingX, p.y-slingY)
			if speed <= previousSpeed || pull <= previousPull {
				t.Fatalf("angle %d power %d: speed %v pull %v did not rise from %v %v", angle, power, speed, pull, previousSpeed, previousPull)
			}
			previousSpeed, previousPull = speed, pull
		}
	}
	g.power = 100
	if got := math.Hypot(g.launchVelocity().x, g.launchVelocity().y); math.Abs(got-maxSpeed) > 1e-9 {
		t.Fatalf("full power speed = %v, want %v", got, maxSpeed)
	}
}

func TestPowerKeysClampAtMinimumAndMaximum(t *testing.T) {
	c := &component{game: newGame(80, 0), scene: newScene(defaultPalette()), height: func() int { return 24 }, wake: make(chan struct{}, 1)}
	c.game.intro = 0
	for range 60 {
		_, _ = c.HandleInput("\x1b[A")
	}
	if c.game.power != 100 {
		t.Fatalf("power after pulling all the way = %d, want 100", c.game.power)
	}
	for range 60 {
		_, _ = c.HandleInput("\x1b[B")
	}
	if c.game.power != 10 {
		t.Fatalf("power after easing all the way = %d, want 10", c.game.power)
	}
}

// The preview is the real flight: every dot is where the launched pig is
// after the same number of physics steps.
func TestPreviewMatchesTheRealFlight(t *testing.T) {
	for _, aim := range [][2]int{{45, 70}, {20, 100}, {70, 40}, {33, 88}} {
		g := emptyField()
		g.angle, g.power = aim[0], aim[1]
		var buf [24]point
		dots := append([]point(nil), g.preview(buf[:0])...)
		if len(dots) < 4 {
			t.Fatalf("aim %v: preview has %d dots", aim, len(dots))
		}
		g.launch()
		for i, dot := range dots {
			for range previewEvery {
				g.step()
			}
			if !g.flying || g.pig != dot {
				t.Fatalf("aim %v dot %d: preview %+v, flight %+v (flying %v)", aim, i, dot, g.pig, g.flying)
			}
		}
	}
}

// Dragging with the mouse aims like the real game: the pull length sets the
// power, the pig launches away from the pointer, and release fires.
func TestMouseDragPullsAndReleaseFires(t *testing.T) {
	c := &component{game: emptyField(), scene: newScene(defaultPalette()), height: func() int { return 24 }, wake: make(chan struct{}, 1)}
	c.game.resize(78)
	c.game.camX = 0
	c.scene.render(c.game, 78, 22, nil)
	// Beyond maxPull left of and below the rest point: full power at 45°.
	restX, restY := c.scene.screen(slingX, slingY)
	pointerX, pointerY := restX-12, restY+12
	press := "\x1b[<0;" + strconv.Itoa(pointerX+2) + ";" + strconv.Itoa((pointerY-1)/2+2) + "M"
	_, _ = c.HandleInput(press)
	if !c.dragging || c.game.power != 100 || c.game.angle < 40 || c.game.angle > 50 {
		t.Fatalf("drag = dragging %v, power %d, angle %d", c.dragging, c.game.power, c.game.angle)
	}
	near := "\x1b[<32;" + strconv.Itoa(restX+2-3) + ";" + strconv.Itoa((restY+1)/2+2) + "M"
	_, _ = c.HandleInput(near)
	if c.game.power >= 50 || c.game.power < 10 {
		t.Fatalf("a short pull gave power %d", c.game.power)
	}
	_, _ = c.HandleInput("\x1b[<0;" + strconv.Itoa(restX+2-3) + ";" + strconv.Itoa((restY+1)/2+2) + "m")
	if c.dragging || !c.game.flying {
		t.Fatal("release did not fire the pig")
	}
}

func TestPreviewTracesTheAimAndStopsAtStructures(t *testing.T) {
	g := emptyField()
	var buf [24]point
	open := g.preview(buf[:0])
	if len(open) < 5 {
		t.Fatalf("preview has %d points", len(open))
	}
	for i := 1; i < len(open); i++ {
		if open[i].x <= open[i-1].x {
			t.Fatal("preview does not advance toward the target")
		}
	}
	for cy := range gridRows {
		place(g, int(open[1].x)/cellSize+1, cy, cellStone)
	}
	if blocked := g.preview(buf[:0]); len(blocked) >= len(open) {
		t.Fatalf("preview passed through a wall: %d points", len(blocked))
	}
	g.launch()
	if len(g.preview(buf[:0])) != 0 {
		t.Fatal("preview shown during flight")
	}
}

func TestCameraShowsTheFortressFollowsThePigAndReturns(t *testing.T) {
	g := newGame(80, 0)
	g.resize(80)
	if g.camX != worldW-80 {
		t.Fatalf("level start camera = %v, want the fortress end %d", g.camX, worldW-80)
	}
	for range int(introHold/stepSeconds) + 240 {
		g.step()
	}
	if g.camX != 0 {
		t.Fatalf("after the intro the camera = %v, want the slingshot", g.camX)
	}
	g.angle, g.power = 40, 90
	g.launch()
	for range 60 {
		g.step()
	}
	if g.camX <= 0 {
		t.Fatal("the camera did not follow the pig")
	}
	wide := newGame(300, 0)
	wide.resize(300)
	if want := -float64(300-worldW) / 2; wide.camX != want {
		t.Fatalf("wide view camera = %v, want the centered world %v", wide.camX, want)
	}
}

// Every rendered view fills exactly the overlay's content area: w-2 cells by
// h-2 lines, for tiny, narrow, ordinary, and wide terminals.
func TestRenderFillsTheViewAtEverySize(t *testing.T) {
	t.Setenv("COLORTERM", "truecolor")
	for _, size := range [][2]int{{1, 1}, {3, 3}, {12, 6}, {40, 12}, {80, 24}, {123, 40}, {200, 60}, {400, 120}} {
		w, h := size[0], size[1]
		c := newComponent(0, standardlogin.FindVariant(""), func() int { return h })
		_, _ = c.HandleInput(" ")
		for range 3 {
			c.mu.Lock()
			for range 20 {
				c.game.step()
			}
			c.mu.Unlock()
			lines := c.Render(w)
			viewW, viewH := max(w-2, 1), max(h-2, 1)
			if len(lines) != viewH {
				t.Fatalf("%dx%d: %d lines, want %d", w, h, len(lines), viewH)
			}
			for i, line := range lines {
				if got := visibleWidth(line); got != viewW {
					t.Fatalf("%dx%d line %d: width %d, want %d: %q", w, h, i, got, viewW, line)
				}
			}
		}
		c.Dispose()
	}
}

// A view resized from one size to another renders exactly what a fresh view
// at the new size renders: no stale pixels, lines, or text survive a resize.
func TestResizeRedrawsCleanly(t *testing.T) {
	t.Setenv("COLORTERM", "truecolor")
	g := newGame(80, 0)
	g.launch()
	for range 90 {
		g.step()
	}
	resized := newScene(defaultPalette())
	for _, size := range [][2]int{{160, 48}, {50, 16}, {98, 30}} {
		g.resize(size[0])
		got := resized.render(g, size[0], size[1], nil)
		want := newScene(defaultPalette()).render(g, size[0], size[1], nil)
		if !slices.Equal(got, want) {
			t.Fatalf("after resizing to %dx%d the view differs from a fresh render", size[0], size[1])
		}
	}
}

func TestComponentRedrawsOnHeightChange(t *testing.T) {
	var height atomic.Int64
	height.Store(24)
	c := newComponent(0, standardlogin.FindVariant(""), func() int { return int(height.Load()) })
	defer c.Dispose()
	invalidated := make(chan struct{}, 64)
	c.SetInvalidate(func() {
		select {
		case invalidated <- struct{}{}:
		default:
		}
	})
	time.Sleep(3 * idleInterval)
	for len(invalidated) > 0 {
		<-invalidated
	}
	height.Store(41)
	select {
	case <-invalidated:
	case <-time.After(4 * idleInterval):
		t.Fatal("a height change did not request a frame")
	}
	if got := len(c.Render(90)); got != 39 {
		t.Fatalf("after resizing to 41 rows the view has %d lines, want 39", got)
	}
}

// The launched pig, the loaded pig, and the HUD icons use the selected login
// sprite's colors: the website green pig by default.
func TestPigsUseTheSelectedSprite(t *testing.T) {
	count := func(variant standardlogin.Variant) (int, int) {
		g := newGame(120, 0)
		g.resize(120)
		g.intro = 0
		g.camX = 0
		s := newScene(pixel.NewPalette(standardlogin.MascotPalette(variant)))
		s.render(g, 120, 40, nil)
		green, pink := 0, 0
		for _, px := range s.canvas.Px {
			switch px {
			case standardlogin.FindVariant("hpe-agentic").Body:
				green++
			case standardlogin.FindVariant("pink").Body:
				pink++
			}
		}
		return green, pink
	}
	if green, pink := count(standardlogin.FindVariant("")); green < 20 || pink != 0 {
		t.Fatalf("default sprite: %d green and %d pink pig pixels", green, pink)
	}
	if green, pink := count(standardlogin.FindVariant("pink")); pink < 20 || green != 0 {
		t.Fatalf("pink sprite: %d green and %d pink pig pixels", green, pink)
	}
}

func TestComponentControls(t *testing.T) {
	c := &component{game: newGame(80, 0), scene: newScene(defaultPalette()), height: func() int { return 24 }, wake: make(chan struct{}, 1)}
	for _, tc := range []struct {
		in           string
		angle, power int
	}{
		// Up and Down pull the band back further or ease it; Left and Right
		// raise and lower the aim; Kitty releases do nothing.
		{"\x1b[A", 45, 73}, {"\x1b[1;1:1A", 45, 76}, {"\x1b[B", 45, 73}, {"\x1bOD", 46, 73}, {"\x1b[C", 45, 73}, {"\x1b[1;1:3A", 45, 73},
	} {
		_, _ = c.HandleInput(tc.in)
		if c.game.angle != tc.angle || c.game.power != tc.power {
			t.Fatalf("after %q aim = %d°/%d%%, want %d°/%d%%", tc.in, c.game.angle, c.game.power, tc.angle, tc.power)
		}
	}
	_, _ = c.HandleInput("\r")
	if !c.game.flying {
		t.Fatal("enter did not launch")
	}
	for _, in := range []string{"q", "Q", "\x1b", "\x1b[27u"} {
		result, err := c.HandleInput(in)
		if err != nil || !result.Done {
			t.Fatalf("quit input %q = %+v, %v", in, result, err)
		}
	}
}

func TestComponentQuitReturnsStateAndDisposeStopsTheTicker(t *testing.T) {
	c := newComponent(1200, standardlogin.FindVariant(""), func() int { return 24 })
	invalidated := make(chan struct{}, 64)
	c.SetInvalidate(func() {
		select {
		case invalidated <- struct{}{}:
		default:
		}
	})
	if _, err := c.HandleInput(" "); err != nil {
		t.Fatal(err)
	}
	select {
	case <-invalidated:
	case <-time.After(2 * time.Second):
		t.Fatal("a flying pig did not invalidate the component")
	}
	result, err := c.HandleInput("q")
	if err != nil {
		t.Fatal(err)
	}
	state, ok := result.Value.(gameState)
	if !result.Done || !ok || state.HighScore < 1200 || state.Level != 1 {
		t.Fatalf("quit result = %+v", result)
	}
	c.SetInvalidate(nil)
	done := make(chan struct{})
	go func() { c.Dispose(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Dispose did not stop the ticker")
	}
}

// The physics step never allocates, and a frame allocates only the strings of
// lines whose pixels changed: nothing at all when the scene is unchanged.
func TestFramesDoNotChurnAllocations(t *testing.T) {
	t.Setenv("COLORTERM", "truecolor")
	g := newGame(160, 0)
	g.resize(160)
	g.intro = 0
	g.camX = 0
	s := newScene(defaultPalette())
	lines := make([]string, 0, 64)
	lines = s.render(g, 160, 50, lines[:0])
	if still := testing.AllocsPerRun(20, func() { lines = s.render(g, 160, 50, lines[:0]) }); still != 0 {
		t.Fatalf("an unchanged frame allocates %.1f times, want 0", still)
	}
	g.launch()
	if steps := testing.AllocsPerRun(200, g.step); steps != 0 {
		t.Fatalf("a physics step allocates %.1f times, want 0", steps)
	}
	flying := testing.AllocsPerRun(20, func() {
		g.step()
		g.step()
		lines = s.render(g, 160, 50, lines[:0])
	})
	if flying > 50 {
		t.Fatalf("a flight frame allocates %.1f times, want at most one per line (50)", flying)
	}
}

func BenchmarkFrame200x60(b *testing.B) {
	b.Setenv("COLORTERM", "truecolor")
	g := newGame(198, 0)
	g.resize(198)
	s := newScene(defaultPalette())
	lines := make([]string, 0, 64)
	b.ReportAllocs()
	for b.Loop() {
		g.step()
		g.step()
		if !g.flying && !g.gameOver && !g.levelDone {
			g.launch()
		}
		if g.gameOver || g.levelDone {
			g.restart()
		}
		lines = s.render(g, 198, 58, lines[:0])
	}
}

func BenchmarkStep(b *testing.B) {
	g := newGame(80, 0)
	b.ReportAllocs()
	for b.Loop() {
		if !g.flying {
			g.restart()
			g.launch()
		}
		g.step()
	}
}

func TestExtensionRegistersAngryPigsCommand(t *testing.T) {
	extensionConn, hostConn := net.Pipe()
	defer func() { _ = hostConn.Close() }()
	done := make(chan error, 1)
	go func() { done <- Extension().RunWithConn(extensionConn) }()

	var header [4]byte
	if _, err := io.ReadFull(hostConn, header[:]); err != nil {
		t.Fatal(err)
	}
	frame := make([]byte, binary.BigEndian.Uint32(header[:]))
	if _, err := io.ReadFull(hostConn, frame); err != nil {
		t.Fatal(err)
	}
	var registration struct {
		Register struct {
			Name     string `json:"name"`
			Commands []struct {
				Name string `json:"name"`
			} `json:"commands"`
		} `json:"register"`
	}
	if err := json.Unmarshal(frame, &registration); err != nil {
		t.Fatal(err)
	}
	if registration.Register.Name != "angrypigs" || len(registration.Register.Commands) != 1 || registration.Register.Commands[0].Name != "angry-pigs" {
		t.Fatalf("registration = %+v", registration.Register)
	}
	writeFrame := func(value any) {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		binary.BigEndian.PutUint32(header[:], uint32(len(data)))
		if _, err := hostConn.Write(header[:]); err != nil {
			t.Fatal(err)
		}
		if _, err := hostConn.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	writeFrame(map[string]any{"type": "ready", "ready": map[string]any{"width": 80}})
	writeFrame(map[string]any{"type": "shutdown", "shutdown": map[string]any{"reason": "test"}})
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("extension did not stop after shutdown")
	}
}

// Angry Pigs wears PiG Runner's look: the same two HUD lines under the
// playfield, the same night sky and turf, and the shared title card until
// the player presses space.
func TestAngryPigsSharesTheRunnerStyle(t *testing.T) {
	t.Setenv("COLORTERM", "truecolor")
	c := newComponent(0, standardlogin.FindVariant(""), func() int { return 40 })
	defer c.Dispose()
	lines := c.Render(120)
	hud := sgrPattern.ReplaceAllString(lines[len(lines)-2], "")
	if !strings.HasPrefix(hud, " ANGRY PIGS  score 0000  high 0000  ↑↓ pull ←→ aim space fire") {
		t.Fatalf("HUD title line = %q", hud)
	}
	if status := sgrPattern.ReplaceAllString(lines[len(lines)-1], ""); !strings.Contains(status, "space start") {
		t.Fatalf("title-screen status = %q", status)
	}
	c.mu.Lock()
	sky, road, card := c.scene.canvas.At(0, 0), c.scene.canvas.At(0, c.scene.ground()), 0
	for _, px := range c.scene.canvas.Px {
		if px == arcade.Accent {
			card++
		}
	}
	c.mu.Unlock()
	if sky != arcade.SkyTop || road != arcade.Accent {
		t.Fatalf("sky %v and road %v are not the runner's", sky, road)
	}
	if card < c.scene.canvas.W+50 {
		t.Fatal("the title card is missing")
	}
	if _, err := c.HandleInput(" "); err != nil || c.waiting {
		t.Fatal("space did not leave the title screen")
	}
	if c.game.flying {
		t.Fatal("the space that starts the game also fired a pig")
	}
}
