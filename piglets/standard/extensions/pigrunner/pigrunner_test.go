package pigrunner

import (
	"math/rand/v2"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	standardlogin "github.com/MichaelKinsy/PiG/piglets/standard/extensions/piglogin"
	"github.com/MichaelKinsy/PiG/piglets/standard/internal/arcade"
	"github.com/MichaelKinsy/PiG/piglets/standard/internal/pixel"
)

var sgrPattern = regexp.MustCompile("\x1b\\[[0-9;]*m")

// visibleWidth counts terminal cells: SGR sequences take none and
// pictographic emoji take two.
func visibleWidth(line string) int {
	width := 0
	for _, r := range sgrPattern.ReplaceAllString(line, "") {
		width += pixel.CellWidth(r)
	}
	return width
}

func renderGame(g *Game, pixelHeight int) []string {
	var buffers frameBuffers
	return g.RenderLines(&buffers, g.Width, pixelHeight, true, nil)
}

func TestNewGame(t *testing.T) {
	g := NewGame(80, 42)
	if g.Over {
		t.Fatal("new game should not be over")
	}
	if g.HighScore != 42 {
		t.Fatalf("HighScore = %d; want 42", g.HighScore)
	}
	if g.Pig.Y != groundY {
		t.Fatalf("Pig.Y = %v; want %d (groundY)", g.Pig.Y, groundY)
	}
}

func TestJump(t *testing.T) {
	g := NewGame(80, 0)
	g.Jump()
	if g.Pig.VelY >= 0 {
		t.Fatal("after Jump, VelY should be negative (upward)")
	}
	// Pig is in the air after one update — can't double-jump.
	g.Update()
	prevVel := g.Pig.VelY
	g.Jump() // should be no-op (not on ground)
	if g.Pig.VelY != prevVel {
		t.Fatal("should not be able to double-jump")
	}
}

func TestDuck(t *testing.T) {
	g := NewGame(80, 0)
	g.Duck()
	if g.Pig.Ducked == 0 {
		t.Fatal("after Duck, Ducked should be > 0")
	}
}

func TestUpdate_ScoreAdvances(t *testing.T) {
	g := NewGame(80, 0)
	for range 10 {
		g.Update()
	}
	if g.Score == 0 {
		t.Fatal("score should advance after ticks")
	}
}

func TestRender_ProducesOutput(t *testing.T) {
	// The playfield downsamples to 256 colours when COLORTERM is unset, which is
	// correct and emits none of the truecolor sequences asserted below.
	t.Setenv("COLORTERM", "truecolor")
	g := NewGame(80, 0)
	g.Update()
	frame := strings.Join(renderGame(g, screenH), "\n")
	if !strings.Contains(frame, "PiG RUNNER") {
		t.Fatal("frame should contain runner title")
	}
	if !strings.Contains(frame, "\x1b[38;2;") {
		t.Fatal("frame should contain truecolor ANSI art")
	}
	if !strings.Contains(frame, "score") {
		t.Fatal("frame should contain score HUD")
	}
}

func TestRenderDownsamplesWithoutTrueColor(t *testing.T) {
	t.Setenv("COLORTERM", "")
	t.Setenv("WT_SESSION", "")
	frame := strings.Join(renderGame(NewGame(80, 0), screenH), "\n")
	if strings.Contains(frame, "38;2;") || !strings.Contains(frame, "\x1b[38;5;") {
		t.Fatal("frame without 24-bit color support must use 256-color SGR")
	}
}

func TestTogglePause(t *testing.T) {
	g := NewGame(80, 0)
	g.TogglePause()
	if !g.Paused {
		t.Fatal("should be paused")
	}
	// Update should be a no-op when paused.
	if g.Update() {
		t.Fatal("Update should return false when paused")
	}
	g.TogglePause()
	if g.Paused {
		t.Fatal("should be unpaused")
	}
}

func TestPigSpritesHaveUniformWidth(t *testing.T) {
	for name, rows := range map[string][]string{
		"runA": pigRunA,
		"runB": pigRunB,
		"duck": pigDuck,
	} {
		want := len(rows[0])
		for y, row := range rows {
			if len(row) != want {
				t.Fatalf("%s row %d width=%d want=%d", name, y, len(row), want)
			}
		}
	}
}

func TestPieSpritesHaveUniformWidth(t *testing.T) {
	for name, rows := range map[string][]string{
		"groundPieA": groundPieA,
		"groundPieB": groundPieB,
		"flyingPie":  flyingPie,
	} {
		want := obWidth
		for y, row := range rows {
			if len(row) != want {
				t.Fatalf("%s row %d width=%d want=%d", name, y, len(row), want)
			}
		}
	}
}

func TestJumpClearsGroundObstacleAtApex(t *testing.T) {
	g := NewGame(80, 0)
	ob := Obstacle{Kind: ObGround, X: float64(pigX + 4), Label: "nil"}
	if !g.collides(ob) {
		t.Fatal("standing pig should collide with ground obstacle")
	}
	g.Jump()
	for range 5 {
		g.Update()
	}
	if g.collides(ob) {
		t.Fatal("jump apex should clear ground obstacle")
	}
}

func TestDuckClearsAirObstacle(t *testing.T) {
	g := NewGame(80, 0)
	air := Obstacle{Kind: ObAir, X: float64(pigX + 2), Label: "drone"}
	if !g.collides(air) {
		t.Fatal("standing pig should collide with air obstacle")
	}
	g.Duck()
	if g.collides(air) {
		t.Fatal("ducking pig should pass under air obstacle")
	}
}

// Speed follows the Dino: it starts at 6, never decreases, and reaches the
// 13 cap after (13-6)/0.001 = 7000 frames, under two minutes.
func TestSpeedIsMonotonicAndReachesTheCap(t *testing.T) {
	g := NewGame(80, 0)
	g.rng = rand.New(rand.NewPCG(1, 1))
	if g.Speed != dinoSpeed {
		t.Fatalf("start speed = %v, want %v", g.Speed, dinoSpeed)
	}
	capFrame := int((dinoMaxSpeed-dinoSpeed)/dinoAcceleration) + 1
	previous := g.Speed
	for frame := 1; frame <= capFrame; frame++ {
		g.Obstacles = g.Obstacles[:0] // keep the pig alive; speed is time-based
		g.Update()
		if g.Speed < previous {
			t.Fatalf("frame %d: speed fell from %v to %v", frame, previous, g.Speed)
		}
		previous = g.Speed
	}
	if g.Speed != dinoMaxSpeed {
		t.Fatalf("speed after %d frames = %v, want the cap %v", capFrame, g.Speed, dinoMaxSpeed)
	}
	if capFrame > 120*frameHz {
		t.Fatalf("the cap takes %d frames, want under two minutes", capFrame)
	}
	g.Obstacles = g.Obstacles[:0]
	g.Update()
	if g.Speed != dinoMaxSpeed {
		t.Fatal("speed passed the cap")
	}
}

// Obstacles come faster as speed rises: the Dino gap grows with speed in
// pixels but shrinks in time, groups appear above their multiple speed, and
// flying pies appear only from speed 8.5.
func TestObstacleDensityRisesWithSpeed(t *testing.T) {
	rate := func(speed float64) (piesPerSecond float64, air, groups int) {
		g := NewGame(160, 0)
		g.rng = rand.New(rand.NewPCG(7, 7))
		g.Tick = dinoClearFrames + 1
		const seconds = 120
		for range seconds * frameHz {
			g.Speed = speed
			g.Pig, g.Over = Pig{Y: groundY}, false
			before := g.spawned
			g.Update()
			if g.spawned != before {
				last := g.Obstacles[len(g.Obstacles)-1]
				piesPerSecond += float64(last.Size) / seconds
				if last.Kind == ObAir {
					air++
				}
				if last.Size > 1 {
					groups++
				}
			}
		}
		return piesPerSecond, air, groups
	}
	slow, slowAir, slowGroups := rate(6)
	mid, _, _ := rate(9)
	fast, fastAir, fastGroups := rate(13)
	if !(slow < mid && mid < fast) {
		t.Fatalf("pies per second at speed 6/9/13 = %.2f/%.2f/%.2f, want rising", slow, mid, fast)
	}
	if slowAir != 0 || fastAir == 0 {
		t.Fatalf("flying pies at speed 6/13 = %d/%d, want none below 8.5 and some above", slowAir, fastAir)
	}
	if fastGroups <= slowGroups {
		t.Fatalf("groups at speed 6/13 = %d/%d, want more at speed", slowGroups, fastGroups)
	}
}

// The runner pig uses the selected login sprite's colors; the default is the
// website's green pig.
func TestVariantPaletteMatchesLoginSelection(t *testing.T) {
	g := NewGameWithVariant(80, 0, standardlogin.FindVariant("hpe-agentic"))
	body := g.PigPalette['P']
	if body.R != 0x48 || body.G != 0xA3 || body.B != 0x81 {
		t.Fatalf("hpe-agentic body = %#v; want the website pig green", body)
	}
	if g.PigPalette['e'] != body {
		t.Fatalf("hpe-agentic inner ear = %#v; want body %#v", g.PigPalette['e'], body)
	}
	if NewGame(80, 0).PigPalette['P'] != body {
		t.Fatal("the default runner pig is not the website green pig")
	}
}

func TestCustomVariantRunnerSprites(t *testing.T) {
	g := NewGameWithVariant(80, 0, standardlogin.FindVariant("sheriff"))
	if len(g.PigRunA) != 12 || len(g.PigRunB) != 12 {
		t.Fatalf("sheriff runner frames = %d/%d rows, want 12/12", len(g.PigRunA), len(g.PigRunB))
	}
	for name, rows := range map[string][]string{"runA": g.PigRunA, "runB": g.PigRunB} {
		for i, row := range rows {
			if len(row) != 14 {
				t.Fatalf("%s row %d width = %d, want 14", name, i, len(row))
			}
		}
	}
	if g.PigPalette['S'].R != 0xFF || g.PigPalette['H'].A == 0 {
		t.Fatalf("sheriff hat colors missing from the runner palette: %#v", g.PigPalette)
	}
}

// Every rendered line is exactly the view width, and the view fills the
// requested height, for narrow, ordinary, and wide terminals.
func TestRenderFillsTheViewAtEverySize(t *testing.T) {
	t.Setenv("COLORTERM", "truecolor")
	for _, size := range [][2]int{{1, 3}, {20, 8}, {40, 13}, {80, 24}, {132, 43}, {300, 90}} {
		w, h := size[0], size[1]
		component := newRunnerComponent(0, standardlogin.FindVariant(""), func() int { return h })
		lines := component.Render(w)
		component.Dispose()
		viewW, viewH := max(w-2, 1), max(h-2, 1)
		if len(lines) != viewH {
			t.Fatalf("%dx%d: %d lines, want %d", w, h, len(lines), viewH)
		}
		for i, line := range lines {
			if got := visibleWidth(line); got != viewW {
				t.Fatalf("%dx%d line %d: width %d, want %d", w, h, i, got, viewW)
			}
			if strings.Contains(line, "\x1b[K") {
				t.Fatalf("%dx%d line %d clears to end of line inside the overlay box", w, h, i)
			}
		}
	}
}

// A taller view keeps the original band geometry at the bottom: the road line
// is the third pixel row from the bottom of the playfield.
func TestTallViewKeepsTheGameBandAtTheBottom(t *testing.T) {
	g := NewGame(80, 0)
	var buffers frameBuffers
	for _, height := range []int{screenH, 60} {
		g.RenderLines(&buffers, g.Width, height, true, nil)
		if got := buffers.canvas.At(0, height-screenH+groundY); got != arcade.Accent {
			t.Fatalf("height %d: road pixel = %#v, want %#v", height, got, arcade.Accent)
		}
	}
}

func TestComponentRunsPausesAndResumes(t *testing.T) {
	component := newRunnerComponent(0, standardlogin.FindVariant(""), func() int { return 24 })
	defer component.Dispose()
	_, _ = component.HandleInput(" ")
	score := func() float64 {
		component.mu.Lock()
		defer component.mu.Unlock()
		return component.game.Distance
	}

	initial := score()
	time.Sleep(4 * tickRate)
	running := score()
	if running <= initial {
		t.Fatalf("runner score did not advance without input: initial=%v running=%v", initial, running)
	}
	_, _ = component.HandleInput("p")
	paused := score()
	time.Sleep(4 * tickRate)
	if still := score(); still != paused {
		t.Fatalf("paused runner score changed: paused=%v after=%v", paused, still)
	}
	_, _ = component.HandleInput("p")
	time.Sleep(4 * tickRate)
	if resumed := score(); resumed <= paused {
		t.Fatalf("resumed runner score did not advance: paused=%v resumed=%v", paused, resumed)
	}
}

func TestComponentRenderUsesLatestResizeWidth(t *testing.T) {
	component := newRunnerComponent(0, standardlogin.FindVariant(""), func() int { return 24 })
	defer component.Dispose()
	for _, width := range []int{122, 74, 20} {
		lines := component.Render(width)
		want := width - 2
		for i, line := range lines {
			if got := visibleWidth(line); got != want {
				t.Fatalf("width %d line %d visible width = %d, want %d", width, i, got, want)
			}
		}
	}
}

// A height change alone requests a new frame, even while the game is paused.
func TestComponentRedrawsOnHeightChange(t *testing.T) {
	var height atomicInt
	height.Store(24)
	component := newRunnerComponent(0, standardlogin.FindVariant(""), height.Load)
	defer component.Dispose()
	_, _ = component.HandleInput("p")
	invalidated := make(chan struct{}, 16)
	component.SetInvalidate(func() {
		select {
		case invalidated <- struct{}{}:
		default:
		}
	})
	time.Sleep(3 * tickRate)
	for len(invalidated) > 0 {
		<-invalidated
	}
	height.Store(40)
	select {
	case <-invalidated:
	case <-time.After(10 * tickRate):
		t.Fatal("a height change did not request a frame")
	}
	if got := len(component.Render(80)); got != 38 {
		t.Fatalf("after resize to 40 rows the view has %d lines, want 38", got)
	}
}

// TestComponentHandleInput_KittyAndLegacy pins the input decoding: jump and
// duck fire for legacy, application-cursor, and Kitty press encodings, Kitty
// releases do nothing, and quit accepts Escape in any encoding.
func TestComponentHandleInput_KittyAndLegacy(t *testing.T) {
	newComponent := func() *runnerComponent {
		return &runnerComponent{game: NewGameWithVariant(64, 0, standardlogin.FindVariant("")), height: func() int { return 24 }}
	}
	for _, in := range []string{" ", "w", "W", "\x1b[A", "\x1bOA", "\x1b[1;1:1A"} {
		c := newComponent()
		_, _ = c.HandleInput(in)
		if want := -(dinoJumpVelocity + dinoSpeed/10) * dinoScale; c.game.Pig.VelY != want {
			t.Errorf("input %q did not jump: VelY=%v want %v", in, c.game.Pig.VelY, want)
		}
	}
	for _, in := range []string{"s", "S", "\x1b[B", "\x1b[1;1:1B"} {
		c := newComponent()
		_, _ = c.HandleInput(in)
		if c.game.Pig.Ducked != duckFrames {
			t.Errorf("input %q did not duck: Ducked=%d want %d", in, c.game.Pig.Ducked, duckFrames)
		}
	}
	for _, in := range []string{"\x1b[1;1:3A", "\x1b[1;1:3B"} {
		c := newComponent()
		_, _ = c.HandleInput(in)
		if c.game.Pig.VelY != 0 || c.game.Pig.Ducked != 0 {
			t.Errorf("key release %q acted on the game", in)
		}
	}
	for _, in := range []string{"q", "Q", "\x1b", "\x1b[27u"} {
		result, err := newComponent().HandleInput(in)
		if err != nil || !result.Done {
			t.Errorf("quit input %q did not finish the component: %+v, %v", in, result, err)
		}
	}
}

func TestSteadyStateFrameAllocations(t *testing.T) {
	t.Setenv("COLORTERM", "truecolor")
	g := NewGame(120, 0)
	var buffers frameBuffers
	lines := make([]string, 0, 64)
	g.RenderLines(&buffers, g.Width, 80, true, lines[:0])
	// A running frame re-encodes the rows the animation changed, one string
	// each; the canvas, encoder buffer, and HUD are reused.
	allocs := testing.AllocsPerRun(50, func() {
		g.Update()
		lines = g.RenderLines(&buffers, g.Width, 80, true, lines[:0])
	})
	if max := float64(80/2 + hudLines); allocs > max {
		t.Fatalf("a frame allocates %.1f times, want at most one per line (%.0f)", allocs, max)
	}
	still := testing.AllocsPerRun(50, func() { lines = g.RenderLines(&buffers, g.Width, 80, true, lines[:0]) })
	if still != 0 {
		t.Fatalf("an unchanged frame allocates %.1f times, want 0", still)
	}
}

func BenchmarkRunnerFrame(b *testing.B) {
	b.Setenv("COLORTERM", "truecolor")
	g := NewGame(200, 0)
	var buffers frameBuffers
	lines := make([]string, 0, 64)
	b.ReportAllocs()
	for b.Loop() {
		g.Update()
		if g.Over {
			g = NewGame(200, g.HighScore)
		}
		lines = g.RenderLines(&buffers, g.Width, 100, true, lines[:0])
	}
}

type atomicInt struct{ v atomic.Int64 }

func (a *atomicInt) Store(v int) { a.v.Store(int64(v)) }
func (a *atomicInt) Load() int   { return int(a.v.Load()) }

// PiG Runner opens on the shared title card and waits for space; a crash
// shows the shared game-over card and a red score.
func TestRunnerTitleAndGameOverScreens(t *testing.T) {
	t.Setenv("COLORTERM", "truecolor")
	component := newRunnerComponent(0, standardlogin.FindVariant(""), func() int { return 40 })
	defer component.Dispose()
	lines := component.Render(120)
	if status := sgrPattern.ReplaceAllString(lines[len(lines)-1], ""); !strings.Contains(status, "space start") {
		t.Fatalf("title status = %q", status)
	}
	time.Sleep(3 * tickRate)
	component.mu.Lock()
	waitingTick := component.game.Tick
	component.mu.Unlock()
	if waitingTick != 0 {
		t.Fatal("the game ran on the title screen")
	}
	_, _ = component.HandleInput(" ")
	component.mu.Lock()
	component.game.Over = true
	component.mu.Unlock()
	lines = component.Render(120)
	if !strings.Contains(lines[len(lines)-2], "\x1b[91;1mscore") || !strings.Contains(lines[len(lines)-1], "CRASHED") {
		t.Fatalf("game-over HUD = %q", lines[len(lines)-2:])
	}
	accent := 0
	for _, px := range component.buffers.canvas.Px {
		if px == arcade.Accent {
			accent++
		}
	}
	if accent < component.buffers.canvas.W+50 {
		t.Fatal("the game-over card is missing")
	}
}
