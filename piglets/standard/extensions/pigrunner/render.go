package pigrunner

import (
	"github.com/MichaelKinsy/PiG/piglets/standard/internal/arcade"
	"github.com/MichaelKinsy/PiG/piglets/standard/internal/pixel"
)

const (
	minWidth = 40
	// hudLines is the number of text lines below the playfield.
	hudLines = arcade.HUDLines
)

var hazardPal = pixel.NewPalette(hazardPalette)

// frameBuffers are the reusable buffers for one runner view.
type frameBuffers struct {
	canvas  pixel.Canvas
	encoder pixel.Encoder
	hud     []string
	hudKey  hudKey
}

type hudKey struct {
	score, highScore, width          int
	over, paused, trueColor, waiting bool
}

// RenderLines renders a view viewWidth cells wide: the playfield pixelHeight
// pixels tall, then the HUD when hud is true. Every line is exactly viewWidth
// cells wide. The game band keeps the original 22-pixel geometry at the
// bottom; a taller view extends the night sky above it, and a shorter view
// crops the top of the sky. A view narrower than the game shows its left part.
func (g *Game) RenderLines(buffers *frameBuffers, viewWidth, pixelHeight int, hud bool, dst []string) []string {
	viewWidth, pixelHeight = max(viewWidth, 1), max(pixelHeight, 2)
	c := &buffers.canvas
	c.Resize(viewWidth, pixelHeight)
	oy := pixelHeight - screenH
	// The scenery keeps the original 20 Hz animation cadence.
	drawBackground(c, g.Tick/3, viewWidth, oy)
	drawObstacles(c, g, oy)
	drawPig(c, g, oy)
	switch {
	case g.Waiting:
		arcade.DrawCard(c, "PIG RUNNER", "PRESS SPACE", 1)
	case g.Over:
		arcade.DrawCard(c, "GAME OVER", "R RETRY", 1)
	}
	buffers.encoder.TrueColor = pixel.SupportsTrueColor()
	dst = buffers.encoder.Encode(c, dst)
	if !hud {
		return dst
	}
	key := hudKey{g.Score, g.HighScore, viewWidth, g.Over, g.Paused, buffers.encoder.TrueColor, g.Waiting}
	if buffers.hud == nil || key != buffers.hudKey {
		buffers.hud, buffers.hudKey = renderHUDLines(g, key.trueColor, viewWidth), key
	}
	return append(dst, buffers.hud...)
}

func drawBackground(c *pixel.Canvas, tick, w, oy int) {
	// Sky gradient.
	for y := range c.H {
		c.Rect(0, y, w, 1, arcade.SkyColor(y, c.H))
	}

	// Stars. The first sixteen are the original band's stars; a taller view
	// adds farther stars that drift more slowly.
	for i := range 16 {
		x := (i*17 + tick/8) % w
		y := 1 + (i*3)%9
		c.Set(x, oy+y, arcade.Star)
	}
	if oy > 1 {
		for i := range 16 * oy / screenH {
			h := uint32(i+1) * 2654435761
			x := (int(h%uint32(w)) + tick/14) % w
			y := 1 + int((h>>16)%uint32(oy-1))
			c.Set(x, y, arcade.FarStar)
		}
	}

	// Distant hills.
	for x := range w {
		for y := groundY - 3 + arcade.FarHill(x+tick/10); y <= groundY; y++ {
			c.Set(x, oy+y, arcade.HillFar)
		}
		for y := groundY - 1 + arcade.NearHill(x+tick/6); y <= groundY; y++ {
			c.Set(x, oy+y, arcade.HillNear)
		}
	}

	// Turf + shallow shadow. Keep the black floor thin so the grass
	// reads as terrain instead of a line above a huge border.
	arcade.DrawGround(c, oy+groundY)
}

func drawPig(c *pixel.Canvas, g *Game, oy int) {
	rows := g.PigRunA
	if g.Pig.Ducked > 0 {
		rows = g.PigDuck
	} else if (g.Tick/3)%6 >= 3 {
		rows = g.PigRunB
	}
	y := int(g.Pig.Y) - len(rows) + 1
	c.Blit(pigX, oy+y, rows, g.pigPal)
}

func drawObstacles(c *pixel.Canvas, g *Game, oy int) {
	for _, ob := range g.Obstacles {
		rows := obstacleArt(ob)
		top := groundY - len(rows) + 1
		if ob.Kind == ObAir {
			top, _ = obBoundsY(ob)
		}
		for i := range max(ob.Size, 1) {
			c.Blit(int(ob.X)+i*obWidth, oy+top, rows, hazardPal)
		}
	}
}

func renderHUDLines(g *Game, trueColor bool, width int) []string {
	hud := arcade.HUD{Title: "PiG RUNNER", Score: g.Score, High: g.HighScore, Hints: "↑jump ↓duck p pause q quit"}
	switch {
	case g.Waiting:
		hud.Status = " " + arcade.Dim("space start  q quit")
	case g.Over:
		hud.State = arcade.Over
		hud.Status = " " + arcade.Alert("💥 CRASHED") + "  " + arcade.Dim("r retry  q quit")
	case g.Paused:
		hud.State = arcade.Paused
		hud.Status = " " + arcade.Notice("⏸ PAUSED") + "  " + arcade.Dim("p resume")
	default:
		hud.Status = " " + arcade.Dim("jump pies, duck flying pies")
	}
	return hud.Lines(width, trueColor)
}

func obstacleArt(ob Obstacle) []string {
	if ob.Kind == ObAir {
		return flyingPie
	}
	if ob.Label == "pie-crust" {
		return groundPieB
	}
	return groundPieA
}
