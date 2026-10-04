package codingagent

// Ports packages/coding-agent/src/modes/interactive/components/easter-egg-3d.ts (the Pi-logo kind, as Pi 1.0.0 pi-logo-animation.ts drew it; D87).
//
// pig divergence (D87): the object of the animation is the pig of PiG's header mark (D2) in the active sprite's colors, not
// Pi's logo, and where Pi's logo plays its sliding puzzle a side-view pig runs across the screen (pig_logo_run.go). The engine, the timeline, the
// dust, the ray caster, the starfield, the hint, the reverse exit and the key and mouse handling are Pi's.

import (
	"context"
	"math"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/coding/piglogin"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

type logoRgb = [3]float64

type logoScreenCell struct {
	text string
	// width is 1 or 2 for a grapheme, 0 for the second column of a wide grapheme.
	width int
	fg    logoRgb
	// hasBg is false when the cell uses the terminal's default background.
	bg    logoRgb
	hasBg bool
	// delay is the seconds until the cell turns into braille dust.
	delay float64
	// ink is the number of braille dots the glyph turns into.
	ink  int
	seed int
}

type logoStar struct {
	// glyph is a braille character with the single dot the star occupies.
	glyph string
	// tint is the color the star leans toward; it only ever shows a small step from the background toward it.
	tint logoRgb
	// strength is the largest step from the background toward the tint, from 0 to 1.
	strength float64
	phase    float64
	// speed is the flicker speed in radians per second.
	speed float64
}

type logoBox struct {
	min, max [3]float64
	color    logoRgb
}

type logoPose struct {
	centerX, centerY float64
	// scale is braille dots per logo pixel at depth 0.
	scale            float64
	yaw, pitch, roll float64
}

// logoFrameRate is the animation's frames per second.
// upstream: packages/coding-agent/src/modes/interactive/components/easter-egg-3d.ts:FRAME_MS
const logoFrameRate = 30

const (
	logoDepth          = 0.7
	logoCameraDistance = 10
	logoFlyStart       = 0.1
	logoFlyDuration    = 1.3
)

// logoCell3 is the grid position of a block: column and row in the sprite, and layer in depth.
type logoCell3 = [3]float64

// logoBlock is one pixel of the pig head, a block of the 3D object. home is in head pixels.
type logoBlock struct {
	home  logoCell3
	color logoRgb
}

// Pi's puzzle timeline. PiG's pig runs in the puzzle's place (D87), on the same cycle, so the spin stays locked to it.
const (
	logoPuzzleStep   = 0.3
	logoPuzzleSteps  = 12
	logoPuzzleReturn = 1
	logoPuzzleHold   = 0.6
)

// jsNumber keeps a derived timing value out of Go's exact constant arithmetic, so it rounds after every operation as Pi's
// module constants do.
func jsNumber(value float64) float64 { return value }

var (
	logoPuzzleStart = jsNumber(logoFlyStart) + logoFlyDuration + 3
	logoPuzzleCycle = jsNumber(logoPuzzleStep)*logoPuzzleSteps + logoPuzzleReturn + logoPuzzleHold
	// logoShuffleEnd is the end of Pi's shuffle within a cycle (PUZZLE_STEP * PUZZLE_STEPS).
	logoShuffleEnd = jsNumber(logoPuzzleStep) * logoPuzzleSteps
)

// pigLogoBlocks are the blocks of the variant's pig head, one per opaque pixel, in the head's colors.
func pigLogoBlocks(variant piglogin.Variant) []logoBlock {
	pixels := piglogin.HeadPixels(variant)
	blocks := make([]logoBlock, len(pixels))
	for i, pixel := range pixels {
		blocks[i] = logoBlock{
			home:  logoCell3{float64(pixel.X), float64(pixel.Y), 0},
			color: logoRgb{float64(pixel.Color.R), float64(pixel.Color.G), float64(pixel.Color.B)},
		}
	}
	return blocks
}

// pigLogoPixel is the size of a head pixel in the animation's grid units. Pi's logo is 4 pixels on a side and its
// camera sits CAMERA_DISTANCE (10) units away; the head is 16 by 14 pixels, so at a unit per pixel it would reach past the
// camera. Half a unit per pixel makes the head 8 by 7 units, and a head pixel is still a half block (2x2 braille dots) at
// the start.
const pigLogoPixel = 0.5

// The header pig's center in grid units: Pi centers its 4x4 logo at (2, 2).
const (
	pigLogoCenterX = piglogin.HeadWidth * pigLogoPixel / 2
	pigLogoCenterY = piglogin.HeadHeight * pigLogoPixel / 2
)

// pigLogoRadius is the farthest distance of any block corner from the origin, with blocks on the outer depth layers. Pi's
// logo has Math.hypot(2, 2, 1 + DEPTH / 2).
var pigLogoRadius = jsHypot(pigLogoCenterX, pigLogoCenterY, 1+logoDepth/2)

const (
	// logoFrameInterval is Pi's FRAME_MS (1000 / 30); Node truncates setInterval's fractional delay.
	logoFrameInterval    = time.Duration(1000/logoFrameRate) * time.Millisecond
	logoDustDuration     = 0.35
	logoWaveSpread       = 0.55
	logoWaveJitter       = 0.12
	logoBackgroundFade   = 0.5
	logoExitDuration     = 1.1
	logoStarsStart       = 2.5
	logoStarsFade        = 2
	logoStarDensity      = 0.018
	logoColorStep        = 5
	logoLightHalo        = 0.3
	logoHaloRadiusX      = 6
	logoHaloRadiusY      = 3
	logoHaloLevels       = 32
	logoStarAlphaLevels  = 8
	logoStarFlickerLevel = 3
	logoHintFade         = 0.5
)

// logoStarTints are neutral, pale blue, pale gold, and pale rose, each mixed half with the terminal's foreground.
var logoStarTints = [...]logoRgb{
	{255, 255, 255},
	{170, 195, 255},
	{255, 225, 170},
	{255, 190, 205},
}

// logoDotBits are the braille dot bits, indexed by row * 2 + column within the 2x4 cell.
var logoDotBits = [8]uint8{0x01, 0x08, 0x02, 0x10, 0x04, 0x20, 0x40, 0x80}

var logoBraille = func() (glyphs [256]string) {
	for bits := range glyphs {
		glyphs[bits] = string(rune(0x2800 + bits))
	}
	return glyphs
}()

var (
	logoDissolveEnd = jsNumber(logoFlyStart) + logoWaveSpread + logoWaveJitter + logoDustDuration
	logoLight       = logoNormalize([3]float64{-0.45, -0.6, 0.75})
	logoHalfVector  = logoNormalize([3]float64{logoLight[0], logoLight[1], logoLight[2] + 1})
	// logoStartScale makes the front face cover exactly the header pig's dots at the start despite the perspective.
	logoStartScale  = (jsNumber(2/pigLogoPixel) * (jsNumber(logoCameraDistance) - logoDepth/2)) / logoCameraDistance
	logoReachFactor = logoCameraDistance / (logoCameraDistance - pigLogoRadius)
)

func logoNormalize(v [3]float64) [3]float64 {
	length := jsHypot(v[0], v[1], v[2])
	return [3]float64{v[0] / length, v[1] / length, v[2] / length}
}

// jsHypot is Math.hypot: the largest magnitude scales a compensated sum of squares.
func jsHypot(values ...float64) float64 {
	largest := 0.0
	for _, value := range values {
		if math.IsInf(value, 0) {
			return math.Inf(1)
		}
		largest = max(largest, math.Abs(value))
	}
	if largest == 0 {
		return 0
	}
	sum, compensation := 0.0, 0.0
	for _, value := range values {
		scaled := value / largest
		summand := scaled*scaled - compensation
		preliminary := sum + summand
		compensation = (preliminary - sum) - summand
		sum = preliminary
	}
	return math.Sqrt(sum) * largest
}

func clamp01(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

// logoSmooth is the quintic smoothstep.
func logoSmooth(value float64) float64 {
	t := clamp01(value)
	return t * t * t * (t*(t*6-15) + 10)
}

func logoEaseOut(value float64) float64 {
	t := 1 - clamp01(value)
	return 1 - t*t*t
}

func logoMix(a, b logoRgb, amount float64) logoRgb {
	return logoRgb{a[0] + (b[0]-a[0])*amount, a[1] + (b[1]-a[1])*amount, a[2] + (b[2]-a[2])*amount}
}

func jsRoundFloat(value float64) float64 { return math.Floor(value + 0.5) }

// jsToUint32 is ECMAScript ToUint32, the operand conversion of Math.imul and the bitwise operators.
func jsToUint32(value float64) uint32 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	return uint32(int64(math.Mod(math.Trunc(value), 4294967296)))
}

func logoHash(value float64) float64 {
	x := jsToUint32(value) ^ 0x9e3779b9
	x *= 0x85ebca6b
	x ^= x >> 13
	x *= 0xc2b2ae35
	x ^= x >> 16
	return float64(x) / 0x100000000
}

// logoIsLight reports whether a background is light, by relative luminance.
func logoIsLight(c logoRgb) bool {
	return 0.2126*c[0]+0.7152*c[1]+0.0722*c[2] > 128
}

func logoToRgb(color tui.Color) logoRgb {
	rgb := tui.ColorToRgb(color)
	return logoRgb{rgb.R, rgb.G, rgb.B}
}

// logoGlyphInk is the braille dots a glyph turns into, a rough measure of how much ink it has.
func logoGlyphInk(text string, seed float64) int {
	if widthx.JSTrim(text) == "" {
		return 0
	}
	switch text {
	case ".", ",", ":", ";", "'", "`", "-", "_", "·":
		return 2
	}
	if runes := []rune(text); len(runes) == 1 && runes[0] >= 0x2500 && runes[0] <= 0x257f {
		return 3
	}
	return 4 + int(math.Floor(seed*3))
}

// jsParseInt is Number.parseInt for the decimal SGR parameters: leading whitespace, an optional sign, then digits; NaN
// when there are none.
func jsParseInt(text string) float64 {
	text = widthx.JSTrim(text)
	sign := 1.0
	if strings.HasPrefix(text, "-") {
		sign, text = -1, text[1:]
	} else {
		text = strings.TrimPrefix(text, "+")
	}
	end := 0
	for end < len(text) && text[end] >= '0' && text[end] <= '9' {
		end++
	}
	if end == 0 {
		return math.NaN()
	}
	value := 0.0
	for _, digit := range text[:end] {
		value = value*10 + float64(digit-'0')
	}
	return sign * value
}

// parseLogoScreen parses rendered lines into cells with resolved colors. Other escape sequences (OSC, APC, cursor) are
// skipped.
func parseLogoScreen(lines []string, width int, foreground, background logoRgb) [][]logoScreenCell {
	palette := func(index int) logoRgb { return logoToRgb(tui.IndexedColor{Index: index}) }
	screen := make([][]logoScreenCell, len(lines))
	for row, line := range lines {
		var cells []logoScreenCell
		var fg, bg logoRgb
		var hasFg, hasBg, dim, inverse bool
		applySgr := func(params string) {
			codes := []string{"0"}
			if params != "" {
				codes = strings.Split(params, ";")
			}
			for i := 0; i < len(codes); i++ {
				var parts []float64
				for part := range strings.SplitSeq(codes[i], ":") {
					parts = append(parts, jsParseInt(part))
				}
				code := parts[0]
				switch {
				case code == 38 || code == 48:
					// Extended color, either as `38;5;n` / `38;2;r;g;b` or colon sub-parameters.
					var args []float64
					if len(parts) > 1 {
						// `38:2::r:g:b` has an empty color space id, dropped here.
						for _, part := range parts[1:] {
							if !math.IsNaN(part) {
								args = append(args, part)
							}
						}
					} else {
						kind := math.NaN()
						if i+1 < len(codes) {
							kind = jsParseInt(codes[i+1])
						}
						count := 1
						switch kind {
						case 5:
							count = 2
						case 2:
							count = 4
						}
						for _, part := range codes[min(i+1, len(codes)):min(i+1+count, len(codes))] {
							args = append(args, jsParseInt(part))
						}
						i += count
					}
					var color logoRgb
					ok := false
					if len(args) >= 2 && args[0] == 5 {
						// Pi's palette lookup takes any index; indexedColor rejects one outside 0-255.
						if index := args[1]; index >= 0 && index <= 255 && index == math.Trunc(index) {
							color, ok = palette(int(index)), true
						} else {
							panic("ANSI color index must be an integer from 0 to 255")
						}
					} else if len(args) >= 4 && args[0] == 2 {
						color, ok = logoRgb{args[1], args[2], args[3]}, true
					}
					if ok {
						if code == 38 {
							fg, hasFg = color, true
						} else {
							bg, hasBg = color, true
						}
					}
				case code == 0:
					hasFg, hasBg, dim, inverse = false, false, false, false
				case code == 2:
					dim = true
				case code == 22:
					dim = false
				case code == 7:
					inverse = true
				case code == 27:
					inverse = false
				case code >= 30 && code <= 37:
					fg, hasFg = palette(int(code)-30), true
				case code >= 90 && code <= 97:
					fg, hasFg = palette(int(code)-90+8), true
				case code == 39:
					hasFg = false
				case code >= 40 && code <= 47:
					bg, hasBg = palette(int(code)-40), true
				case code >= 100 && code <= 107:
					bg, hasBg = palette(int(code)-100+8), true
				case code == 49:
					hasBg = false
				}
			}
		}
		pushText := func(text string) {
			for text != "" {
				var segment string
				segment, text = widthx.FirstGrapheme(text)
				glyphWidth := widthx.VisibleWidth(segment)
				if glyphWidth == 0 || len(cells)+glyphWidth > width {
					continue
				}
				cellFg := foreground
				if hasFg {
					cellFg = fg
				}
				cellBg, cellHasBg := bg, hasBg
				if inverse {
					cellFg = background
					if hasBg {
						cellFg = bg
					}
					cellBg, cellHasBg = foreground, true
					if hasFg {
						cellBg = fg
					}
				}
				if dim {
					base := background
					if cellHasBg {
						base = cellBg
					}
					cellFg = logoMix(cellFg, base, 0.4)
				}
				cell := logoScreenCell{text: segment, width: glyphWidth, fg: cellFg, bg: cellBg, hasBg: cellHasBg}
				cells = append(cells, cell)
				if glyphWidth == 2 {
					cell.text, cell.width = "", 0
					cells = append(cells, cell)
				}
			}
		}
		i := 0
		for i < len(line) {
			sequenceStart := strings.IndexByte(line[i:], 0x1b)
			if sequenceStart == -1 {
				pushText(line[i:])
				break
			}
			sequenceStart += i
			if sequenceStart > i {
				pushText(line[i:sequenceStart])
			}
			var kind byte
			if sequenceStart+1 < len(line) {
				kind = line[sequenceStart+1]
			}
			switch kind {
			case '[':
				end := sequenceStart + 2
				for end < len(line) && (line[end] < 0x40 || line[end] > 0x7e) {
					end++
				}
				if end < len(line) && line[end] == 'm' {
					applySgr(line[sequenceStart+2 : end])
				}
				i = end + 1
			case ']', '_', 'P', '^':
				// String sequences end at BEL or ST (ESC \).
				bell := strings.IndexByte(line[sequenceStart+2:], 0x07)
				st := strings.Index(line[sequenceStart+2:], "\x1b\\")
				if bell == -1 && st == -1 {
					i = len(line)
					break
				}
				if bell != -1 && (st == -1 || bell < st) {
					i = sequenceStart + 2 + bell + 1
				} else {
					i = sequenceStart + 2 + st + 2
				}
			default:
				i = sequenceStart + 2
			}
		}
		screen[row] = cells
	}
	return screen
}

// logoRotation is the row-major 3x3 rotation matrix for yaw (y), then pitch (x), then roll (z): Rz * Rx * Ry.
func logoRotation(yaw, pitch, roll float64) [9]float64 {
	sy, cy, sx, cx, sz, cz := math.Sin(yaw), math.Cos(yaw), math.Sin(pitch), math.Cos(pitch), math.Sin(roll), math.Cos(roll)
	ry := [9]float64{cy, 0, sy, 0, 1, 0, -sy, 0, cy}
	rx := [9]float64{1, 0, 0, 0, cx, -sx, 0, sx, cx}
	rz := [9]float64{cz, -sz, 0, sz, cz, 0, 0, 0, 1}
	return logoMultiply(rz, logoMultiply(rx, ry))
}

func logoMultiply(a, b [9]float64) [9]float64 {
	var result [9]float64
	for row := range 3 {
		for column := range 3 {
			result[row*3+column] = a[row*3]*b[column] + a[row*3+1]*b[3+column] + a[row*3+2]*b[6+column]
		}
	}
	return result
}

// The spin is locked to the cycle: one turn per cycle, facing the camera in the middle of each hold.
const (
	logoSpinRamp   = 1.4
	logoSpinLinger = 0.6
)

var (
	logoSpinSpeed      = (math.Pi * 2) / logoPuzzleCycle
	logoFirstFrontView = logoPuzzleStart + jsNumber(logoPuzzleStep)*logoPuzzleSteps + logoPuzzleReturn + logoPuzzleHold/2
)

// logoBaseSpinPhase is a uniform spin that accelerates from rest over logoSpinRamp, then turns at logoSpinSpeed.
func logoBaseSpinPhase(time float64) float64 {
	elapsed := time - logoFlyStart
	if elapsed <= 0 {
		return 0
	}
	u := elapsed / logoSpinRamp
	// Integral of the quintic smoothstep ramp.
	if u < 1 {
		return logoSpinSpeed * logoSpinRamp * (math.Pow(u, 6) - 3*math.Pow(u, 5) + 2.5*math.Pow(u, 4))
	}
	return logoSpinSpeed * (logoSpinRamp*0.5 + elapsed - logoSpinRamp)
}

// logoSpinAlignment is extra rotation added during the flight, so the phase is a whole number of turns at every front view.
var logoSpinAlignment = math.Pi*2 - math.Mod(logoBaseSpinPhase(logoFirstFrontView), math.Pi*2)

// logoSpinPhase is whole turns exactly when the pig faces the camera at home.
func logoSpinPhase(time float64) float64 {
	return logoBaseSpinPhase(time) + logoSpinAlignment*logoSmooth((time-logoFlyStart)/logoFlyDuration)
}

// logoSpinAngle is the yaw. It matches the phase at whole turns but lingers there, so the pig reads from the front.
func logoSpinAngle(time float64) float64 {
	phase := logoSpinPhase(time)
	return phase - logoSpinLinger*math.Sin(phase)
}

type logoFace struct {
	axis int
	// plane is the plane coordinate on axis in object space.
	plane                  float64
	uMin, uMax, vMin, vMax float64
	// Projected bounds in braille dots, inclusive.
	minX, minY, maxX, maxY int
	red, green, blue       float64
}

type logoBounds struct{ minX, minY, maxX, maxY int }

// logoRaster renders the blocks into braille cells. Only faces that point at the camera and are not covered by a touching
// block are drawn. Each face is rasterized over its projected bounds by intersecting each dot's ray with the face's plane,
// with a depth buffer resolving overlaps. Buffers are reused between frames; the float buffers are single precision, as
// Pi's Float32Arrays are.
type logoRaster struct {
	bits       []uint8
	counts     []uint8
	rgb        []float32
	haloAmount []float32
	haloRgb    []float32
	width      int
	height     int
	depth      []float32
	// faceIDs holds the hit face per dot, plus one. pig divergence (D87): Pi's Uint8Array holds its logo's faces; the
	// running pig has more than 255.
	faceIDs   []uint16
	dirty     *logoBounds
	haloDirty *logoBounds
	faces     []logoFace
}

func (r *logoRaster) render(width, height int, pose logoPose, boxes []logoBox, background logoRgb, haloStrength float64) {
	r.renderLayer(width, height, pose, boxes, background, haloStrength, nil, false)
}

// renderLayer renders boxes keeping only the dots keep accepts (all when keep is nil), by dot index. With accumulate it
// adds to the previous layer's dots instead of replacing them.
//
// pig divergence (D87): Pi renders one object; the head and the running pig dissolve into each other dot by dot as two
// layers.
func (r *logoRaster) renderLayer(width, height int, pose logoPose, boxes []logoBox, background logoRgb, haloStrength float64, keep func(int) bool, accumulate bool) {
	dotWidth := width * 2
	dotHeight := height * 4
	if width != r.width || height != r.height {
		r.width, r.height = width, height
		r.bits = make([]uint8, width*height)
		r.counts = make([]uint8, width*height)
		r.rgb = make([]float32, width*height*3)
		r.haloAmount = make([]float32, width*height)
		r.haloRgb = make([]float32, width*height*3)
		r.haloDirty = nil
		r.depth = make([]float32, dotWidth*dotHeight)
		for i := range r.depth {
			r.depth[i] = float32(math.Inf(1))
		}
		r.faceIDs = make([]uint16, dotWidth*dotHeight)
		r.dirty = nil
	} else if r.dirty != nil && !accumulate {
		d := *r.dirty
		for row := d.minY; row <= d.maxY; row++ {
			clear(r.bits[row*width+d.minX : row*width+d.maxX+1])
			clear(r.counts[row*width+d.minX : row*width+d.maxX+1])
			clear(r.rgb[(row*width+d.minX)*3 : (row*width+d.maxX+1)*3])
		}
		r.dirty = nil
	}
	if r.haloDirty != nil {
		d := *r.haloDirty
		for row := d.minY; row <= d.maxY; row++ {
			clear(r.haloAmount[row*width+d.minX : row*width+d.maxX+1])
			clear(r.haloRgb[(row*width+d.minX)*3 : (row*width+d.maxX+1)*3])
		}
		r.haloDirty = nil
	}

	m := logoRotation(pose.yaw, pose.pitch, pose.roll)
	centerX, centerY, scale := pose.centerX, pose.centerY, pose.scale
	// The camera sits at (0, 0, logoCameraDistance) in camera space; object space is the transpose rotation.
	origin := [3]float64{m[6] * logoCameraDistance, m[7] * logoCameraDistance, m[8] * logoCameraDistance}
	light := logoIsLight(background)
	faces := r.visibleFaces(m, origin, pose, boxes, dotWidth, dotHeight, light)
	if len(faces) == 0 {
		if haloStrength > 0 {
			r.renderHalo(haloStrength)
		}
		return
	}
	minX, minY, maxX, maxY := dotWidth, dotHeight, -1, -1
	for _, face := range faces {
		minX = min(minX, face.minX)
		minY = min(minY, face.minY)
		maxX = max(maxX, face.maxX)
		maxY = max(maxY, face.maxY)
	}
	if maxX < minX || maxY < minY {
		return
	}

	depth, faceIDs := r.depth, r.faceIDs
	for faceIndex, face := range faces {
		a := face.axis
		u := (a + 1) % 3
		v := (a + 2) % 3
		originA, originU, originV := origin[a], origin[u], origin[v]
		// The ray direction in object space is linear in the dot position, so it is stepped per dot.
		stepA, stepU, stepV := m[a]/scale, m[u]/scale, m[v]/scale
		sx := (float64(face.minX) + 0.5 - centerX) / scale
		for dotY := face.minY; dotY <= face.maxY; dotY++ {
			sy := (float64(dotY) + 0.5 - centerY) / scale
			directionA := m[a]*sx + m[3+a]*sy - m[6+a]*logoCameraDistance
			directionU := m[u]*sx + m[3+u]*sy - m[6+u]*logoCameraDistance
			directionV := m[v]*sx + m[3+v]*sy - m[6+v]*logoCameraDistance
			index := dotY*dotWidth + face.minX
			for dotX := face.minX; dotX <= face.maxX; dotX, index = dotX+1, index+1 {
				t := (face.plane - originA) / directionA
				if t > 0 && t < float64(depth[index]) {
					hitU := originU + t*directionU
					hitV := originV + t*directionV
					if hitU >= face.uMin && hitU <= face.uMax && hitV >= face.vMin && hitV <= face.vMax {
						depth[index] = float32(t)
						faceIDs[index] = uint16(faceIndex + 1)
					}
				}
				directionA += stepA
				directionU += stepU
				directionV += stepV
			}
		}
	}

	// Shade the hit dots, pack them into braille cells, and reset the dot buffers for the next frame.
	bits, counts, rgb := r.bits, r.counts, r.rgb
	cellMinX, cellMinY, cellMaxX, cellMaxY := width, height, -1, -1
	for dotY := minY; dotY <= maxY; dotY++ {
		index := dotY*dotWidth + minX
		for dotX := minX; dotX <= maxX; dotX, index = dotX+1, index+1 {
			id := faceIDs[index]
			if id == 0 {
				continue
			}
			face := faces[id-1]
			// Points farther from the camera fade slightly toward the background for depth.
			fog := clamp01(0.15 - logoCameraDistance*(1-float64(depth[index]))*0.12)
			if light {
				fog *= 0.5
			}
			faceIDs[index] = 0
			depth[index] = float32(math.Inf(1))
			if keep != nil && !keep(index) {
				continue
			}
			cellX := dotX >> 1
			cellY := dotY >> 2
			cell := cellY*width + cellX
			bits[cell] |= logoDotBits[(dotY&3)*2+(dotX&1)]
			counts[cell]++
			rgb[cell*3] = float32(float64(rgb[cell*3]) + (face.red + (background[0]-face.red)*fog))
			rgb[cell*3+1] = float32(float64(rgb[cell*3+1]) + (face.green + (background[1]-face.green)*fog))
			rgb[cell*3+2] = float32(float64(rgb[cell*3+2]) + (face.blue + (background[2]-face.blue)*fog))
			cellMinX = min(cellMinX, cellX)
			cellMaxX = max(cellMaxX, cellX)
			cellMinY = min(cellMinY, cellY)
			cellMaxY = max(cellMaxY, cellY)
		}
	}
	if cellMaxX >= 0 {
		if accumulate && r.dirty != nil {
			cellMinX, cellMinY = min(cellMinX, r.dirty.minX), min(cellMinY, r.dirty.minY)
			cellMaxX, cellMaxY = max(cellMaxX, r.dirty.maxX), max(cellMaxY, r.dirty.maxY)
		}
		r.dirty = &logoBounds{minX: cellMinX, minY: cellMinY, maxX: cellMaxX, maxY: cellMaxY}
	}
	if haloStrength > 0 {
		r.renderHalo(haloStrength)
	}
}

func logoTent(radius int) []float64 {
	weights := make([]float64, radius*2+1)
	total := 0.0
	for i := range weights {
		weights[i] = float64(radius + 1 - absInt(i-radius))
		total += weights[i]
	}
	for i := range weights {
		weights[i] /= total
	}
	return weights
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

var (
	logoHaloWeightsX = logoTent(logoHaloRadiusX)
	logoHaloWeightsY = logoTent(logoHaloRadiusY)
)

// renderHalo blurs the pig's coverage and color over neighboring cells with a separable tent filter, so the tint falls off
// smoothly past the pig's edges.
func (r *logoRaster) renderHalo(strength float64) {
	if r.dirty == nil {
		return
	}
	cells := *r.dirty
	width, height, counts, rgb := r.width, r.height, r.counts, r.rgb
	minX := max(0, cells.minX-logoHaloRadiusX)
	maxX := min(width-1, cells.maxX+logoHaloRadiusX)
	minY := max(0, cells.minY-logoHaloRadiusY)
	maxY := min(height-1, cells.maxY+logoHaloRadiusY)
	regionWidth := maxX - minX + 1

	// Horizontal pass over the rows that contain the pig: coverage and premultiplied color per cell.
	rows := cells.maxY - cells.minY + 1
	horizontal := make([]float32, rows*regionWidth*4)
	for y := cells.minY; y <= cells.maxY; y++ {
		for x := minX; x <= maxX; x++ {
			var coverage, red, green, blue float64
			for dx := -logoHaloRadiusX; dx <= logoHaloRadiusX; dx++ {
				sourceX := x + dx
				if sourceX < cells.minX || sourceX > cells.maxX {
					continue
				}
				source := y*width + sourceX
				count := counts[source]
				if count == 0 {
					continue
				}
				weight := logoHaloWeightsX[dx+logoHaloRadiusX] / 8
				coverage += float64(count) * weight
				red += float64(rgb[source*3]) * weight
				green += float64(rgb[source*3+1]) * weight
				blue += float64(rgb[source*3+2]) * weight
			}
			target := ((y-cells.minY)*regionWidth + (x - minX)) * 4
			horizontal[target] = float32(coverage)
			horizontal[target+1] = float32(red)
			horizontal[target+2] = float32(green)
			horizontal[target+3] = float32(blue)
		}
	}

	// Vertical pass into the halo buffers.
	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			var coverage, red, green, blue float64
			for dy := -logoHaloRadiusY; dy <= logoHaloRadiusY; dy++ {
				sourceY := y + dy
				if sourceY < cells.minY || sourceY > cells.maxY {
					continue
				}
				weight := logoHaloWeightsY[dy+logoHaloRadiusY]
				source := ((sourceY-cells.minY)*regionWidth + (x - minX)) * 4
				coverage += float64(horizontal[source]) * weight
				red += float64(horizontal[source+1]) * weight
				green += float64(horizontal[source+2]) * weight
				blue += float64(horizontal[source+3]) * weight
			}
			target := y*width + x
			// Cells under the pig keep at least their own coverage, so faces stay solidly tinted.
			count := float64(counts[target])
			if count > 0 && count/8 > coverage {
				coverage = count / 8
				red = (float64(rgb[target*3]) / count) * coverage
				green = (float64(rgb[target*3+1]) / count) * coverage
				blue = (float64(rgb[target*3+2]) / count) * coverage
			}
			if coverage <= 0 {
				continue
			}
			// Quantized so neighboring cells usually share a background and its escape sequence.
			amount := jsRoundFloat(strength*math.Pow(math.Min(1, coverage), 0.7)*logoHaloLevels) / logoHaloLevels
			if amount <= 0 {
				continue
			}
			r.haloAmount[target] = float32(amount)
			r.haloRgb[target*3] = float32(jsRoundFloat(red/coverage/logoColorStep) * logoColorStep)
			r.haloRgb[target*3+1] = float32(jsRoundFloat(green/coverage/logoColorStep) * logoColorStep)
			r.haloRgb[target*3+2] = float32(jsRoundFloat(blue/coverage/logoColorStep) * logoColorStep)
		}
	}
	r.haloDirty = &logoBounds{minX: minX, minY: minY, maxX: maxX, maxY: maxY}
}

func (r *logoRaster) visibleFaces(m [9]float64, origin [3]float64, pose logoPose, boxes []logoBox, dotWidth, dotHeight int, lightBackground bool) []logoFace {
	faces := r.faces[:0]
	for boxIndex, box := range boxes {
		for a := range 3 {
			u := (a + 1) % 3
			v := (a + 2) % 3
			for _, side := range [2]float64{-1, 1} {
				plane := box.min[a]
				if side > 0 {
					plane = box.max[a]
				}
				// Back faces point away from the camera.
				if (origin[a]-plane)*side <= 0 {
					continue
				}
				// Faces pressed against a neighboring block are hidden. Positions are exact while blocks rest.
				covered := false
				for otherIndex, other := range boxes {
					touching := other.max[a]
					if side > 0 {
						touching = other.min[a]
					}
					if otherIndex != boxIndex && touching == plane &&
						other.min[u] <= box.min[u] && other.max[u] >= box.max[u] &&
						other.min[v] <= box.min[v] && other.max[v] >= box.max[v] {
						covered = true
						break
					}
				}
				if covered {
					continue
				}

				minX, minY := math.Inf(1), math.Inf(1)
				maxX, maxY := math.Inf(-1), math.Inf(-1)
				var corner [3]float64
				for _, cornerU := range [2]float64{box.min[u], box.max[u]} {
					for _, cornerV := range [2]float64{box.min[v], box.max[v]} {
						corner[a] = plane
						corner[u] = cornerU
						corner[v] = cornerV
						x := m[0]*corner[0] + m[1]*corner[1] + m[2]*corner[2]
						y := m[3]*corner[0] + m[4]*corner[1] + m[5]*corner[2]
						z := m[6]*corner[0] + m[7]*corner[1] + m[8]*corner[2]
						perspective := (pose.scale * logoCameraDistance) / (logoCameraDistance - z)
						screenX := pose.centerX + x*perspective
						screenY := pose.centerY + y*perspective
						minX = math.Min(minX, screenX)
						minY = math.Min(minY, screenY)
						maxX = math.Max(maxX, screenX)
						maxY = math.Max(maxY, screenY)
					}
				}
				face := logoFace{
					minX: int(math.Max(0, math.Floor(minX))),
					minY: int(math.Max(0, math.Floor(minY))),
					maxX: int(math.Min(float64(dotWidth-1), math.Ceil(maxX))),
					maxY: int(math.Min(float64(dotHeight-1), math.Ceil(maxY))),
				}
				if face.maxX < face.minX || face.maxY < face.minY {
					continue
				}

				// Faces are flat, so lighting is computed once per face. The normal in camera space is a column of the
				// rotation matrix.
				nx := m[a] * side
				ny := m[3+a] * side
				nz := m[6+a] * side
				diffuse := math.Max(0, nx*logoLight[0]+ny*logoLight[1]+nz*logoLight[2])
				rim := math.Max(0, nx*0.8-nz*0.3)
				// On light backgrounds, highlights toward white would vanish, so faces only get darker than the
				// sprite's colors there.
				specular, shade := 0.0, 0.0
				if lightBackground {
					shade = math.Min(1, 0.55+diffuse*0.45+rim*0.1)
				} else {
					specular = math.Pow(math.Max(0, nx*logoHalfVector[0]+ny*logoHalfVector[1]+nz*logoHalfVector[2]), 24) * 0.6 * 255
					shade = 0.45 + diffuse*0.78 + rim*0.25
				}
				face.axis = a
				face.plane = plane
				face.uMin, face.uMax = box.min[u], box.max[u]
				face.vMin, face.vMax = box.min[v], box.max[v]
				face.red = math.Min(255, box.color[0]*shade+specular)
				face.green = math.Min(255, box.color[1]*shade+specular)
				face.blue = math.Min(255, box.color[2]*shade+specular)
				faces = append(faces, face)
			}
		}
	}
	r.faces = faces
	return faces
}

// pigLogoAnimationOptions are the screen to dissolve, as rendered lines, and the terminal cell of the header pig's top-left
// corner.
type pigLogoAnimationOptions struct {
	screen     []string
	logoColumn int
	logoRow    int
	// clearColumns and clearRows are the cells of the clicked logo, which the 3D pig replaces: the head's, or the text mark's.
	clearColumns, clearRows int
}

type logoExit struct {
	start     time.Time
	time      float64
	yaw       float64
	targetYaw float64
	// run is the running pig when the exit started, which dissolves back into the head (nil when it was not running).
	run *pigRunner
}

type logoHint struct {
	row, start int
	text       string
	keyLength  int
	keyColor   logoRgb
	color      logoRgb
}

// pigLogoAnimation is the fullscreen easter egg shown when the header pig is clicked. The current screen dissolves into
// braille dust, spreading out from the pig, while the pig lifts off as a 3D object, flies to the center, grows, spins and
// runs. Leaving plays the same timeline backwards, so the pig lands on the header and the screen reassembles.
//
// The pig is ray cast per braille dot. A braille cell holds 2x4 dots and roughly square dots, so one pig pixel (a half
// block) is exactly 2x2 dots and the header pig (HeadCells by HeadRows cells) is 32x28 dots at the start.
//
// All methods run on the interactive owner loop; the timer worker only posts there.
type pigLogoAnimation struct {
	tui.BaseComponent
	rows       func() int
	options    pigLogoAnimationOptions
	foreground logoRgb
	background logoRgb
	onDone     func()
	variant    piglogin.Variant
	// runColors is the running pig's palette (pigRunPalette), computed on its first run.
	runColors  map[byte]logoRgb
	now        func() time.Time
	startTime  time.Time
	lastRender time.Time
	// running is true until finish; the timer worker stops posting once it is false.
	running      bool
	exit         *logoExit
	blocks       []logoBlock
	screenWidth  int
	screenHeight int
	// stars holds a star per cell index, or nil.
	stars     []*logoStar
	cells     [][]logoScreenCell
	ansiCache map[int]string
	logo      logoRaster
	boxes     []logoBox
	pigBoxes  []logoBox
	cancel    context.CancelFunc
	timerDone chan struct{}
}

func newPigLogoAnimation(rows func() int, options pigLogoAnimationOptions, variant piglogin.Variant, foreground, background logoRgb, now func() time.Time, onDone func()) *pigLogoAnimation {
	start := now()
	return &pigLogoAnimation{
		rows:         rows,
		options:      options,
		foreground:   foreground,
		background:   background,
		onDone:       onDone,
		variant:      variant,
		now:          now,
		startTime:    start,
		lastRender:   start,
		running:      true,
		blocks:       pigLogoBlocks(variant),
		screenWidth:  -1,
		screenHeight: -1,
		ansiCache:    map[int]string{},
	}
}

// startTimer owns the frame timer: each tick runs on the owner loop through post and stops the animation once its exit
// finished, or once nothing rendered it for a second (as when PiG hides every overlay on exit); otherwise it requests a
// render. Dispose cancels and joins the worker.
func (a *pigLogoAnimation) startTimer(parent context.Context, spawn func(func()), post func(context.Context, func()) error, requestRender func()) {
	ctx, cancel := context.WithCancel(parent)
	a.cancel = cancel
	a.timerDone = make(chan struct{})
	spawn(func() {
		defer close(a.timerDone)
		ticker := time.NewTicker(logoFrameInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			stopped := make(chan bool, 1)
			if err := post(ctx, func() {
				if ctx.Err() != nil || !a.running {
					stopped <- true
					return
				}
				a.tick(requestRender)
				stopped <- !a.running
			}); err != nil {
				return
			}
			select {
			case <-ctx.Done():
				return
			case done := <-stopped:
				if done {
					return
				}
			}
		}
	})
}

func (a *pigLogoAnimation) tick(requestRender func()) {
	if (a.exit != nil && a.exitProgress() >= 1) || a.now().Sub(a.lastRender) > time.Second {
		a.finish()
		return
	}
	requestRender()
}

// Dispose stops the timer without finishing: the renderer that showed the overlay is going away.
func (a *pigLogoAnimation) Dispose() {
	a.running = false
	if a.cancel != nil {
		a.cancel()
		<-a.timerDone
		a.cancel = nil
	}
}

// close plays the exit animation. A second call skips it.
func (a *pigLogoAnimation) close() {
	if a.exit != nil {
		a.finish()
		return
	}
	elapsed := a.elapsed()
	yaw := logoSpinAngle(elapsed)
	turn := math.Pi * 2
	run := a.runner(elapsed)
	a.exit = &logoExit{
		start:     a.now(),
		time:      elapsed,
		yaw:       yaw,
		targetYaw: math.Ceil(yaw/turn) * turn,
		run:       run,
	}
}

func (a *pigLogoAnimation) HandleInput(data string) {
	keybindings := tui.GetKeybindings()
	if keybindings.Matches(data, "tui.select.cancel") || keybindings.Matches(data, "app.clear") {
		a.close()
	}
}

func (a *pigLogoAnimation) HandleMouse(event tui.TuiMouseEvent) *tui.TuiMouseDispatchResult {
	if event.Type == tui.MouseClick {
		a.close()
	}
	render := false
	return &tui.TuiMouseDispatchResult{TuiMouseEventResult: tui.TuiMouseEventResult{Handled: true, Render: &render}}
}

func (a *pigLogoAnimation) Invalidate() {
	a.BaseComponent.Invalidate()
	a.screenWidth = -1
	a.screenHeight = -1
	clear(a.ansiCache)
}

func (a *pigLogoAnimation) Render(width int) []string {
	return a.renderFrame(width, a.runner(a.elapsed()))
}

// renderFrame is Pi's frame, with the running pig, when it is not nil, as the 3D object in place of the head.
func (a *pigLogoAnimation) renderFrame(width int, runner *pigRunner) []string {
	height := max(1, a.rows())
	foreground, background := a.foreground, a.background
	a.lastRender = a.now()
	if width != a.screenWidth || height != a.screenHeight {
		a.screenWidth = width
		a.screenHeight = height
		a.cells = a.prepareCells(width, foreground)
		a.stars = a.prepareStars(width, height)
	}

	// Dissolve time runs forward on entry and backward on exit, so the screen reassembles in reverse order.
	var dissolveTime, hintAlpha, starAlpha float64
	var pose logoPose
	if a.exit != nil {
		progress := a.exitProgress()
		landing := clamp01(progress / 0.8)
		settle := 1 - logoSmooth(landing)
		dissolveTime = math.Min(a.exit.time, logoDissolveEnd) * (1 - progress)
		pose = a.pose(width, height, a.flyProgress(a.exit.time)*settle, a.exit.time)
		pose.yaw = a.exit.yaw + (a.exit.targetYaw-a.exit.yaw)*logoEaseOut(landing)
		pose.pitch *= settle
		pose.roll *= settle
		hintAlpha = a.hintAlpha(a.exit.time) * (1 - logoSmooth(progress/0.2))
		starAlpha = a.starAlpha(a.exit.time) * (1 - logoSmooth(progress/0.3))
	} else {
		elapsed := a.elapsed()
		dissolveTime = elapsed
		pose = a.pose(width, height, a.flyProgress(elapsed), elapsed)
		hintAlpha = a.hintAlpha(elapsed)
		starAlpha = a.starAlpha(elapsed)
	}

	a.boxes = a.boxes[:0]
	if runner != nil {
		a.pigBoxes = runner.appendBoxes(a.pigBoxes[:0])
	}
	// The head stays whole (blockOffsets); it is not drawn once the running pig has fully replaced it.
	if runner == nil || runner.mix < 1 {
		for _, block := range a.blocks {
			x := block.home[0]*pigLogoPixel - pigLogoCenterX
			y := block.home[1]*pigLogoPixel - pigLogoCenterY
			a.boxes = append(a.boxes, logoBox{
				min:   [3]float64{x, y, -logoDepth / 2},
				max:   [3]float64{x + pigLogoPixel, y + pigLogoPixel, logoDepth / 2},
				color: block.color,
			})
		}
	}
	logo := &a.logo
	haloStrength := 0.0
	if logoIsLight(background) {
		haloStrength = logoLightHalo
	}
	switch {
	case runner == nil:
		logo.render(width, height, pose, a.boxes, background, haloStrength)
	case runner.mix >= 1:
		logo.render(width, height, runner.pose(pose), a.pigBoxes, background, haloStrength)
	default:
		// Each dot shows the pig once the mix passes its threshold, and the head before, so the dots trade places.
		mix := runner.mix
		logo.renderLayer(width, height, pose, a.boxes, background, 0, func(dot int) bool { return pigRunDotThreshold(dot) >= mix }, false)
		logo.renderLayer(width, height, runner.pose(pose), a.pigBoxes, background, haloStrength, func(dot int) bool { return pigRunDotThreshold(dot) < mix }, true)
	}
	halo := func(index int, base logoRgb, hasBase bool) (logoRgb, bool) {
		amount := float64(logo.haloAmount[index])
		if amount <= 0 {
			return base, hasBase
		}
		if !hasBase {
			base = background
		}
		color := logo.haloRgb
		return logoMix(base, logoRgb{float64(color[index*3]), float64(color[index*3+1]), float64(color[index*3+2])}, amount), true
	}
	backgroundFade := logoSmooth(dissolveTime / logoBackgroundFade)
	textFade := logoSmooth(dissolveTime/0.6) * 0.3
	// Once the screen has fully dissolved, the text layer is empty and can be skipped.
	textActive := dissolveTime < logoDissolveEnd || backgroundFade < 1
	hint := a.hint(width, height, hintAlpha, background)
	frame := math.Floor(dissolveTime * 14)
	now := a.elapsed()
	// Star brightness is quantized, so a row only changes when one of its stars steps to another level instead of on
	// every frame.
	starLevel := jsRoundFloat(starAlpha*logoStarAlphaLevels) / logoStarAlphaLevels
	starfield := func(index int) (string, logoRgb, bool) {
		if starLevel <= 0 || index >= len(a.stars) {
			return "", logoRgb{}, false
		}
		star := a.stars[index]
		if star == nil {
			return "", logoRgb{}, false
		}
		flicker := jsRoundFloat((0.5+0.5*math.Sin(now*star.speed+star.phase))*logoStarFlickerLevel) / logoStarFlickerLevel
		return star.glyph, logoMix(background, logoMix(foreground, star.tint, 0.5), star.strength*(0.7+0.3*flicker)*starLevel), true
	}
	lines := make([]string, 0, height)
	var line strings.Builder
	for row := range height {
		var cells []logoScreenCell
		if row < len(a.cells) {
			cells = a.cells[row]
		}
		line.Reset()
		currentFg, currentBg := -1, -1
		emit := func(text string, fg logoRgb, hasFg bool, bg logoRgb, hasBg bool) {
			fgKey, bgKey := -1, -1
			if hasFg {
				fgKey = logoPack(fg)
			}
			if hasBg {
				bgKey = logoPack(bg)
			}
			if fgKey != currentFg && text != " " {
				if hasFg {
					line.WriteString(a.ansi(fgKey, false))
				} else {
					line.WriteString("\x1b[39m")
				}
				currentFg = fgKey
			}
			if bgKey != currentBg {
				if hasBg {
					line.WriteString(a.ansi(bgKey, true))
				} else {
					line.WriteString("\x1b[49m")
				}
				currentBg = bgKey
			}
			line.WriteString(text)
		}
		for column := 0; column < width; column++ {
			index := row*width + column
			var cell *logoScreenCell
			if textActive && column < len(cells) {
				cell = &cells[column]
			}
			var base logoRgb
			hasBase := false
			if cell != nil && cell.hasBg && backgroundFade < 1 {
				base, hasBase = logoMix(cell.bg, background, backgroundFade), true
			}
			cellBg, hasCellBg := halo(index, base, hasBase)
			if hint != nil && row == hint.row && column >= hint.start && column < hint.start+len(hint.text) {
				offset := column - hint.start
				color := hint.color
				if offset < hint.keyLength {
					color = hint.keyColor
				}
				emit(hint.text[offset:offset+1], color, true, cellBg, hasCellBg)
				continue
			}
			if dots := logo.bits[index]; dots != 0 {
				// Quantized so neighboring cells on one face usually share a color and its escape sequence.
				step := float64(logoColorStep * int(logo.counts[index]))
				rgb := logo.rgb
				color := logoRgb{
					jsRoundFloat(float64(rgb[index*3])/step) * logoColorStep,
					jsRoundFloat(float64(rgb[index*3+1])/step) * logoColorStep,
					jsRoundFloat(float64(rgb[index*3+2])/step) * logoColorStep,
				}
				emit(logoBraille[dots], color, true, cellBg, hasCellBg)
				continue
			}
			if cell == nil {
				glyph, fg, ok := starfield(index)
				if !ok {
					glyph = " "
				}
				emit(glyph, fg, ok, cellBg, hasCellBg)
				continue
			}
			dust := (dissolveTime - cell.delay) / logoDustDuration
			if dust < 0 && cell.width == 2 && (index+1 >= len(logo.bits) || logo.bits[index+1] == 0) {
				emit(cell.text, logoMix(cell.fg, background, textFade), true, cellBg, hasCellBg)
				column++
				continue
			}
			if dust < 0 && cell.width == 1 {
				emit(cell.text, logoMix(cell.fg, background, textFade), true, cellBg, hasCellBg)
				continue
			}
			dotCount := cell.ink
			if dust >= 0 {
				dotCount = int(jsRoundFloat(float64(cell.ink) * (1 - clamp01(dust))))
			}
			if dotCount <= 0 {
				glyph, fg, ok := starfield(index)
				if !ok {
					glyph = " "
				}
				emit(glyph, fg, ok, cellBg, hasCellBg)
				continue
			}
			// Pick dotCount distinct dots; the choice changes a few times per second so the dust shimmers.
			var bits uint8
			placed := 0
			for attempt := 0; placed < dotCount && attempt < 32; attempt++ {
				bit := logoDotBits[int(math.Floor(logoHash(float64(cell.seed)*7919+frame*131+float64(attempt))*8))]
				if bits&bit != 0 {
					continue
				}
				bits |= bit
				placed++
			}
			fade := textFade + (1-textFade)*math.Pow(clamp01(dust), 0.8)
			emit(logoBraille[bits], logoMix(cell.fg, background, fade), true, cellBg, hasCellBg)
		}
		line.WriteString("\x1b[0m")
		lines = append(lines, line.String())
	}
	return lines
}

func (a *pigLogoAnimation) elapsed() float64 {
	return a.now().Sub(a.startTime).Seconds()
}

func (a *pigLogoAnimation) exitProgress() float64 {
	if a.exit == nil {
		return 0
	}
	return clamp01(a.now().Sub(a.exit.start).Seconds() / logoExitDuration)
}

func (a *pigLogoAnimation) finish() {
	if !a.running {
		return
	}
	a.running = false
	if a.cancel != nil {
		a.cancel()
	}
	a.onDone()
}

// blockOffsets is each block's displacement from its place in the header pig at time, in grid units.
//
// pig divergence (D87): Pi's logo slides its blocks around as a puzzle here; the pig's head stays whole and the running
// pig (runner) takes its place for the puzzle's shuffle time.
func (a *pigLogoAnimation) blockOffsets(float64) [][3]float64 {
	return make([][3]float64, len(a.blocks))
}

func (a *pigLogoAnimation) flyProgress(time float64) float64 {
	return logoSmooth((time - logoFlyStart) / logoFlyDuration)
}

func (a *pigLogoAnimation) starAlpha(time float64) float64 {
	return logoSmooth((time - logoStarsStart) / logoStarsFade)
}

func (a *pigLogoAnimation) hintAlpha(time float64) float64 {
	return logoSmooth((time - logoFlyStart - logoFlyDuration) / logoHintFade)
}

func (a *pigLogoAnimation) pose(width, height int, progress, time float64) logoPose {
	// At the start the front face must cover exactly the header pig's 32x28 dots despite the perspective.
	reach := pigLogoRadius * logoReachFactor
	endScale := math.Max(logoStartScale, math.Min(float64(width)*2*0.35, (float64(height)*4-8)*0.48)/reach)
	startX := float64(a.options.logoColumn*2) + pigLogoCenterX*(2/pigLogoPixel)
	startY := float64(a.options.logoRow*4) + pigLogoCenterY*(2/pigLogoPixel)
	endX := float64(width)
	endY := float64(height)*2 - 2
	phase := logoSpinPhase(time)
	return logoPose{
		centerX: startX + (endX-startX)*progress,
		centerY: startY + (endY-startY)*progress,
		// Interpolate the zoom geometrically so it feels uniform.
		scale: logoStartScale * math.Pow(endScale/logoStartScale, progress),
		yaw:   logoSpinAngle(time),
		// Tilts are zero at whole turns, so the camera looks straight at the front of the pig at home.
		pitch: 0.3 * math.Sin(phase) * progress,
		roll:  0.06 * math.Sin(2*phase) * progress,
	}
}

func (a *pigLogoAnimation) hint(width, height int, alpha float64, background logoRgb) *logoHint {
	if alpha <= 0 {
		return nil
	}
	key := "escape"
	if keys := tui.GetKeybindings().GetKeys("tui.select.cancel"); len(keys) > 0 {
		key = keys[0]
	}
	key = tui.FormatKeyText(key, false)
	text := key + " to return"
	// Pi measures the hint in UTF-16 code units; the key names and the text are ASCII.
	if len(text) > width {
		return nil
	}
	colors := tui.ActiveTheme().ColorValues()
	return &logoHint{
		row:       height - 2,
		start:     (width - len(text)) / 2,
		text:      text,
		keyLength: len(key),
		keyColor:  logoMix(background, logoToRgb(colors["muted"]), alpha),
		color:     logoMix(background, logoToRgb(colors["dim"]), alpha),
	}
}

// prepareStars is a sparse, deterministic starfield: one braille dot in about logoStarDensity of all cells.
func (a *pigLogoAnimation) prepareStars(width, height int) []*logoStar {
	stars := make([]*logoStar, width*height)
	for index := range stars {
		if logoHash(float64(index*3+0x51ed)) >= logoStarDensity {
			continue
		}
		random := func(salt int) float64 { return logoHash(float64(index*7 + salt*0x9e37)) }
		stars[index] = &logoStar{
			glyph:    logoBraille[logoDotBits[int(math.Floor(random(1)*8))]],
			tint:     logoStarTints[int(math.Floor(random(2)*float64(len(logoStarTints))))],
			strength: 0.1 + random(3)*0.12,
			phase:    random(4) * math.Pi * 2,
			speed:    0.4 + random(5)*0.8,
		}
	}
	return stars
}

func (a *pigLogoAnimation) prepareCells(width int, foreground logoRgb) [][]logoScreenCell {
	cells := parseLogoScreen(a.options.screen, width, foreground, a.background)
	centerX := float64(a.options.logoColumn) + float64(piglogin.HeadCells)/2
	centerY := float64(a.options.logoRow) + float64(piglogin.HeadRows)/2
	height := float64(max(1, a.rows()))
	farthest := jsHypot(math.Max(centerX, float64(width)-centerX), math.Max(centerY, height-centerY)*2)
	for row, line := range cells {
		for column := range line {
			cell := &line[column]
			// Wide graphemes share their first column's timing so both halves change together.
			owner := cell
			if cell.width == 0 {
				owner = &line[column-1]
			}
			seed := logoHash(float64(row*65_537 + column))
			if cell.width != 0 {
				distance := jsHypot(float64(column)-centerX, (float64(row)-centerY)*2) / farthest
				cell.delay = logoFlyStart + distance*logoWaveSpread + seed*logoWaveJitter
			} else {
				cell.delay = owner.delay
			}
			cell.seed = row*65_537 + column
			cell.ink = logoGlyphInk(owner.text, seed)
		}
	}
	// The 3D pig replaces the header pig.
	for row := a.options.logoRow; row < a.options.logoRow+a.options.clearRows; row++ {
		if row < 0 || row >= len(cells) {
			continue
		}
		line := cells[row]
		for column := a.options.logoColumn; column < a.options.logoColumn+a.options.clearColumns; column++ {
			if column < 0 || column >= len(line) {
				continue
			}
			cell := &line[column]
			cell.text = " "
			cell.width = 1
			cell.ink = 0
			cell.hasBg = false
		}
	}
	return cells
}

func logoPack(color logoRgb) int {
	return int(jsRoundFloat(color[0]))<<16 | int(jsRoundFloat(color[1]))<<8 | int(jsRoundFloat(color[2]))
}

func (a *pigLogoAnimation) ansi(key int, isBackground bool) string {
	cacheKey := key * 2
	if isBackground {
		cacheKey++
	}
	if value, ok := a.ansiCache[cacheKey]; ok {
		return value
	}
	color := tui.RgbColorValue{R: float64((key >> 16) & 255), G: float64((key >> 8) & 255), B: float64(key & 255)}
	mode := tui.ActiveTheme().ColorMode()
	var value string
	if isBackground {
		value = tui.BackgroundAnsi(color, mode)
	} else {
		value = tui.ForegroundAnsi(color, mode)
	}
	a.ansiCache[cacheKey] = value
	return value
}
