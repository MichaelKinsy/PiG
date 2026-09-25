package codingagent

import (
	"image/color"
	"maps"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

const (
	loginFullWidth   = 66
	loginMarginWidth = 2
	loginMascotGap   = 3
	loginMascotX     = extension.LoginHeroWidth + loginMascotGap
	loginSceneWidth  = loginMascotX + extension.LoginMascotWidth
	loginSceneHeight = loginArtY + extension.LoginMascotHeight
	loginArtY        = extension.LoginBrandHeight + 1
	loginReset       = "\x1b[0m"
)

// LoginHeaderOptions contains host-owned values used by the native login
// template. OperationalLines are rendered after the extension-owned identity.
type LoginHeaderOptions struct {
	OperationalLines []string
	TrueColor        bool
	GlyphFree        bool
	ColorOverrides   map[byte]color.RGBA
}

type loginHeaderRenderer struct {
	mu          sync.Mutex
	definition  extension.ValidatedLoginDefinition
	options     LoginHeaderOptions
	cachedWidth int
	cachedLines []string
	cachedTheme *tui.Theme
	hasCache    bool
}

func newLoginHeaderRenderer(definition extension.ValidatedLoginDefinition, options LoginHeaderOptions) *loginHeaderRenderer {
	options.OperationalLines = append([]string(nil), options.OperationalLines...)
	options.ColorOverrides = maps.Clone(options.ColorOverrides)
	return &loginHeaderRenderer{definition: definition, options: options}
}

func (r *loginHeaderRenderer) Render(width int) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	theme := tui.ActiveTheme()
	if r.hasCache && r.cachedWidth == width && r.cachedTheme == theme {
		return append([]string(nil), r.cachedLines...)
	}
	r.cachedLines = RenderLoginHeader(r.definition, width, r.options)
	r.cachedWidth = width
	r.cachedTheme = theme
	r.hasCache = true
	return append([]string(nil), r.cachedLines...)
}

// RenderLoginHeader renders a validated definition with Pig's fixed native
// login template. It is local and deterministic; callers choose terminal color
// and glyph capabilities before rendering.
func RenderLoginHeader(definition extension.ValidatedLoginDefinition, width int, options LoginHeaderOptions) []string {
	if width <= 0 {
		return nil
	}
	if width < loginFullWidth {
		return renderCompactLoginHeader(definition, width, options.OperationalLines)
	}

	glyphFree := options.GlyphFree && width >= loginMarginWidth+loginSceneWidth*2
	scene := make([][]byte, loginSceneHeight)
	for y := range scene {
		scene[y] = []byte(strings.Repeat(".", loginSceneWidth))
	}
	brand := definition.Brand()
	blitLoginGrid(scene, brand, 0, 0, 1, 1)
	blitLoginGrid(scene, definition.Hero(), 0, loginArtY, 1, 1)
	blitLoginGrid(scene, definition.Mascot(), loginMascotX, loginArtY, 1, 1)
	// A fully transparent brand omits the brand band so the art starts at the
	// top of the header instead of below blank rows.
	top := 0
	if loginGridTransparent(brand) {
		top = loginArtY
	}
	rows := make([]string, 0, len(scene)-top)
	for y := top; y < len(scene); y++ {
		rows = append(rows, string(scene[y]))
	}
	lines := renderLoginPixels(rows, loginSceneWidth, len(rows), definition, options.ColorOverrides, options.TrueColor, glyphFree)
	lines = append(lines, "")
	lines = appendWrappedLoginText(lines, "\x1b[1m"+definition.Name()+loginReset+"  "+definition.Description(), width)
	lines = appendWrappedLoginText(lines, "\x1b[3m"+definition.Tagline(), width)
	for _, line := range options.OperationalLines {
		lines = appendWrappedLoginText(lines, line, width)
	}
	return lines
}

func renderCompactLoginHeader(definition extension.ValidatedLoginDefinition, width int, operationalLines []string) []string {
	values := []string{definition.Name(), definition.Description(), definition.Tagline()}
	values = append(values, operationalLines...)
	lines := make([]string, 0, len(values))
	for _, value := range values {
		lines = appendWrappedLoginText(lines, value, width)
	}
	return lines
}

func appendWrappedLoginText(lines []string, value string, width int) []string {
	margin := min(loginMarginWidth, max(0, width-1))
	prefix := strings.Repeat(" ", margin)
	available := width - margin
	for _, wrapped := range widthx.WrapTextWithAnsi(value, available) {
		lines = append(lines, prefix+wrapped+loginReset)
	}
	return lines
}

func loginGridTransparent(rows []string) bool {
	for _, row := range rows {
		if strings.Trim(row, ".") != "" {
			return false
		}
	}
	return true
}

func blitLoginGrid(destination [][]byte, source []string, xOffset, yOffset, xScale, yScale int) {
	for sourceY, row := range source {
		for sourceX := range len(row) {
			for dy := range yScale {
				for dx := range xScale {
					destination[yOffset+sourceY*yScale+dy][xOffset+sourceX*xScale+dx] = row[sourceX]
				}
			}
		}
	}
}

func renderLoginPixels(rows []string, pixelWidth, pixelHeight int, definition extension.ValidatedLoginDefinition, overrides map[byte]color.RGBA, trueColor, glyphFree bool) []string {
	if glyphFree {
		return renderLoginSolidPixels(rows, pixelWidth, pixelHeight, definition, overrides, trueColor)
	}
	return renderLoginHalfBlockPixels(rows, pixelWidth, pixelHeight, definition, overrides, trueColor)
}

func renderLoginHalfBlockPixels(rows []string, pixelWidth, pixelHeight int, definition extension.ValidatedLoginDefinition, overrides map[byte]color.RGBA, trueColor bool) []string {
	output := make([]string, 0, (pixelHeight+1)/2)
	for y := 0; y < pixelHeight; y += 2 {
		var line strings.Builder
		line.WriteString("  ")
		for x := range pixelWidth {
			top, topOK := loginPixelColor(rows, x, y, definition, overrides)
			bottom, bottomOK := loginPixelColor(rows, x, y+1, definition, overrides)
			switch {
			case topOK && bottomOK:
				line.WriteString(tui.FgSeq(top, trueColor))
				line.WriteString(tui.BgSeq(bottom, trueColor))
				line.WriteString("▀")
			case topOK:
				line.WriteString(tui.FgSeq(top, trueColor))
				line.WriteString("▀")
			case bottomOK:
				line.WriteString(tui.FgSeq(bottom, trueColor))
				line.WriteString("▄")
			default:
				line.WriteByte(' ')
			}
			line.WriteString(loginReset)
		}
		output = append(output, line.String())
	}
	return output
}

func renderLoginSolidPixels(rows []string, pixelWidth, pixelHeight int, definition extension.ValidatedLoginDefinition, overrides map[byte]color.RGBA, trueColor bool) []string {
	output := make([]string, 0, pixelHeight)
	for y := range pixelHeight {
		var line strings.Builder
		line.WriteString("  ")
		for x := range pixelWidth {
			pixel, ok := loginPixelColor(rows, x, y, definition, overrides)
			if ok {
				line.WriteString(tui.BgSeq(pixel, trueColor))
			}
			line.WriteString("  ")
			line.WriteString(loginReset)
		}
		output = append(output, line.String())
	}
	return output
}

func loginPixelColor(rows []string, x, y int, definition extension.ValidatedLoginDefinition, overrides map[byte]color.RGBA) (color.RGBA, bool) {
	if y < 0 || y >= len(rows) || x < 0 || x >= len(rows[y]) || rows[y][x] == '.' {
		return color.RGBA{}, false
	}
	symbol := rows[y][x]
	if value, ok := overrides[symbol]; ok {
		return value, true
	}
	return definition.Color(symbol)
}
