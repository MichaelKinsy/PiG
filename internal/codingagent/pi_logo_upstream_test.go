package codingagent

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/piglogin"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Upstream draws the pi logo (pi-logo.ts, 4 cells by 2 lines) at the start of the header's first two lines, the version
// beside its first line and the first line of key hints beside its second (interactive-mode.ts:998-1006). D2 draws PiG's
// pig head there instead, piglogin.HeadCells cells by piglogin.HeadRows lines, with the header's next lines beside its
// other lines, or the one-line text mark in the logo's slot where the head cannot be drawn. The heads are asserted against
// the goldens in coding/piglogin/testdata.

func newHeaderMode(t *testing.T) *InteractiveMode {
	t.Helper()
	isolatePigHome(t)
	// The head is drawn only in truecolor; a test that asserts another color level or Apple Terminal pins it after this.
	pinHeaderTerminal(t, tui.TerminalColorModeTrueColor)
	km := &KeybindingsManager{definitions: appKeybindingDefinitions, ordered: appKeybindingOrder, platform: tui.HostKeybindingPlatform()}
	km.rebuild()
	m := &InteractiveMode{
		opts:        InteractiveOptions{LoginVisible: true},
		keybindings: km,
		extHeader:   newSpecialLinesComponent(nil),
		tuiInst:     tui.NewWithOutput(io.Discard, 100, 40),
	}
	m.restoreBuiltInHeader()
	return m
}

// pinHeaderTerminal activates the dark theme in mode for the test, whatever color level the terminal that runs it reports
// (CI sets no COLORTERM, so the detected level there is 256 colors), and draws half blocks as every terminal but Apple
// Terminal does (supportsHalfBlockMark), so the header does not depend on the terminal that runs the test. It restores the
// capabilities, the theme and the half-block check afterwards; a test of Apple Terminal stubs the check after it.
func pinHeaderTerminal(t *testing.T, mode tui.TerminalColorMode) {
	t.Helper()
	previousTheme, previousCaps, previousHalfBlocks := tui.ActiveTheme(), tui.GetCapabilities(), supportsHalfBlockMark
	supportsHalfBlockMark = func() bool { return true }
	t.Cleanup(func() {
		supportsHalfBlockMark = previousHalfBlocks
		// Rebuild the previous theme in its own color mode, which can differ from the capabilities' (the lazily created
		// default theme is truecolor whatever the terminal reports), so later tests see the theme they would have seen.
		tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: previousTheme.ColorMode() == tui.TerminalColorModeTrueColor})
		tui.SetThemeByName(previousTheme.Name)
		tui.SetCapabilities(previousCaps)
	})
	tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: mode == tui.TerminalColorModeTrueColor})
	tui.SetTheme("dark")
	if got := tui.ActiveTheme().ColorMode(); got != mode {
		t.Fatalf("theme color mode = %s, want %s", got, mode)
	}
}

// pinHeaderTerminal leaves the theme in the color mode it had, not the one the restored capabilities imply: the lazily
// created default theme is truecolor in a 256-color terminal (CI), and the tests after a header test must still see it.
func TestPinHeaderTerminalRestoresTheThemeInItsMode(t *testing.T) {
	pinHeaderTerminal(t, tui.TerminalColorModeTrueColor)
	tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: false})
	t.Run("pinned", func(t *testing.T) { pinHeaderTerminal(t, tui.TerminalColorMode256) })
	if got := tui.ActiveTheme().ColorMode(); got != tui.TerminalColorModeTrueColor {
		t.Errorf("theme color mode after the pin = %s, want truecolor", got)
	}
	if tui.GetCapabilities().TrueColor {
		t.Error("capabilities after the pin report truecolor, want the 256 colors they had")
	}
}

// isolatePigHome keeps the header from reading the sprite the developer chose in the real $PIG_HOME.
func isolatePigHome(t *testing.T) {
	t.Helper()
	t.Setenv("PIG_HOME", t.TempDir())
	piglogin.Refresh()
}

// headGolden is a sprite's pig head as coding/piglogin/testdata/head-<id>.golden pins it, read from the file, not from the
// renderer under test.
func headGolden(t *testing.T, id string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "coding", "piglogin", "testdata", "head-"+id+".golden"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != piglogin.HeadRows {
		t.Fatalf("head golden %s has %d lines", id, len(lines))
	}
	return lines
}

func textMark(t *testing.T, variant piglogin.Variant, mode tui.TerminalColorMode) string {
	t.Helper()
	mark := piglogin.TextMark(variant, mode)
	if widthx.VisibleWidth(mark) != piglogin.TextMarkWidth || stripANSITest(mark) != "PiG." {
		t.Fatalf("TextMark(%q, %s) = %q", variant.ID, mode, mark)
	}
	return mark
}

func headerLines(m *InteractiveMode, width int) (raw, plain []string) {
	raw = m.extHeader.Render(width)
	return raw, strings.Split(stripANSITest(strings.Join(raw, "\n")), "\n")
}

// assertHead checks that the header's first lines start with the head, one line each, after the padded text's 1-cell left
// margin.
// A head line may follow the style a wrapped line beside the head carried onto it, as Pi's Text carries it onto the line
// that starts with its logo; the style is overridden by the head's own colors.
func assertHead(t *testing.T, label string, raw, head []string) {
	t.Helper()
	if len(raw) < len(head) {
		t.Fatalf("%s: header has %d lines, want the %d head lines first", label, len(raw), len(head))
	}
	startsWithHead := func(line, head string) bool {
		rest, ok := strings.CutPrefix(line, " ")
		for ok {
			if strings.HasPrefix(rest, head) {
				return true
			}
			loc := carriedStyle.FindStringIndex(rest)
			ok = loc != nil
			if ok {
				rest = rest[loc[1]:]
			}
		}
		return false
	}
	for i, line := range head {
		if !startsWithHead(raw[i], line) {
			t.Errorf("%s: line %d =\n%q\nwant the head line\n%q", label, i, raw[i], " "+line)
		}
	}
}

var carriedStyle = regexp.MustCompile(`^\x1b\[[0-9;]*m`)

func hasHalfBlocks(lines []string) bool {
	return strings.ContainsAny(strings.Join(lines, ""), "▀▄")
}

// piLogoSignatures are the byte sequences of Pi's logo: its three brand colors and its glyphs.
var piLogoSignatures = []string{"38;2;228;138;122", "48;2;79;142;179", "38;2;234;182;93", "▀▀█", "█▀"}

func assertNoPiLogo(t *testing.T, label string, raw []string) {
	t.Helper()
	joined := strings.Join(raw, "\n")
	for _, signature := range piLogoSignatures {
		if strings.Contains(joined, signature) {
			t.Errorf("%s: the header contains Pi's logo (%q)", label, signature)
		}
	}
}

// pi-logo.ts:36-39 (Pi 1.0.0): the text fallback for the logo is "Pi" with the logo's coral and yellow, in the terminal's color mode. Upstream has no test for it; these cases assert the sequence the source defines.
// pig divergence (D2): the text fallback is the bold "PiG." text mark of the active sprite, not Pi's coral and yellow "Pi".
func TestPiWordmarkUpstream(t *testing.T) {
	isolatePigHome(t)
	for _, mode := range []tui.TerminalColorMode{tui.TerminalColorModeTrueColor, tui.TerminalColorMode256} {
		got := piWordmark(mode)
		if want := textMark(t, piglogin.Default(), mode); got != want {
			t.Errorf("mode %v: piWordmark = %q, want %q", mode, got, want)
		}
		assertNoPiLogo(t, "wordmark", []string{got})
	}
	if got := piWordmark(tui.TerminalColorMode256); strings.Contains(got, "38;2;") {
		t.Errorf("256-color piWordmark contains a truecolor sequence: %q", got)
	}
}

// interactive-mode.ts:1044-1048 (Pi 1.0.0): with quietStartup "header" the compact onboarding line drops " and loaded resources", because the loaded resources stay hidden. Upstream has no test for it; the case asserts the text the source defines.
func TestBuiltInHeaderOnboardingWithHeaderOnlyQuietStartupUpstream(t *testing.T) {
	isolatePigHome(t)
	pinHeaderTerminal(t, tui.TerminalColorModeTrueColor)
	km := &KeybindingsManager{definitions: appKeybindingDefinitions, ordered: appKeybindingOrder, platform: tui.HostKeybindingPlatform()}
	km.rebuild()
	for _, tc := range []struct {
		quietStartup any
		want         string
	}{
		{quietStartup: false, want: "Press ctrl+o to show full startup help and loaded resources."},
		{quietStartup: "header", want: "Press ctrl+o to show full startup help."},
	} {
		m := &InteractiveMode{
			opts:        InteractiveOptions{LoginVisible: true},
			keybindings: km,
			extHeader:   newSpecialLinesComponent(nil),
			tuiInst:     tui.NewWithOutput(io.Discard, 100, 40),
		}
		setUpstreamQuietStartup(t, m, tc.quietStartup)
		m.restoreBuiltInHeader()
		lines := strings.Split(stripANSITest(strings.Join(m.extHeader.Render(100), "\n")), "\n")
		// The onboarding line is the header's third, beside the pig head (D2).
		if len(lines) < 3 || !strings.Contains(lines[2], "Press") {
			t.Fatalf("quietStartup %v: header = %q", tc.quietStartup, lines)
		}
		if got := strings.TrimSpace(lines[2][strings.Index(lines[2], "Press"):]); got != tc.want {
			t.Errorf("quietStartup %v: onboarding line = %q, want %q", tc.quietStartup, got, tc.want)
		}
	}
}

const compactHints = "escape interrupt · ctrl+c/ctrl+d clear/exit · / commands · ! bash · ctrl+o more"

const pressLine = "Press ctrl+o to show full startup help and loaded resources."

const onboardingLine = "PiG can explain its own features and look up its docs. Ask it how to use or extend PiG."

// D2: the pig head takes Pi's logo slot. Its first line carries the version and its other lines the header's next lines,
// each wrapped in the cells right of the head, as Pi's logo carries the version and the first hint line; the lines after the
// head are Pi's, at the padded text's left margin. Where nothing wraps the expanded header has Pi's 22 lines; the compact
// header is the head's 7 lines, 2 more than Pi's 5.
func TestBuiltInHeaderDrawsThePigHeadInPisLogoSlot(t *testing.T) {
	m := newHeaderMode(t)
	head := headGolden(t, piglogin.DefaultID)
	for _, tc := range []struct {
		expanded bool
		want     []string
	}{
		{false, []string{"v" + pigversion.Version, compactHints, pressLine, "", onboardingLine, "", ""}},
		{true, append(append([]string{"v" + pigversion.Version}, expandedHints...), "", onboardingLine)},
	} {
		m.setAllToolsExpanded(tc.expanded)
		raw, lines := headerLines(m, 120)
		label := fmt.Sprintf("expanded=%v", tc.expanded)
		assertHead(t, label, raw, head)
		if len(lines) != len(tc.want) {
			t.Fatalf("%s: header has %d lines, want %d: %q", label, len(lines), len(tc.want), lines)
		}
		for i, want := range tc.want {
			prefix := " "
			if i < piglogin.HeadRows {
				prefix = " " + stripANSITest(head[i]) + " "
			}
			got := strings.TrimRight(lines[i], " ")
			if want = strings.TrimRight(prefix+want, " "); got != want {
				t.Errorf("%s: line %d = %q, want %q", label, i, got, want)
			}
		}
		for i, line := range lines {
			if widthx.VisibleWidth(line) != 120 {
				t.Errorf("%s: line %d is not padded to the width: %q", label, i, line)
			}
		}
		assertNoPiLogo(t, label, raw)
	}
}

// A line that starts beside the head wraps in the cells right of it, and its continuation stays at that column while head
// lines remain and after them; a head line with no text left is drawn alone; the lines after the head wrap in the full
// padded width, as Pi's do. Every line is padded to the width.
func TestHeadBesideTextLayout(t *testing.T) {
	head := []string{strings.Repeat("A", piglogin.HeadCells), strings.Repeat("B", piglogin.HeadCells), strings.Repeat("C", piglogin.HeadCells), strings.Repeat("D", piglogin.HeadCells)}
	a, b, c, d := " "+head[0]+" ", " "+head[1]+" ", " "+head[2]+" ", " "+head[3]
	text := "v1\none two three four five six seven\nshort\n\nafter the head it wraps in the full width"
	for _, tc := range []struct {
		width int
		want  []string
	}{
		// 21 cells beside the head, 38 after it.
		{40, []string{
			a + "v1",
			b + "one two three four",
			c + "five six seven",
			d + " short",
			"",
			" after the head it wraps in the full",
			" width",
		}},
		// 12 cells beside the head, 29 after it: the line that runs out of head lines keeps its column.
		{31, []string{
			a + "v1",
			b + "one two",
			c + "three four",
			d + " five six",
			strings.Repeat(" ", piglogin.HeadCells+2) + "seven",
			" short",
			"",
			" after the head it wraps in",
			" the full width",
		}},
		{120, []string{
			a + "v1",
			b + "one two three four five six seven",
			c + "short",
			d,
			" after the head it wraps in the full width",
		}},
	} {
		got := headBesideText(head, text, tc.width)
		if len(got) != len(tc.want) {
			t.Fatalf("width %d: %d lines, want %d:\n%s", tc.width, len(got), len(tc.want), strings.Join(got, "\n"))
		}
		for i, want := range tc.want {
			if strings.TrimRight(got[i], " ") != want {
				t.Errorf("width %d line %d = %q, want %q", tc.width, i, got[i], want)
			}
			if w := widthx.VisibleWidth(got[i]); w != tc.width {
				t.Errorf("width %d line %d is %d cells", tc.width, i, w)
			}
		}
	}
	short := headBesideText(head, "v1", 40)
	if len(short) != len(head) || strings.TrimRight(short[3], " ") != d {
		t.Errorf("a head taller than the text = %q, want every head line", short)
	}
}

// At 80 columns the compact hints and the onboarding line wrap beside the head, and the header is the head's 7 lines.
func TestBuiltInHeaderAt80Columns(t *testing.T) {
	m := newHeaderMode(t)
	raw, lines := headerLines(m, 80)
	assertHead(t, "80", raw, headGolden(t, piglogin.DefaultID))
	beside := make([]string, len(lines))
	for i, line := range lines {
		beside[i] = strings.TrimSpace(string([]rune(line)[min(len([]rune(line)), piglogin.HeadCells+2):]))
	}
	if len(lines) != piglogin.HeadRows || beside[0] != "v"+pigversion.Version ||
		strings.Join(beside[1:3], " ") != compactHints || beside[3] != pressLine || beside[4] != "" ||
		strings.Join(beside[5:], " ") != onboardingLine {
		t.Fatalf("80-column header = %q", lines)
	}
}

// PiG Standard's goldens (login-pig-default-{50,66,120}.golden) pin the native login template itself: the same bytes the
// original test's mirror renderer produced.
func TestNativeLoginTemplateMatchesTheOriginalGoldens(t *testing.T) {
	definition, err := extension.ValidateLoginDefinition(piglogin.LoginDefinitionFor(piglogin.Default()))
	if err != nil {
		t.Fatal(err)
	}
	for _, width := range []int{50, 66, 120} {
		want, err := os.ReadFile(filepath.Join("..", "..", "coding", "piglogin", "testdata", fmt.Sprintf("login-pig-default-%d.golden", width)))
		if err != nil {
			t.Fatal(err)
		}
		got := strings.Join(RenderLoginHeader(definition, width, LoginHeaderOptions{TrueColor: true}), "\n") + "\n"
		if got != string(want) {
			t.Errorf("width %d: native login header differs from the golden\ngot:\n%q\nwant:\n%q", width, got, want)
		}
	}
}

// The head is drawn while the version fits beside it in the padded width: the head, a space and the version. Below that the
// text mark takes Pi's logo slot.
func TestBuiltInHeaderUsesTheHeadAsSoonAsTheVersionFits(t *testing.T) {
	m := newHeaderMode(t)
	fits := 2 + piglogin.HeadCells + 1 + widthx.VisibleWidth("v"+pigversion.Version)
	raw, _ := headerLines(m, fits)
	assertHead(t, fmt.Sprintf("width %d", fits), raw, headGolden(t, piglogin.DefaultID))
	raw, lines := headerLines(m, fits-1)
	if hasHalfBlocks(raw) || !strings.HasPrefix(lines[0], " PiG. v") {
		t.Errorf("width %d = %q, want the text mark", fits-1, lines[:2])
	}
}

func TestBuiltInHeaderFallsBackToTheTextMarkWhenNarrow(t *testing.T) {
	m := newHeaderMode(t)
	mark := textMark(t, piglogin.Default(), tui.TerminalColorModeTrueColor)
	for _, width := range []int{26, 24, 20} {
		raw, lines := headerLines(m, width)
		if got, want := strings.TrimRight(lines[0], " "), " PiG. v"+pigversion.Version; got != want {
			t.Errorf("width %d first line = %q, want %q", width, got, want)
		}
		// Pi's layout: a 4-cell logo, a space and the hints in the width-2 cells of the padded text.
		wrapped := widthx.WrapTextWithAnsi("xxxx "+compactHints, width-2)
		wantSecond := " " + strings.Replace(wrapped[0], "xxxx", "    ", 1)
		if got := strings.TrimRight(lines[1], " "); got != wantSecond {
			t.Errorf("width %d second line = %q, want %q", width, got, wantSecond)
		}
		if !strings.Contains(raw[0], mark) || hasHalfBlocks(raw) {
			t.Errorf("width %d: first line does not carry the text mark alone: %q", width, raw[0])
		}
		assertNoPiLogo(t, "width", raw)
	}
}

// The head needs truecolor; a 256-color terminal gets the text mark in that terminal's color mode.
func TestBuiltInHeaderWithoutTruecolorUsesTheTextMark(t *testing.T) {
	m := newHeaderMode(t)
	pinHeaderTerminal(t, tui.TerminalColorMode256)
	raw, lines := headerLines(m, 100)
	if hasHalfBlocks(raw) || !strings.Contains(raw[0], textMark(t, piglogin.Default(), tui.TerminalColorMode256)) {
		t.Errorf("256-color header = %q, want the text mark", raw[:2])
	}
	if strings.Contains(raw[0]+raw[1], "38;2;") {
		t.Errorf("256-color header contains a truecolor sequence: %q %q", raw[0], raw[1])
	}
	if got := strings.TrimRight(lines[1], " "); got != "      "+compactHints {
		t.Errorf("256-color second line = %q", got)
	}
	pinHeaderTerminal(t, tui.TerminalColorModeTrueColor)
	raw, _ = headerLines(m, 100)
	assertHead(t, "truecolor", raw, headGolden(t, piglogin.DefaultID))
}

func TestBuiltInHeaderShowsTheSelectedSprite(t *testing.T) {
	m := newHeaderMode(t)
	if err := piglogin.Activate("sheriff"); err != nil {
		t.Fatal(err)
	}
	raw, _ := headerLines(m, 100)
	head := headGolden(t, "sheriff")
	assertHead(t, "sheriff", raw, head)
	if hat := "38;2;107;76;58m"; !strings.Contains(strings.Join(raw[:piglogin.HeadRows], ""), hat) {
		t.Errorf("the sheriff's head has no hat color")
	}
	if strings.Join(head, "") == strings.Join(headGolden(t, piglogin.DefaultID), "") {
		t.Error("the sheriff draws the default head")
	}
}

// /reload restores the built-in header (interactive_commands.go), which reads the saved selection again.
func TestReloadRendersThePiGHeaderAgain(t *testing.T) {
	m := newHeaderMode(t)
	root := piglogin.ConfigHome()
	if err := piglogin.SaveVariant(root, "cloud"); err != nil {
		t.Fatal(err)
	}
	m.restoreBuiltInHeader()
	raw, _ := headerLines(m, 100)
	assertHead(t, "reload", raw, headGolden(t, "cloud"))
	assertNoPiLogo(t, "reload", raw)
}

// Pi's extension API: setHeader replaces the built-in header whatever it draws, and setHeader(undefined) restores it.
func TestAnotherExtensionStillReplacesTheHeader(t *testing.T) {
	m := newHeaderMode(t)
	u := &ExtUIContext{m: m}
	u.SetHeader([]string{"a custom header"})
	raw, plain := headerLines(m, 100)
	if len(plain) != 1 || plain[0] != "a custom header" {
		t.Fatalf("header after setHeader = %q", plain)
	}
	if hasHalfBlocks(raw) || strings.Contains(strings.Join(plain, ""), "escape") {
		t.Errorf("the replacement still draws the built-in header: %q", plain)
	}
	u.SetHeader(nil)
	raw, plain = headerLines(m, 100)
	assertHead(t, "restore", raw, headGolden(t, piglogin.DefaultID))
	if !strings.Contains(strings.Join(plain, "\n"), compactHints) {
		t.Errorf("setHeader(nil) restored %q, want the PiG header", plain)
	}
	assertNoPiLogo(t, "restore", raw)
}

// The sign-in header stays replaceable: a Piglet's setLogin (D60) puts its own login in the header slot instead of the
// built-in head, and setHeader(undefined) brings the built-in head back.
func TestPigletSetLoginReplacesTheBuiltInArt(t *testing.T) {
	m := newHeaderMode(t)
	m.opts.LoginHeaderOptions = LoginHeaderOptions{TrueColor: true, OperationalLines: []string{"operational line"}}
	u := &ExtUIContext{m: m}
	brand := make([]string, extension.LoginBrandHeight)
	for i := range brand {
		brand[i] = strings.Repeat("b", extension.LoginBrandWidth)
	}
	hero := make([]string, extension.LoginHeroHeight)
	for i := range hero {
		hero[i] = strings.Repeat("h", extension.LoginHeroWidth)
	}
	mascot := make([]string, extension.LoginMascotHeight)
	for i := range mascot {
		mascot[i] = strings.Repeat("m", extension.LoginMascotWidth)
	}
	piglet := extension.LoginDefinition{
		Brand: brand, Hero: hero, Mascot: mascot,
		Palette: map[string]string{"b": "#010203", "h": "#040506", "m": "#070809"},
		Name:    "Piglet", Description: "a Piglet's own login", Tagline: "not the built-in art",
	}
	if err := u.SetLogin(piglet); err != nil {
		t.Fatal(err)
	}
	validated, err := extension.ValidateLoginDefinition(piglet)
	if err != nil {
		t.Fatal(err)
	}
	raw, plain := headerLines(m, 100)
	if want := RenderLoginHeader(validated, 100, m.opts.LoginHeaderOptions); strings.Join(raw, "\n") != strings.Join(want, "\n") {
		t.Fatalf("header after setLogin =\n%q\nwant the Piglet's login\n%q", raw, want)
	}
	joined := strings.Join(raw, "\n")
	if !strings.Contains(joined, "38;2;1;2;3") || !strings.Contains(strings.Join(plain, "\n"), "Piglet") {
		t.Errorf("the Piglet's brand or name is missing: %q", plain)
	}
	for _, line := range headGolden(t, piglogin.DefaultID) {
		if strings.Contains(joined, line) {
			t.Fatalf("the Piglet's login still draws the built-in head line %q", line)
		}
	}
	if strings.Contains(strings.Join(plain, "\n"), "v"+pigversion.Version) {
		t.Errorf("the Piglet's login still carries the built-in version line: %q", plain)
	}
	// The selected sprite does not leak through the Piglet's login on a later render.
	if err := piglogin.Activate("sheriff"); err != nil {
		t.Fatal(err)
	}
	if again, _ := headerLines(m, 100); strings.Join(again, "\n") != joined {
		t.Error("the Piglet's login changed when the built-in sprite changed")
	}
	u.SetHeader(nil)
	raw, _ = headerLines(m, 100)
	assertHead(t, "restored after setLogin", raw, headGolden(t, "sheriff"))
}

func TestQuietStartupDrawsNoHeader(t *testing.T) {
	m := newHeaderMode(t)
	m.opts.LoginVisible = false
	m.restoreBuiltInHeader()
	if got := m.extHeader.Render(100); len(got) != 0 {
		t.Fatalf("quiet startup header = %q", got)
	}
	(&ExtUIContext{m: m}).SetHeader(nil)
	if got := m.extHeader.Render(100); len(got) != 0 {
		t.Fatalf("quiet startup header after setHeader(nil) = %q", got)
	}
}

func TestNarrowestTerminalsStillRenderTheHeader(t *testing.T) {
	m := newHeaderMode(t)
	for _, width := range []int{1, 2, 5, 6, 10, 20} {
		raw, _ := headerLines(m, width)
		if len(raw) == 0 {
			t.Errorf("width %d rendered no header", width)
		}
		assertNoPiLogo(t, "narrow", raw)
	}
}

// Apple Terminal misaligns half blocks, and Pi draws its "Pi" wordmark there instead of its logo (pi-logo.ts
// supportsPiLogo, interactive-mode.ts withLogo); PiG draws the text mark in the wordmark's place, with the hints on the next
// line at the margin.
func TestBuiltInHeaderUsesTheTextMarkInAppleTerminal(t *testing.T) {
	m := newHeaderMode(t)
	previous := supportsHalfBlockMark
	supportsHalfBlockMark = func() bool { return false }
	t.Cleanup(func() { supportsHalfBlockMark = previous })
	for _, width := range []int{120, 100, 80} {
		raw, lines := headerLines(m, width)
		if got, want := strings.TrimRight(lines[0], " "), " PiG. v"+pigversion.Version; got != want {
			t.Errorf("width %d first line = %q, want %q", width, got, want)
		}
		// interactive-mode.ts withLogo without the logo: `${piWordmark()} v${version}\n${hints}`.
		wrapped := widthx.WrapTextWithAnsi(compactHints, width-2)
		if got, want := strings.TrimRight(lines[1], " "), " "+wrapped[0]; got != want {
			t.Errorf("width %d second line = %q, want %q", width, got, want)
		}
		if hasHalfBlocks(raw) {
			t.Errorf("width %d: the Apple Terminal header draws half blocks: %q", width, lines[:2])
		}
		assertNoPiLogo(t, "apple terminal", raw)
	}
}

// Escape in the /sprite picker dismisses it through the interactive host's dialog (context.Canceled), which the built-in
// pig-login command must treat as no choice rather than report as an extension error.
func TestSpritePickerEscapeIsNoErrorThroughTheInteractiveHost(t *testing.T) {
	isolatePigHome(t)
	m, _ := newExtensionDialogProbe(t)
	ext, err := piglogin.Extension()
	if err != nil {
		t.Fatal(err)
	}
	c := extension.NewContext(t.TempDir(), &ExtUIContext{m: m}, func() error { return nil }, extension.ContextActions{})
	_, err = runExtensionDialogProbe(t, m, func() (string, error) {
		return "", ext.Commands["sprite"].Handler(extension.WithContext(context.Background(), c), "")
	}, []string{"\x1b"})
	if err != nil {
		t.Fatalf("/sprite then escape = %v, want no error", err)
	}
	if got := piglogin.Active(); got.ID != piglogin.DefaultID {
		t.Fatalf("escape changed the sprite to %q", got.ID)
	}
}

// /sprite set repaints the header with the chosen sprite at once, not at the next keystroke.
func TestSpriteSetRepaintsTheHeaderThroughTheInteractiveHost(t *testing.T) {
	m := newHeaderMode(t)
	repaints := 0
	m.extHeader = newSpecialLinesComponent(func() { repaints++ })
	m.restoreBuiltInHeader()
	repaints = 0
	ext, err := piglogin.Extension()
	if err != nil {
		t.Fatal(err)
	}
	c := extension.NewContext(t.TempDir(), &ExtUIContext{m: m}, func() error { return nil }, extension.ContextActions{})
	if err := ext.Commands["sprite"].Handler(extension.WithContext(context.Background(), c), "set sheriff"); err != nil {
		t.Fatal(err)
	}
	if repaints == 0 {
		t.Fatal("/sprite set requested no repaint of the header")
	}
	raw, _ := headerLines(m, 100)
	assertHead(t, "/sprite set sheriff", raw, headGolden(t, "sheriff"))
}

// expandedHints are Pi's expanded startup hints (interactive-mode.ts expandedInstructions) with the default Linux
// keybindings.
var expandedHints = []string{
	"escape to interrupt", "ctrl+c to clear", "ctrl+c twice to exit", "ctrl+d to exit (empty)", "ctrl+z to suspend",
	"ctrl+k to delete to end", "shift+tab to cycle thinking level", "ctrl+p/shift+ctrl+p to cycle models",
	"ctrl+l to select model", "ctrl+o to expand tools", "ctrl+t to expand thinking", "ctrl+g for external editor",
	"/ for commands", "! to run bash", "!! to run bash (no context)", "alt+enter to queue follow-up",
	"alt+up to edit all queued messages", "ctrl+v to paste files on macOS, images, or text", "drop files to attach",
}

func init() {
	// Pi's Windows defaults differ: ctrl+z is unbound, cycle-back is alt+p, follow-up is ctrl+q, dequeue is alt+q, paste is alt+v.
	if runtime.GOOS == "darwin" {
		for i, hint := range expandedHints {
			expandedHints[i] = strings.NewReplacer("alt+enter", "option+enter", "alt+up", "option+up").Replace(hint)
		}
		return
	}
	if runtime.GOOS != "windows" {
		return
	}
	for i, hint := range expandedHints {
		switch hint {
		case "ctrl+z to suspend":
			expandedHints[i] = " to suspend"
		case "ctrl+p/shift+ctrl+p to cycle models":
			expandedHints[i] = "ctrl+p/alt+p to cycle models"
		case "alt+enter to queue follow-up":
			expandedHints[i] = "ctrl+q to queue follow-up"
		case "alt+up to edit all queued messages":
			expandedHints[i] = "alt+q to edit all queued messages"
		case "ctrl+v to paste files on macOS, images, or text":
			expandedHints[i] = "alt+v to paste files on macOS, images, or text"
		}
	}
}

// /sprite preview's art (piglogin.ArtLines) is the native login template's art for the sprite's login definition: the same
// half blocks in the same places, so a Piglet that sets that definition draws the same picture.
func TestSpritePreviewArtIsTheNativeLoginArt(t *testing.T) {
	isolatePigHome(t)
	for _, variant := range piglogin.Variants {
		definition, err := extension.ValidateLoginDefinition(piglogin.LoginDefinitionFor(variant))
		if err != nil {
			t.Fatal(err)
		}
		login := RenderLoginHeader(definition, 120, LoginHeaderOptions{TrueColor: true})
		art := piglogin.ArtLines(variant, tui.TerminalColorModeTrueColor)
		for i, line := range art {
			want := strings.TrimRight(stripANSITest(strings.TrimPrefix(login[i], loginMargin)), " ")
			if got := strings.TrimRight(stripANSITest(line), " "); got != want {
				t.Errorf("%s art line %d = %q, want the login template's %q", variant.ID, i, got, want)
			}
		}
	}
}

// D87: the click area is the drawn pig. For every sprite, every half block the header draws on the head's lines lies in
// the recorded logo area (the 16 cells after the padding on the first 7 lines), the text beside it starts after the area
// and its separating cell, and the area's corners hold the pig's outermost pixels.
func TestHeaderLogoAreaIsTheDrawnPig(t *testing.T) {
	m := newHeaderMode(t)
	for _, variant := range piglogin.Variants {
		if err := piglogin.Activate(variant.ID); err != nil {
			t.Fatal(err)
		}
		_, lines := headerLines(m, 100)
		area := m.builtInHeaderLogo.Load()
		if area == nil || !area.visible || area.column != 1 || area.row != 0 || area.columns != piglogin.HeadCells || area.rows != piglogin.HeadRows {
			t.Fatalf("%s: logo area = %+v, want the %dx%d head at column 1", variant.ID, area, piglogin.HeadCells, piglogin.HeadRows)
		}
		minX, maxX, maxY := len(lines[0]), -1, -1
		for y, line := range lines {
			for x, r := range []rune(line) {
				if r != '▀' && r != '▄' {
					continue
				}
				if y >= area.row+area.rows || x < area.column || x >= area.column+area.columns {
					t.Fatalf("%s: half block at (%d, %d) outside the logo area %+v", variant.ID, x, y, area)
				}
				minX, maxX, maxY = min(minX, x), max(maxX, x), max(maxY, y)
			}
		}
		if maxY != area.rows-1 || maxX-minX+1 > area.columns {
			t.Fatalf("%s: the pig spans columns %d-%d to line %d, area %+v", variant.ID, minX, maxX, maxY, area)
		}
		if got := []rune(lines[0])[area.column+area.columns+1]; got != 'v' {
			t.Fatalf("%s: the version starts with %q after the area, want 'v'", variant.ID, got)
		}
	}
}

// macOS Terminal.app reports TERM=xterm-256color and TERM_PROGRAM=Apple_Terminal: no truecolor and misaligned half blocks. The
// header then draws the text mark in the 256-color fallback, its letters colored from the sprite's ramp as Pi colors "Pi".
func TestBuiltInHeaderColorsTheTextMarkInAppleTerminal256(t *testing.T) {
	m := newHeaderMode(t)
	pinHeaderTerminal(t, tui.TerminalColorMode256)
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("TERM_PROGRAM", "Apple_Terminal")
	previous := supportsHalfBlockMark
	supportsHalfBlockMark = func() bool { return !tui.IsAppleTerminalSession() }
	t.Cleanup(func() { supportsHalfBlockMark = previous })
	raw, lines := headerLines(m, 100)
	if !strings.Contains(raw[0], textMark(t, piglogin.Default(), tui.TerminalColorMode256)) {
		t.Fatalf("Apple Terminal header = %q, want the 256-color text mark", raw[0])
	}
	if strings.Contains(raw[0], "38;2;") || hasHalfBlocks(raw) {
		t.Errorf("Apple Terminal header has a truecolor sequence or half blocks: %q", raw[0])
	}
	if strings.Count(raw[0], "38;5;") < 4 {
		t.Errorf("Apple Terminal text mark does not color P, i, G and the period: %q", raw[0])
	}
	if got := strings.TrimRight(lines[0], " "); got != " PiG. v"+pigversion.Version {
		t.Errorf("first line = %q", got)
	}
}
