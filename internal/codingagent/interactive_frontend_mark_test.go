package codingagent

import (
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/piglogin"
	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/internal/configroot"
	"github.com/MichaelKinsy/PiG/tui"
)

// headCell is one half-block cell of a pig head as HeadLines draws it: a space, or the foreground and optional
// background SGR colors and the half block, then a reset.
var headCell = regexp.MustCompile(`^(?: |\x1b\[38;(2;\d+;\d+;\d+|5;\d+)m(?:\x1b\[48;(2;\d+;\d+;\d+|5;\d+)m)?([▀▄])\x1b\[0m)`)

// sgrHex is the color an SGR color argument selects as #rrggbb: "2;r;g;b" itself, "5;n" the xterm palette color n.
func sgrHex(t *testing.T, arg string) string {
	t.Helper()
	var channels []int
	for _, part := range strings.Split(arg, ";")[1:] {
		n, err := strconv.Atoi(part)
		if err != nil {
			t.Fatal(err)
		}
		channels = append(channels, n)
	}
	if arg[0] == '2' {
		return fmt.Sprintf("#%02x%02x%02x", channels[0], channels[1], channels[2])
	}
	n := channels[0]
	switch {
	case n >= 232:
		gray := 8 + (n-232)*10
		return fmt.Sprintf("#%02x%02x%02x", gray, gray, gray)
	case n >= 16:
		levels := [6]int{0, 95, 135, 175, 215, 255}
		return fmt.Sprintf("#%02x%02x%02x", levels[(n-16)/36], levels[(n-16)%36/6], levels[(n-16)%6])
	}
	t.Fatalf("head color %q is one of the terminal's own sixteen", arg)
	return ""
}

// drawnHeadMark reads a pig head back out of the half-block lines the startup header draws (piglogin.HeadLines): each
// opaque pixel, in row-major order, in the color its SGR sequence selects.
func drawnHeadMark(t *testing.T, variant piglogin.Variant, mode tui.TerminalColorMode) frontend.Mark {
	t.Helper()
	grid := make([][]string, piglogin.HeadHeight)
	for y := range grid {
		grid[y] = make([]string, piglogin.HeadWidth)
	}
	for line, text := range piglogin.HeadLines(variant, mode) {
		for x := 0; text != ""; x++ {
			cell := headCell.FindStringSubmatch(text)
			if cell == nil {
				t.Fatalf("head line %d: unexpected %q", line, text)
			}
			text = text[len(cell[0]):]
			switch cell[3] {
			case "▀":
				grid[line*2][x] = sgrHex(t, cell[1])
				if cell[2] != "" {
					grid[line*2+1][x] = sgrHex(t, cell[2])
				}
			case "▄":
				grid[line*2+1][x] = sgrHex(t, cell[1])
			}
		}
	}
	mark := frontend.Mark{Width: piglogin.HeadWidth, Height: piglogin.HeadHeight}
	for y, row := range grid {
		for x, color := range row {
			if color != "" {
				mark.Pixels = append(mark.Pixels, frontend.MarkPixel{X: x, Y: y, Color: color})
			}
		}
	}
	return mark
}

// markTestSprite is an extension's sprite: a blue pig.
func markTestSprite(t *testing.T) extension.ValidatedSpriteDefinition {
	t.Helper()
	definition, err := extension.ValidateSpriteDefinition(extension.SpriteDefinition{
		ID: "mark-test-pig", Name: "Mark Test PiG", Tagline: "Registered by an extension.",
		Mascot: []string{
			"................", "...OOOO..OOOO...", "...OeeO..OeeO...", "..OeePPPPPPeeO..",
			".OPPPpPPPPpPPPO.", ".OPPWWPPPPWWPPO.", ".OPPWKPPPPKWPPO.", ".OPbbPssssPbbPO.",
			".OPPPsKssKsPPPO.", ".OPPPPssssPPPPO.", ".OPPPPPPPPPPPPO.", "..OPPPPPPPPPPO..",
			"...OOOOOOOOOO...", "................",
		},
		Palette: map[string]string{
			"O": "#18141E", "K": "#18141E", "W": "#FFFFFF", "P": "#5B8DEF",
			"p": "#8FB2F5", "s": "#3D6BC4", "b": "#F49AA6", "e": "#4A7BD8",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return definition
}

// The first frame carries the pig head of the sprite the startup header draws as the mark, in the colors the header
// draws it in, and a later frame only when the mark changed: an extension registering the saved sprite and
// unregistering it, /sprite picking another one, and a theme in 256-color mode, which draws the head in the xterm
// palette. An edit or a theme switch that leaves the head's colors carries none.
func TestFrontendFramesCarryTheActiveSpritesMark(t *testing.T) {
	isolatePigHome(t)
	pinHeaderTerminal(t, tui.TerminalColorModeTrueColor)
	t.Cleanup(func() { piglogin.Unregister("mark-test-ext") })
	// The saved sprite is the extension's, drawn as the default until the extension registers it.
	if err := piglogin.SaveVariant(configroot.Dir(), "mark-test-pig"); err != nil {
		t.Fatal(err)
	}
	piglogin.Refresh()
	session := &fakeFrontendSession{}
	m, _ := newFrontendProbe(t, &fakeFrontend{session: session}, "regular")
	ui := &ExtUIContext{m: m}
	vader, _ := piglogin.ByID("darth-vader")

	defaultMark := drawnHeadMark(t, piglogin.Default(), tui.TerminalColorModeTrueColor)
	if len(defaultMark.Pixels) == 0 {
		t.Fatal("the default head draws no pixel")
	}
	if got := session.frames[0].Mark; got == nil || !reflect.DeepEqual(*got, defaultMark) {
		t.Fatalf("first frame mark = %v, want the default sprite's head %v", got, defaultMark)
	}
	step := func(name string, run func(), want *frontend.Mark) {
		t.Helper()
		frames := len(session.frames)
		run()
		m.tuiInst.Render()
		var marks []*frontend.Mark
		for _, frame := range session.frames[frames:] {
			marks = append(marks, frame.Mark)
		}
		if want == nil && slices.ContainsFunc(marks, func(mark *frontend.Mark) bool { return mark != nil }) {
			t.Fatalf("after %s: frames carried marks %v, want none", name, marks)
		}
		if want != nil && (len(marks) != 1 || marks[0] == nil || !reflect.DeepEqual(*marks[0], *want)) {
			t.Fatalf("after %s: frame marks %v, want one frame with %v", name, marks, *want)
		}
	}
	step("an edit", func() { m.editor.SetText("x") }, nil)
	blue := markTestSprite(t)
	// The registered sprite's head: its grid in its palette, which sets every symbol the grid draws.
	blueMark := drawnHeadMark(t, piglogin.Variant{Sprite: blue.Mascot(), PaletteOverrides: blue.Palette()}, tui.TerminalColorModeTrueColor)
	step("registering the saved sprite", func() {
		if err := ui.RegisterSprite("mark-test-ext", blue); err != nil {
			t.Fatal(err)
		}
	}, &blueMark)
	if got := piglogin.Active().ID; got != "mark-test-pig" {
		t.Fatalf("active sprite = %q, want the registered one", got)
	}
	step("registering it again", func() {
		if err := ui.RegisterSprite("mark-test-ext", blue); err != nil {
			t.Fatal(err)
		}
	}, nil)
	step("unregistering it", func() { ui.UnregisterSprites("mark-test-ext") }, &defaultMark)
	step("/sprite darth-vader", func() {
		if err := piglogin.Activate("darth-vader"); err != nil {
			t.Fatal(err)
		}
	}, new(drawnHeadMark(t, vader, tui.TerminalColorModeTrueColor)))
	step("a light theme", func() {
		if err := m.theme().setThemeName("light", false); err != nil {
			t.Fatal(err)
		}
	}, nil)
	palette := drawnHeadMark(t, vader, tui.TerminalColorMode256)
	if reflect.DeepEqual(palette, drawnHeadMark(t, vader, tui.TerminalColorModeTrueColor)) {
		t.Fatal("darth-vader draws the same colors in 256-color mode")
	}
	step("a dark theme in 256-color mode", func() {
		tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: false})
		if err := m.theme().setThemeName("dark", false); err != nil {
			t.Fatal(err)
		}
	}, &palette)
}

// Without a frontend the ANSI renderer draws the run, and no mark is built.
func TestAnsiRunBuildsNoMark(t *testing.T) {
	isolatePigHome(t)
	pinHeaderTerminal(t, tui.TerminalColorModeTrueColor)
	m, out := newFrontendProbe(t, nil, "regular")
	m.editor.SetText("x")
	m.tuiInst.Render()
	if m.surface != nil || out.Len() == 0 || m.frontendMark.built {
		t.Fatalf("an ANSI run has surface %v, painted %d bytes and built a mark %v", m.surface, out.Len(), m.frontendMark.mark)
	}
}
