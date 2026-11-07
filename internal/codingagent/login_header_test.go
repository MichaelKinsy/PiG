package codingagent

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/piglogin"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

func loginHeaderDefinition(name string) extension.LoginDefinition {
	return extension.LoginDefinition{
		Brand:       filledLoginRows(extension.LoginBrandWidth, extension.LoginBrandHeight, 'B'),
		Hero:        filledLoginRows(extension.LoginHeroWidth, extension.LoginHeroHeight, 'H'),
		Mascot:      filledLoginRows(extension.LoginMascotWidth, extension.LoginMascotHeight, 'M'),
		Palette:     map[string]string{"B": "#102030", "H": "#405060", "M": "#708090"},
		Name:        name,
		Description: "A deterministic coding agent",
		Tagline:     "Ships focused changes",
	}
}

func loginHeaderFixture(t *testing.T) extension.ValidatedLoginDefinition {
	t.Helper()
	definition, err := extension.ValidateLoginDefinition(loginHeaderDefinition("Test Pig"))
	if err != nil {
		t.Fatal(err)
	}
	return definition
}

func filledLoginRows(width, height int, symbol byte) []string {
	rows := make([]string, height)
	for y := range rows {
		rows[y] = strings.Repeat(string(symbol), width)
	}
	return rows
}

func TestLoginHeaderFixedNativeTemplate(t *testing.T) {
	lines := RenderLoginHeader(loginHeaderFixture(t), 66, LoginHeaderOptions{TrueColor: true})
	if len(lines) != 13 { // 10 composed-art rows + separator + 2 identity rows
		t.Fatalf("got %d rows, want 13", len(lines))
	}
	for i, line := range lines {
		if got := widthx.VisibleWidth(line); got > 66 {
			t.Fatalf("row %d is %d cells wide", i, got)
		}
	}
	for row := range 10 {
		plain := widthx.StripAnsi(lines[row])
		if widthx.VisibleWidth(plain) != 53 {
			t.Fatalf("art row %d width = %d, want 53", row, widthx.VisibleWidth(plain))
		}
		wantColored := 41
		if row >= 3 {
			wantColored = 48
			cells := []rune(plain)
			if string(cells[:2]) != "  " || string(cells[34:37]) != "   " {
				t.Fatalf("art row %d does not preserve margin and hero/mascot gap: %q", row, plain)
			}
		}
		if strings.Count(plain, "▀") != wantColored {
			t.Fatalf("art row %d has %d colored cells, want %d", row, strings.Count(plain, "▀"), wantColored)
		}
	}
	if loginArtY != extension.LoginBrandHeight+1 {
		t.Fatalf("art offset = %d, want one pixel after %d-row brand", loginArtY, extension.LoginBrandHeight)
	}
	if lines[10] != "" {
		t.Fatalf("text separator = %q, want blank", lines[10])
	}
}

func TestLoginHeaderOmitsTransparentBrandBand(t *testing.T) {
	input := loginHeaderDefinition("Test Pig")
	input.Brand = filledLoginRows(extension.LoginBrandWidth, extension.LoginBrandHeight, '.')
	delete(input.Palette, "B")
	definition, err := extension.ValidateLoginDefinition(input)
	if err != nil {
		t.Fatal(err)
	}
	for _, options := range []LoginHeaderOptions{{TrueColor: true}, {TrueColor: true, GlyphFree: true}} {
		width := 66
		wantArt := extension.LoginHeroHeight / 2
		if options.GlyphFree {
			width, wantArt = 104, extension.LoginHeroHeight
		}
		lines := RenderLoginHeader(definition, width, options)
		if len(lines) != wantArt+3 {
			t.Fatalf("glyph-free %v: got %d rows, want %d art rows + separator + 2 identity rows", options.GlyphFree, len(lines), wantArt)
		}
		for row := range wantArt {
			if !strings.Contains(lines[row], "▀") && !strings.Contains(lines[row], "\x1b[48;2;") {
				t.Fatalf("glyph-free %v: art row %d is blank; the brand band was not omitted", options.GlyphFree, row)
			}
		}
		if lines[wantArt] != "" {
			t.Fatalf("glyph-free %v: separator = %q, want blank", options.GlyphFree, lines[wantArt])
		}
	}
}

func TestLoginHeaderAppliesHostThemeColorsWithoutChangingExtensionPalette(t *testing.T) {
	definition := loginHeaderFixture(t)
	overridden := RenderLoginHeader(definition, 66, LoginHeaderOptions{
		TrueColor:      true,
		ColorOverrides: map[byte]color.RGBA{'B': {R: 0xAA, G: 0xBB, B: 0xCC, A: 0xFF}},
	})
	if !strings.Contains(overridden[0], "\x1b[38;2;170;187;204m") {
		t.Fatalf("theme override missing from built-in row: %q", overridden[0])
	}

	m := &InteractiveMode{
		opts: InteractiveModeOptions{
			LoginVisible: true,
			LoginHeaderOptions: LoginHeaderOptions{
				TrueColor:      true,
				ColorOverrides: map[byte]color.RGBA{'B': {R: 0xAA, G: 0xBB, B: 0xCC, A: 0xFF}},
			},
		},
		extHeader: newSpecialLinesComponent(func() {}),
	}
	if err := (&ExtUIContext{m: m}).SetLogin(loginHeaderDefinition("Extension Pig")); err != nil {
		t.Fatal(err)
	}
	extensionLines := m.extHeader.Render(66)
	if !strings.Contains(extensionLines[0], "\x1b[38;2;16;32;48m") || strings.Contains(extensionLines[0], "38;2;170;187;204") {
		t.Fatalf("host theme colors leaked into extension palette: %q", extensionLines[0])
	}
}

func TestLoginHeaderUsesTrueColorAnd256ColorWithLineResets(t *testing.T) {
	definition := loginHeaderFixture(t)
	trueColor := RenderLoginHeader(definition, 66, LoginHeaderOptions{TrueColor: true})
	color256 := RenderLoginHeader(definition, 66, LoginHeaderOptions{TrueColor: false})
	if !strings.Contains(trueColor[0], "\x1b[38;2;16;32;48m") {
		t.Fatalf("truecolor row lacks 24-bit sequence: %q", trueColor[0])
	}
	if !strings.Contains(color256[0], "\x1b[38;5;") || strings.Contains(color256[0], "38;2;") {
		t.Fatalf("256-color row uses wrong capability: %q", color256[0])
	}
	for i, line := range append(trueColor, color256...) {
		if line != "" && !strings.HasSuffix(line, loginReset) {
			t.Fatalf("row %d does not end with reset: %q", i, line)
		}
	}
}

func TestLoginHeaderCompactFallbackStacksWrapsAndOmitsArt(t *testing.T) {
	definition := loginHeaderFixture(t)
	lines := RenderLoginHeader(definition, 65, LoginHeaderOptions{
		TrueColor:        true,
		OperationalLines: []string{"Version 1.2.3 with an intentionally long operational status that wraps"},
	})
	if len(lines) < 5 {
		t.Fatalf("compact output has %d lines, want wrapped stacked identity", len(lines))
	}
	joined := strings.Join(lines, "\n")
	for _, forbidden := range []string{"▀", "▄", "\x1b[38;", "\x1b[48;"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("compact output contains pixel-art token %q", forbidden)
		}
	}
	for i, line := range lines {
		if !strings.HasPrefix(line, "  ") {
			t.Fatalf("row %d lacks two-cell margin: %q", i, line)
		}
		if got := widthx.VisibleWidth(line); got > 65 {
			t.Fatalf("row %d is %d cells wide", i, got)
		}
		if !strings.HasSuffix(line, loginReset) {
			t.Fatalf("row %d lacks reset", i)
		}
	}
}

func TestLoginHeaderOperationalLinesAreHostOwnedInput(t *testing.T) {
	definition := loginHeaderFixture(t)
	without := RenderLoginHeader(definition, 80, LoginHeaderOptions{TrueColor: true})
	with := RenderLoginHeader(definition, 80, LoginHeaderOptions{TrueColor: true, OperationalLines: []string{"Pig v1.2.3", "Config ~/.pig"}})
	if len(with) != len(without)+2 {
		t.Fatalf("operational rows added %d lines, want 2", len(with)-len(without))
	}
	if !strings.Contains(with[len(with)-2], "Pig v1.2.3") || !strings.Contains(with[len(with)-1], "Config ~/.pig") {
		t.Fatalf("operational rows missing: %q", with[len(with)-2:])
	}
}

func TestLoginHeaderWrapsFullModeTextToWidth(t *testing.T) {
	input := extension.LoginDefinition{
		Brand:       filledLoginRows(extension.LoginBrandWidth, extension.LoginBrandHeight, 'B'),
		Hero:        filledLoginRows(extension.LoginHeroWidth, extension.LoginHeroHeight, 'H'),
		Mascot:      filledLoginRows(extension.LoginMascotWidth, extension.LoginMascotHeight, 'M'),
		Palette:     map[string]string{"B": "#102030", "H": "#405060", "M": "#708090"},
		Name:        strings.Repeat("N", extension.LoginNameWidthLimit),
		Description: strings.Repeat("D", extension.LoginDescriptionWidthLimit),
		Tagline:     strings.Repeat("T", extension.LoginTaglineWidthLimit),
	}
	definition, err := extension.ValidateLoginDefinition(input)
	if err != nil {
		t.Fatal(err)
	}

	lines := RenderLoginHeader(definition, 66, LoginHeaderOptions{
		TrueColor:        true,
		OperationalLines: []string{strings.Repeat("O", 80)},
	})
	for i, line := range lines {
		if got := widthx.VisibleWidth(line); got > 66 {
			t.Fatalf("row %d is %d cells wide: %q", i, got, line)
		}
	}
}

func TestLoginHeaderUsesGlyphFreeArtOnlyWhenItFits(t *testing.T) {
	definition := loginHeaderFixture(t)
	wide := RenderLoginHeader(definition, 104, LoginHeaderOptions{TrueColor: true, GlyphFree: true})
	joined := strings.Join(wide, "\n")
	if strings.ContainsAny(joined, "▀▄") {
		t.Fatal("glyph-free output contains half-block glyphs")
	}
	if !strings.Contains(joined, "\x1b[48;2;16;32;48m") {
		t.Fatal("glyph-free output lacks colored background cells")
	}
	if len(wide) != 23 { // 20 composed-art rows + separator + 2 identity rows
		t.Fatalf("glyph-free output has %d rows, want 23", len(wide))
	}
	for i, line := range wide {
		if got := widthx.VisibleWidth(line); got > 104 {
			t.Fatalf("wide glyph-free row %d is %d cells", i, got)
		}
	}

	narrow := RenderLoginHeader(definition, 103, LoginHeaderOptions{TrueColor: true, GlyphFree: true})
	if !strings.ContainsAny(strings.Join(narrow, "\n"), "▀▄") {
		t.Fatal("renderer did not fall back to half-block art when glyph-free art was too wide")
	}
}

func loginHeaderMarkerFixture(t *testing.T) extension.ValidatedLoginDefinition {
	t.Helper()
	brand := filledLoginRows(extension.LoginBrandWidth, extension.LoginBrandHeight, '.')
	brand[0] = "B" + brand[0][1:]
	brand[1] = brand[1][:1] + "D" + brand[1][2:]
	brand[4] = brand[4][:40] + "C"
	hero := filledLoginRows(extension.LoginHeroWidth, extension.LoginHeroHeight, '.')
	hero[0] = "H" + hero[0][1:]
	hero[13] = hero[13][:31] + "I"
	mascot := filledLoginRows(extension.LoginMascotWidth, extension.LoginMascotHeight, '.')
	mascot[0] = "M" + mascot[0][1:]
	mascot[13] = mascot[13][:15] + "N"
	definition, err := extension.ValidateLoginDefinition(extension.LoginDefinition{
		Brand: brand, Hero: hero, Mascot: mascot,
		Palette: map[string]string{
			"B": "#110000", "C": "#330000", "D": "#220000", "H": "#003300",
			"I": "#004400", "M": "#000055", "N": "#000066",
		},
		Name: "Markers", Description: "Geometry oracle", Tagline: "Every anchor has one place",
	})
	if err != nil {
		t.Fatal(err)
	}
	return definition
}

func TestLoginHeaderMapsSourcePixelAnchorsToFixedGeometry(t *testing.T) {
	lines := RenderLoginHeader(loginHeaderMarkerFixture(t), 66, LoginHeaderOptions{TrueColor: true})
	wantBlocks := map[[2]int]rune{
		{0, 2}:  '▀',
		{2, 42}: '▀',
		{3, 2}:  '▀',
		{3, 37}: '▀',
		{9, 33}: '▄',
		{9, 52}: '▄',
	}
	for point, want := range wantBlocks {
		plain := []rune(widthx.StripAnsi(lines[point[0]]))
		if plain[point[1]] != want {
			t.Fatalf("row %d column %d = %q, want colored anchor %q", point[0], point[1], plain[point[1]], want)
		}
	}
	for _, point := range [][2]int{{0, 4}, {2, 41}, {3, 3}, {3, 36}, {9, 32}, {9, 51}} {
		plain := []rune(widthx.StripAnsi(lines[point[0]]))
		if plain[point[1]] != ' ' {
			t.Fatalf("row %d column %d = %q, want transparent gap", point[0], point[1], plain[point[1]])
		}
	}
}

func TestLoginHeaderHalfBlocksResetAroundTransparentPixels(t *testing.T) {
	definition := loginHeaderMarkerFixture(t)
	lines := RenderLoginHeader(definition, 66, LoginHeaderOptions{TrueColor: true})
	brandTop := lines[0]
	if !strings.Contains(brandTop, "\x1b[38;2;17;0;0m▀\x1b[0m") {
		t.Fatalf("top-only pixel does not reset before transparency: %q", brandTop)
	}
	if !strings.Contains(brandTop, "\x1b[38;2;34;0;0m▄\x1b[0m ") {
		t.Fatalf("bottom-only pixel lacks the expected foreground and reset: %q", brandTop)
	}
	sceneTop := lines[3]
	if !strings.Contains(sceneTop, "\x1b[38;2;0;51;0m▀\x1b[0m") {
		t.Fatalf("opaque top/bottom pixel lacks foreground, background, and reset: %q", sceneTop)
	}
}

func TestLoginHeaderCompactFallbackFitsEveryPositiveBoundaryWidth(t *testing.T) {
	definition := loginHeaderFixture(t)
	for _, width := range []int{1, 2, 3, 10, 64, 65} {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			lines := RenderLoginHeader(definition, width, LoginHeaderOptions{
				OperationalLines: []string{"", strings.Repeat("unbroken", 12), "\x1b[1mstyled operational text\x1b[0m"},
			})
			for i, line := range lines {
				if got := widthx.VisibleWidth(line); got > width {
					t.Fatalf("row %d is %d cells wide at width %d: %q", i, got, width, line)
				}
				if line != "" && !strings.HasSuffix(line, loginReset) {
					t.Fatalf("row %d lacks reset: %q", i, line)
				}
			}
		})
	}
}

func TestLoginHeaderRendererCachesDefensivelyAndTracksTheme(t *testing.T) {
	definition := loginHeaderFixture(t)
	options := LoginHeaderOptions{TrueColor: true, OperationalLines: []string{"Pig v1"}}
	renderer := newLoginHeaderRenderer(definition, options)
	options.OperationalLines[0] = "changed outside renderer"
	tui.SetTheme("dark")
	defer tui.SetTheme("dark")

	first := renderer.Render(66)
	first[0] = "changed returned line"
	second := renderer.Render(66)
	if second[0] == first[0] || strings.Contains(strings.Join(second, "\n"), "changed outside renderer") {
		t.Fatal("cached render aliases caller-owned data")
	}
	oldTheme := renderer.cachedTheme
	tui.SetTheme("light")
	renderer.Render(66)
	if renderer.cachedTheme == oldTheme || renderer.cachedTheme != tui.ActiveTheme() {
		t.Fatal("theme change did not invalidate the cached render")
	}

}

func TestExtUIContextSetLoginSharesHeaderSlotAndInvalidPreservesCurrent(t *testing.T) {
	const builtInHeader = "stock header"
	m := &InteractiveMode{
		opts:      InteractiveModeOptions{BuiltInHeaderLines: []string{builtInHeader}, LoginHeaderOptions: LoginHeaderOptions{TrueColor: true}, LoginVisible: true},
		extHeader: newSpecialLinesComponent(func() {}),
	}
	m.restoreBuiltInHeader()
	ui := &ExtUIContext{m: m}

	ui.SetHeader(extension.FrameHeader([]string{"custom header"}, 0))
	if got := ui.SetLogin(extension.LoginDefinition{}); got == nil {
		t.Fatal("invalid SetLogin returned nil error")
	}
	if got := m.extHeader.Render(80); len(got) != 1 || got[0] != "custom header" {
		t.Fatalf("invalid SetLogin replaced active header: %q", got)
	}

	definition := extension.LoginDefinition{
		Brand:   filledLoginRows(extension.LoginBrandWidth, extension.LoginBrandHeight, 'B'),
		Hero:    filledLoginRows(extension.LoginHeroWidth, extension.LoginHeroHeight, 'H'),
		Mascot:  filledLoginRows(extension.LoginMascotWidth, extension.LoginMascotHeight, 'M'),
		Palette: map[string]string{"B": "#102030", "H": "#405060", "M": "#708090"},
		Name:    "Replacement", Description: "Last valid call wins", Tagline: "One shared slot",
	}
	if err := ui.SetLogin(definition); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(m.extHeader.Render(65), "\n"); !strings.Contains(got, "Replacement") || strings.Contains(got, "custom header") {
		t.Fatalf("valid SetLogin did not replace custom header: %q", got)
	}

	ui.SetHeader(nil)
	if got := strings.Join(m.extHeader.Render(65), "\n"); !strings.Contains(got, builtInHeader) || strings.Contains(got, "Replacement") {
		t.Fatalf("SetHeader(nil) did not restore stock header: %q", got)
	}
}

func TestRestoreBuiltInHeaderUsesConfiguredLines(t *testing.T) {
	m := &InteractiveMode{
		opts:      InteractiveModeOptions{BuiltInHeaderLines: []string{"minimal identity"}, LoginVisible: true},
		extHeader: newSpecialLinesComponent(func() {}),
	}
	m.restoreBuiltInHeader()
	if got := m.extHeader.Render(80); len(got) != 1 || got[0] != "minimal identity" {
		t.Fatalf("emergency header = %q", got)
	}
}

func TestExtUIContextSetLoginHonorsStartupSilenceGate(t *testing.T) {
	m := &InteractiveMode{
		opts:      InteractiveModeOptions{LoginVisible: false},
		extHeader: newSpecialLinesComponent(func() {}),
	}
	ui := &ExtUIContext{m: m}
	definition := extension.LoginDefinition{
		Brand:   filledLoginRows(extension.LoginBrandWidth, extension.LoginBrandHeight, 'B'),
		Hero:    filledLoginRows(extension.LoginHeroWidth, extension.LoginHeroHeight, 'H'),
		Mascot:  filledLoginRows(extension.LoginMascotWidth, extension.LoginMascotHeight, 'M'),
		Palette: map[string]string{"B": "#102030", "H": "#405060", "M": "#708090"},
		Name:    "Quiet Pig", Description: "Must stay hidden", Tagline: "No startup output",
	}
	if err := ui.SetLogin(definition); err != nil {
		t.Fatal(err)
	}
	if got := m.extHeader.Render(80); len(got) != 0 {
		t.Fatalf("quiet native login rendered %q", got)
	}
}

func TestExtUIContextSetLoginKeepsOperationalRowsWithoutBuiltInArt(t *testing.T) {
	m := &InteractiveMode{
		opts: InteractiveModeOptions{
			LoginVisible:       true,
			LoginHeaderOptions: LoginHeaderOptions{OperationalLines: []string{"PiG v1", "startup hints"}},
			BuiltInHeaderLines: []string{"minimal identity"},
		},
		extHeader: newSpecialLinesComponent(func() {}),
	}
	m.restoreBuiltInHeader()
	ui := &ExtUIContext{m: m}
	definition := extension.LoginDefinition{
		Brand:   filledLoginRows(extension.LoginBrandWidth, extension.LoginBrandHeight, 'B'),
		Hero:    filledLoginRows(extension.LoginHeroWidth, extension.LoginHeroHeight, 'H'),
		Mascot:  filledLoginRows(extension.LoginMascotWidth, extension.LoginMascotHeight, 'M'),
		Palette: map[string]string{"B": "#102030", "H": "#405060", "M": "#708090"},
		Name:    "Extension Pig", Description: "Works without built-in art", Tagline: "Core API",
	}
	if err := ui.SetLogin(definition); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(m.extHeader.Render(65), "\n")
	for _, want := range []string{"Extension Pig", "PiG v1", "startup hints"} {
		if !strings.Contains(got, want) {
			t.Fatalf("extension login output lacks %q: %q", want, got)
		}
	}
}

func TestNewSessionResetRestoresBuiltInHeaderBeforeHandlers(t *testing.T) {
	const builtInHeader = "stock header"
	m := &InteractiveMode{
		opts: InteractiveModeOptions{
			BuiltInHeaderLines: []string{builtInHeader},
			LoginVisible:       true,
		},
		extHeader: newSpecialLinesComponent(func() {}),
	}
	m.extHeader.SetLines([]string{"old extension login"})
	m.buildSlashContext(context.Background()).Reset()
	got := strings.Join(m.extHeader.Render(65), "\n")
	if !strings.Contains(got, builtInHeader) || strings.Contains(got, "old extension login") {
		t.Fatalf("new session did not restore stock header: %q", got)
	}
}

func TestSuccessfulReloadRestoresBuiltInHeaderAndFailedReloadPreservesCurrentLogin(t *testing.T) {
	restoreStartupTheme(t) // Reload applies the theme setting, which selects the process-wide active theme.
	const builtInHeader = "stock header"
	host := &orderRecordingHost{}
	m := reloadTestMode(InteractiveModeOptions{
		BuiltInHeaderLines: []string{builtInHeader},
		LoginVisible:       true,
		SubprocessHost:     host,
	})
	m.extHeader = newSpecialLinesComponent(func() {})
	m.extHeader.SetLines([]string{"old extension login"})
	if err := m.buildSlashContext(t.Context()).Reload(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(m.extHeader.Render(65), "\n"); !strings.Contains(got, builtInHeader) || strings.Contains(got, "old extension login") {
		t.Fatalf("successful reload did not restore stock header before session_start: %q", got)
	}

	m.extHeader.SetLines([]string{"active login after failed reload"})
	host.err = errors.New("reload failed")
	if err := m.buildSlashContext(t.Context()).Reload(); err != nil {
		t.Fatal(err)
	}
	if got := m.extHeader.Render(65); len(got) != 1 || got[0] != "active login after failed reload" {
		t.Fatalf("failed reload replaced active login: %q", got)
	}
}

// D60: SetLogin and SetHeader share one slot and the last successful call wins. A quiet startup accepts the login but keeps the slot hidden, so it replaces an earlier extension header instead of leaving that header visible. The bridge already treats the login as newer than that header (a delayed older setHeader is dropped), so in-order application must agree.
func TestQuietLoginReplacesEarlierExtensionHeaderWithHiddenSlot(t *testing.T) {
	m := &InteractiveMode{opts: InteractiveModeOptions{LoginVisible: false}, extHeader: newSpecialLinesComponent(func() {})}
	ui := &ExtUIContext{m: m}
	ui.SetHeader(extension.FrameHeader([]string{"earlier header"}, 80))
	if got := m.extHeader.Render(80); len(got) == 0 {
		t.Fatal("setup: extension header not shown")
	}
	if err := ui.SetLogin(loginHeaderDefinition("Quiet Pig")); err != nil {
		t.Fatal(err)
	}
	if got := m.extHeader.Render(80); len(got) != 0 || m.extHeader.HasContent() {
		t.Fatalf("quiet login left the earlier header visible: %q", got)
	}
}

// loginCellRE matches one painted cell: its color escapes, the glyph, and the reset.
var loginCellRE = regexp.MustCompile(`((?:\x1b\[[0-9;]*m)*)([▀▄ ])\x1b\[0m`)

var loginRGBRE = regexp.MustCompile(`\x1b\[(38|48);2;(\d+);(\d+);(\d+)m`)

// loginPoint is one pixel of the login scene: its column and its row.
type loginPoint struct{ x, y int }

// paintedPixels reads the pixels back out of the half-block lines: cell x of line n holds pixels (x, 2n) and (x, 2n+1).
// A ▀ cell paints its foreground at the top and its background (when set) at the bottom, a ▄ cell paints its foreground at
// the bottom, and a blank cell paints nothing.
func paintedPixels(t *testing.T, lines []string) map[loginPoint]color.RGBA {
	t.Helper()
	painted := map[loginPoint]color.RGBA{}
	for n, line := range lines {
		cells := loginCellRE.FindAllStringSubmatch(line, -1)
		for x, cell := range cells {
			var fg, bg *color.RGBA
			for _, m := range loginRGBRE.FindAllStringSubmatch(cell[1], -1) {
				r, _ := strconv.Atoi(m[2])
				g, _ := strconv.Atoi(m[3])
				b, _ := strconv.Atoi(m[4])
				c := color.RGBA{uint8(r), uint8(g), uint8(b), 0xFF}
				if m[1] == "38" {
					fg = &c
				} else {
					bg = &c
				}
			}
			switch {
			case cell[2] == " " && (fg != nil || bg != nil):
				t.Fatalf("a blank cell carries a color: %q", cell[0])
			case cell[2] != " " && fg == nil:
				t.Fatalf("a glyph cell has no foreground color: %q", cell[0])
			case cell[2] == "▄" && bg != nil:
				t.Fatalf("a lower half block carries a background: %q", cell[0])
			}
			switch cell[2] {
			case "▀":
				painted[loginPoint{x, 2 * n}] = *fg
				if bg != nil {
					painted[loginPoint{x, 2*n + 1}] = *bg
				}
			case "▄":
				painted[loginPoint{x, 2*n + 1}] = *fg
			}
		}
	}
	return painted
}

// The native login template paints exactly the definition's pixels where the scene puts them: the brand band at the top
// left (or nothing, and no rows, when it is transparent), the hero one pixel row below the brand and the mascot after the
// hero and its gap, each pixel in its palette color, whatever width the header takes. The name line is bold and the tagline
// italic. Widths below the full layout draw stacked text and no pixels. The uniform fixture and every built-in sprite's
// definition (art with many colors) are checked.
func TestLoginHeaderPaintsExactlyTheDefinitionPixels(t *testing.T) {
	definitions := []extension.ValidatedLoginDefinition{loginHeaderFixture(t)}
	for _, variant := range piglogin.Variants {
		definition, err := extension.ValidateLoginDefinition(piglogin.LoginDefinitionFor(variant))
		if err != nil {
			t.Fatalf("%s: %v", variant.ID, err)
		}
		definitions = append(definitions, definition)
	}
	for _, definition := range definitions {
		name := definition.Name()
		want := map[loginPoint]color.RGBA{}
		place := func(grid []string, left, top int) {
			for y, row := range grid {
				for x := range len(row) {
					if c, ok := definition.Color(row[x]); ok {
						want[loginPoint{left + x, top + y}] = c
					}
				}
			}
		}
		top := extension.LoginBrandHeight + 1
		if len(paintedOf(definition.Brand(), definition)) == 0 {
			top = 0
		}
		place(definition.Brand(), 0, 0)
		place(definition.Hero(), 0, top)
		place(definition.Mascot(), extension.LoginHeroWidth+3, top)
		artRows := (top + max(extension.LoginHeroHeight, extension.LoginMascotHeight) + 1) / 2
		for _, width := range []int{50, 66, 120} {
			lines := RenderLoginHeader(definition, width, LoginHeaderOptions{TrueColor: true})
			for i, line := range lines {
				if w := widthx.VisibleWidth(line); w > width {
					t.Errorf("%s width %d: line %d is %d cells wide", name, width, i, w)
				}
			}
			if width < 66 {
				if got := paintedPixels(t, lines); len(got) != 0 {
					t.Errorf("%s width %d: the compact header paints pixels: %v", name, width, got)
				}
				continue
			}
			if len(lines) < artRows+3 {
				t.Fatalf("%s width %d: %d lines, want %d art rows, a blank line, the name and the tagline", name, width, len(lines), artRows)
			}
			art := make([]string, artRows)
			for i, line := range lines[:artRows] {
				rest, ok := strings.CutPrefix(line, loginMargin)
				if !ok {
					t.Fatalf("%s width %d: art row %d lacks the margin: %q", name, width, i, line)
				}
				art[i] = rest
			}
			if got := paintedPixels(t, art); !maps.Equal(got, want) {
				for p, c := range want {
					if got[p] != c {
						t.Errorf("%s width %d: pixel (%d,%d) = %v, want %v", name, width, p.x, p.y, got[p], c)
						break
					}
				}
				t.Errorf("%s width %d: painted %d pixels, want the definition's %d in place", name, width, len(got), len(want))
			}
			text := lines[artRows:]
			if text[0] != "" {
				t.Errorf("%s width %d: line after the art = %q, want blank", name, width, text[0])
			}
			if wantName := loginMargin + "\x1b[1m" + definition.Name() + "\x1b[0m"; !strings.HasPrefix(text[1], wantName) {
				t.Errorf("%s width %d: name line = %q, want it to start with the bold name %q", name, width, text[1], wantName)
			}
			joined := stripANSITest(strings.Join(text, "\n"))
			for _, part := range []string{definition.Description(), definition.Tagline()} {
				if !strings.Contains(joined, part) {
					t.Errorf("%s width %d: header lacks %q", name, width, part)
				}
			}
			if !slices.ContainsFunc(text, func(line string) bool { return strings.HasPrefix(line, loginMargin+"\x1b[3m") }) {
				t.Errorf("%s width %d: no italic tagline line in %q", name, width, text)
			}
		}
	}
}

// paintedOf returns the opaque pixels of a grid.
func paintedOf(grid []string, definition extension.ValidatedLoginDefinition) []loginPoint {
	var points []loginPoint
	for y, row := range grid {
		for x := range len(row) {
			if _, ok := definition.Color(row[x]); ok {
				points = append(points, loginPoint{x, y})
			}
		}
	}
	return points
}
