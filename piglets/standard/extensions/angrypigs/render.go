package angrypigs

import (
	"image/color"
	"math"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/piglets/standard/internal/arcade"
	"github.com/MichaelKinsy/PiG/piglets/standard/internal/pixel"
)

// scene holds the reusable buffers that draw one Angry Pigs view.
type scene struct {
	canvas  pixel.Canvas
	encoder pixel.Encoder
	pigPal  *pixel.Palette
	preview [24]point
	hud     []string
	hudKey  hudKey
	digits  [20]byte
	// waiting shows the title screen instead of play.
	waiting bool

	// Per-frame view transform. camY lifts the view to keep a high pig in
	// sight; groundY is the ground surface row before that lift.
	camX, camY, groundY, shakeX int
}

type hudKey struct {
	event                           string
	angle, power, width             int
	score, high, level, pigs, birds int
	levelDone, gameOver, trueColor  bool
	waiting                         bool
}

func newScene(pigPal *pixel.Palette) *scene {
	return &scene{pigPal: pigPal}
}

// render appends a view w cells wide and h lines tall to dst: the pixel
// playfield, then the two HUD lines shared with PiG Runner when the view has
// room for them. Every line is exactly w cells.
func (s *scene) render(g *game, w, h int, dst []string) []string {
	w, h = max(w, 1), max(h, 1)
	hud := h >= 1+arcade.HUDLines
	pxH := h * 2
	if hud {
		pxH -= arcade.HUDLines * 2
	}
	c := &s.canvas
	c.Resize(w, pxH)
	// Like PiG Runner, the turf is the road line over a two-pixel floor.
	s.groundY = pxH - 3
	s.shakeX = int(math.Round(math.Sin(g.time*70) * g.shake * 2))
	s.camX = int(math.Round(g.camX))
	s.camY = 0
	if g.flying {
		s.camY = max(int(math.Round(g.pig.y))+18-s.groundY, 0)
	}

	s.drawSky()
	arcade.DrawGround(c, s.ground())
	s.drawSlingBack(g)
	s.drawStructures(g)
	s.drawTrail(g)
	s.drawPigs(g)
	s.drawSlingFront(g)
	s.drawParticles(g)
	s.drawPopups(g)
	s.drawMinimap(g)
	s.drawCard(g)

	s.encoder.TrueColor = pixel.SupportsTrueColor()
	dst = s.encoder.Encode(c, dst)
	if !hud {
		return dst
	}
	key := hudKey{
		event: g.lastEvent, angle: g.angle, power: g.power, width: w,
		score: g.score, high: max(g.highScore, g.score), level: g.level, pigs: g.pigsLeft, birds: g.birdsLeft(),
		levelDone: g.levelDone, gameOver: g.gameOver, trueColor: s.encoder.TrueColor, waiting: s.waiting,
	}
	if s.hud == nil || key != s.hudKey {
		s.hud, s.hudKey = s.hudLines(g, key), key
	}
	return append(dst, s.hud...)
}

// hudLines is the PiG Runner HUD: title, score, high score, and controls,
// then the level, pigs, birds, aim, and the latest event.
func (s *scene) hudLines(g *game, key hudKey) []string {
	hud := arcade.HUD{
		Title: "ANGRY PIGS", Score: g.score, High: key.high,
		Hints: "↑↓ pull ←→ aim space fire n next r restart q quit",
	}
	pigs := strings.Repeat("●", g.pigsLeft) + strings.Repeat("○", levelPigs[g.level]-g.pigsLeft)
	info := "level " + strconv.Itoa(g.level+1) + "/" + strconv.Itoa(len(levels)) + "  pigs " + pigs + "  birds " + strconv.Itoa(key.birds) +
		"  pull " + strconv.Itoa(g.power) + "%  aim " + strconv.Itoa(g.angle) + "°"
	switch {
	case s.waiting:
		hud.Status = " " + arcade.Dim("space start  q quit")
	case g.gameOver && g.birdsLeft() == 0:
		hud.Status = " " + arcade.Notice("🏆 YOU WIN") + "  " + arcade.Dim("r play again  q quit")
		hud.State = arcade.Over
	case g.gameOver:
		hud.Status = " " + arcade.Alert("💥 OUT OF PIGS") + "  " + arcade.Dim("r retry  q quit")
		hud.State = arcade.Over
	case g.levelDone:
		hud.Status = " " + arcade.Notice("★ LEVEL CLEAR") + "  " + arcade.Dim("n next level  q quit")
	default:
		hud.Status = " " + arcade.Dim(info+"  ·  "+g.lastEvent)
	}
	return hud.Lines(key.width, key.trueColor)
}

// drawCard draws the shared title card before play and result cards after
// a level or game ends.
func (s *scene) drawCard(g *game) {
	switch {
	case s.waiting:
		arcade.DrawCard(&s.canvas, "ANGRY PIGS", "PRESS SPACE", 1)
	case g.banner == "YOU WIN!":
		arcade.DrawCard(&s.canvas, g.banner, "R PLAY AGAIN", 1)
	case g.banner == "GAME OVER":
		arcade.DrawCard(&s.canvas, g.banner, "R RETRY", 1)
	case g.banner != "":
		arcade.DrawCard(&s.canvas, g.banner, "N NEXT LEVEL", 1)
	}
}

// screen converts world coordinates to canvas pixels.
func (s *scene) screen(x, y float64) (int, int) {
	return int(math.Round(x)) - s.camX + s.shakeX, s.groundY + s.camY - int(math.Round(y))
}

// world converts a canvas pixel back to world coordinates.
func (s *scene) world(px, py int) (float64, float64) {
	return float64(px + s.camX - s.shakeX), float64(s.groundY + s.camY - py)
}

// ground is the ground surface row after the vertical lift.
func (s *scene) ground() int { return s.groundY + s.camY }

// drawSky draws PiG Runner's night sky over the whole view: its gradient
// down to the turf, two star layers, and its two hill lines, all scrolling
// slower than the world for parallax.
func (s *scene) drawSky() {
	c := &s.canvas
	gy := s.ground()
	for y := range c.H {
		c.Rect(0, y, c.W, 1, arcade.SkyColor(y, max(gy, 1)))
	}
	for i := range max(c.W*gy/220, 4) {
		h := uint32(i+1) * 2654435761
		layer, col, parallax := 7, arcade.Star, 0.15
		if i%3 != 0 {
			layer, col, parallax = 11, arcade.FarStar, 0.05
		}
		span := c.W + worldW
		x := (int(h%uint32(span)) - int(float64(s.camX)*parallax)) % span
		if x < 0 {
			x += span
		}
		y := 1 + int((h>>layer)%uint32(max(gy-6, 1)))
		c.Set(x, y, col)
	}
	for x := range c.W {
		far := arcade.FarHill(x + int(float64(s.camX)*0.3))
		c.Rect(x, gy-3+far, 1, 4-far, arcade.HillFar)
		near := arcade.NearHill(x + int(float64(s.camX)*0.6))
		c.Rect(x, gy-1+near, 1, 2-near, arcade.HillNear)
	}
}

func (s *scene) slingTop() (int, int) {
	return s.screen(slingX-4, slingY+2)
}

func (s *scene) loaded(g *game) bool {
	return !g.flying && !g.levelDone && !g.gameOver && g.pigsLeft > 0
}

func (s *scene) drawSlingBack(g *game) {
	x, y := s.slingTop()
	s.canvas.Blit(x, y, slingshot, slingPalette)
	if s.loaded(g) {
		px, py := s.screen(g.pouch().x-3, g.pouch().y)
		s.band(x+8, y, px, py, g.power)
	}
	s.drawPowerMeter(g)
}

func (s *scene) drawSlingFront(g *game) {
	x, y := s.slingTop()
	if s.loaded(g) {
		px, py := s.screen(g.pouch().x-3, g.pouch().y)
		s.band(x, y, px, py, g.power)
	} else {
		s.line(x, y, x+8, y, bandColor)
	}
}

// band draws one side of the rubber band; it thickens as it stretches.
func (s *scene) band(x0, y0, x1, y1, power int) {
	s.line(x0, y0, x1, y1, bandColor)
	if power >= 55 {
		s.line(x0, y0+1, x1, y1+1, bandColor)
	}
}

// powerMeterHeight is the pixel height of the slingshot's power gauge.
const powerMeterHeight = 16

// drawPowerMeter draws a vertical gauge right of the slingshot that fills
// from green through gold to red with the pull.
func (s *scene) drawPowerMeter(g *game) {
	if !s.loaded(g) {
		return
	}
	c := &s.canvas
	x, top := s.screen(slingX+8, slingY+powerMeterHeight/2+2)
	c.Rect(x-1, top-1, 5, powerMeterHeight+2, meterFrame)
	c.Rect(x, top, 3, powerMeterHeight, meterEmpty)
	filled := powerMeterHeight * g.power / 100
	for i := range filled {
		t := float64(i) / float64(powerMeterHeight-1)
		col := pixel.Lerp(meterLow, meterMid, min(t*2, 1))
		if t > 0.5 {
			col = pixel.Lerp(meterMid, meterHigh, (t-0.5)*2)
		}
		c.Rect(x, top+powerMeterHeight-1-i, 3, 1, col)
	}
}

// line draws a one-pixel line.
func (s *scene) line(x0, y0, x1, y1 int, col color.RGBA) {
	steps := max(abs(x1-x0), abs(y1-y0), 1)
	for i := 0; i <= steps; i++ {
		t := float64(i) / float64(steps)
		s.canvas.Set(x0+int(math.Round(float64(x1-x0)*t)), y0+int(math.Round(float64(y1-y0)*t)), col)
	}
}

func abs(v int) int { return max(v, -v) }

func (s *scene) drawStructures(g *game) {
	blink := int(g.time*10)%37 == 0
	for cy := range gridRows {
		for cx := range gridCols {
			cl := g.grid[cy][cx]
			if cl.kind == cellEmpty {
				continue
			}
			x, y := s.screen(float64(cx*cellSize), float64((cy+1)*cellSize)+cl.drop)
			switch cl.kind {
			case cellWood:
				s.drawBlock(x, y, woodTexture, woodPalette, cl, 255)
			case cellStone:
				s.drawBlock(x, y, stoneTexture, stonePalette, cl, 255)
			case cellIce:
				s.drawBlock(x, y, iceTexture, icePalette, cl, 215)
			case cellBird:
				frame := 0
				if blink || (int(g.time*10)+cx*7)%53 == 0 {
					frame = 1
				}
				s.canvas.Blit(x, y, birdFrames[frame], birdPalette)
			}
		}
	}
}

func (s *scene) drawBlock(x, y int, texture []string, pal *pixel.Palette, cl cell, alpha uint8) {
	c := &s.canvas
	for yy, row := range texture {
		for xx := range len(row) {
			col := pal[row[xx]]
			if alpha == 255 {
				c.Set(x+xx, y+yy, col)
			} else {
				c.Blend(x+xx, y+yy, col, alpha)
			}
		}
	}
	if damage := maxHits[cl.kind] - cl.hits; damage > 0 {
		crack := pixel.Scale(pal['d'], 0.7)
		for yy, row := range cracks[min(damage, len(cracks)-1)] {
			for xx := range len(row) {
				if row[xx] == 'x' {
					c.Set(x+xx, y+yy, crack)
				}
			}
		}
	}
}

func (s *scene) drawTrail(g *game) {
	for i := range g.trailLen {
		if i%2 == 1 {
			continue
		}
		x, y := s.screen(g.trail[i].x, g.trail[i].y)
		s.canvas.Blend(x, y, trailColor, 200)
	}
	points := g.preview(s.preview[:0])
	for i, p := range points {
		x, y := s.screen(p.x, p.y)
		alpha := uint8(230 - 170*i/max(len(points), 1))
		s.canvas.Blend(x, y, previewDot, alpha)
		if i < 3 {
			s.canvas.Blend(x+1, y, previewDot, alpha/2)
		}
	}
}

func (s *scene) drawPigs(g *game) {
	c := &s.canvas
	waiting := g.pigsLeft
	if s.loaded(g) {
		waiting--
		p := g.pouch()
		x, y := s.screen(p.x-4, p.y+4)
		c.Blit(x, y, pigBall, s.pigPal)
	}
	for i := range waiting {
		x, y := s.screen(slingX-20-float64(i)*8, 8)
		bob := 0
		if int(g.time*2+float64(i))%2 == 0 {
			bob = 1
		}
		c.Blit(x, y+bob, pigBall, s.pigPal)
	}
	if !g.flying {
		return
	}
	x, y := s.screen(g.pig.x-4, g.pig.y+4)
	if math.Mod(math.Abs(g.spin), 2) < 1 {
		c.Blit(x, y, pigBall, s.pigPal)
	} else {
		c.BlitMirrored(x, y, pigBall, s.pigPal)
	}
	if y+8 < 0 {
		// The pig is above the view: mark its column at the top edge.
		for i := range 3 {
			c.Rect(x+4-i, 1+i, 1+2*i, 1, s.pigPal['P'])
		}
	}
}

func (s *scene) drawParticles(g *game) {
	for i := range g.particles.used {
		p := &g.particles.pool[i]
		if p.age >= p.life {
			continue
		}
		x, y := s.screen(p.x, p.y)
		fade := uint8(255 * (1 - p.age/p.life))
		switch p.kind {
		case dustKind:
			s.canvas.Blend(x, y, p.col, fade)
			if p.age < p.life*0.5 {
				s.canvas.Blend(x+1, y, p.col, fade/2)
				s.canvas.Blend(x, y-1, p.col, fade/2)
			}
		case debrisKind:
			s.canvas.Blend(x, y, p.col, max(fade, 90))
			s.canvas.Blend(x+1, y, pixel.Scale(p.col, 0.75), max(fade, 90))
		case featherKind:
			s.canvas.Blend(x, y, p.col, fade)
		}
	}
}

func (s *scene) drawPopups(g *game) {
	for i := range g.popups {
		p := &g.popups[i]
		if p.value == 0 || p.age >= popupLife {
			continue
		}
		x, y := s.screen(p.x, p.y)
		s.drawNumber(x-6, y-3, '+', p.value, hudGold, hudShadow)
	}
}

// drawNumber draws an optional sign followed by n with the pixel font,
// without allocating, and returns the drawn width.
func (s *scene) drawNumber(x, y int, sign byte, n int, ink, shadow color.RGBA) int {
	b := s.digits[:0]
	if sign != 0 {
		b = append(b, sign)
	}
	b = strconv.AppendInt(b, int64(n), 10)
	for i, ch := range b {
		s.canvas.DrawGlyph(x+i*pixel.GlyphAdvance, y, rune(ch), ink, shadow)
	}
	return len(b)*pixel.GlyphAdvance - 1
}

// minimapLayout places the whole-world map under the HUD's left side when the
// view is narrower than the world and there is room: 4x smaller, else 6x.
func (s *scene) minimapLayout() (x, y, scale int, ok bool) {
	c := &s.canvas
	if c.W >= worldW || c.H < 12 {
		return 0, 0, 0, false
	}
	for _, scale := range [...]int{4, 6} {
		mw, mh := worldW/scale, gridRows*cellSize/scale+2
		if c.W >= mw+8 && s.groundY >= minimapTop+mh+10 {
			return 2, minimapTop, scale, true
		}
	}
	return 0, 0, 0, false
}

const minimapTop = 2

// drawMinimap shows the whole world when the view is narrower than it: the
// ground, the structures, the slingshot, the pig, and the visible window.
func (s *scene) drawMinimap(g *game) {
	mx, my, scale, ok := s.minimapLayout()
	if !ok || g.banner != "" || s.waiting {
		return
	}
	c := &s.canvas
	mw, mh := worldW/scale, gridRows*cellSize/scale+2
	for y := range mh {
		for x := range mw {
			c.Blend(mx+x, my+y, arcade.RoadDark, 170)
		}
	}
	base := my + mh - 1
	c.Rect(mx, base, mw, 1, arcade.Accent)
	dot := (cellSize + scale - 1) / scale
	for cy := range gridRows {
		for cx := range gridCols {
			kind := g.grid[cy][cx].kind
			if kind == cellEmpty {
				continue
			}
			col := materialColor(kind)
			if kind == cellBird {
				col = birdPalette['Y']
			}
			c.Rect(mx+cx*cellSize/scale, base-(cy+1)*cellSize/scale, dot, dot, col)
		}
	}
	c.Rect(mx+int(slingX)/scale, base-int(slingY)/scale, 1, int(slingY)/scale, slingPalette['T'])
	if g.flying {
		c.Rect(mx+int(g.pig.x)/scale, base-int(g.pig.y)/scale-1, 2, 2, s.pigPal['P'])
	}
	left := mx + max(int(g.camX), 0)/scale
	right := min(left+c.W/scale, mx+mw-1)
	for x := left; x <= right; x++ {
		c.Set(x, my, hudInk)
	}
	c.Rect(left, my, 1, 3, hudInk)
	c.Rect(right, my, 1, 3, hudInk)
}
