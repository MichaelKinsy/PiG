package tui

import (
	"bytes"
	"encoding/json"
	"maps"
	"os"
	"slices"
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
)

// handoffSurface is a started surface with a transcript line and the input
// editor in the dock.
func handoffSurface(t *testing.T) (*TuiSurface, *recordingSession, *Text, *Editor) {
	t.Helper()
	text := NewText("doc")
	editor := NewEditor()
	surface, session := startedSurface(t, NewContainer(text), NewContainer(NewText("status"), NewContainer(editor), NewText("footer")))
	surface.SetHooks(SurfaceHooks{Editor: func() *Editor { return editor }})
	surface.RepaintAll()
	return surface, session, text, editor
}

// While PiG lends the terminal, a change draws no frame, even when a render
// is asked for. Resume tells the session first and then sends one frame with
// only the changes made meanwhile: an update of the changed transcript node
// and of the editor node, no full resend. A second Suspend changes nothing.
func TestTuiSurfaceSuspendHoldsFramesAndResumeSendsOnlyTheChanges(t *testing.T) {
	surface, session, text, editor := handoffSurface(t)
	if len(session.tree[frontend.RegionMain]) != 1 {
		t.Fatalf("main nodes = %#v", session.tree[frontend.RegionMain])
	}
	textID := session.tree[frontend.RegionMain][0].id
	frames := len(session.frames)
	session.calls = nil

	surface.Suspend()
	text.SetText("doc 2")
	editor.SetText("edited")
	surface.RequestRender()
	surface.Render()
	surface.Suspend()
	if len(session.frames) != frames || !slices.Equal(session.calls, []string{"suspend"}) {
		t.Fatalf("while suspended: frames %d (want %d), calls %q", len(session.frames), frames, session.calls)
	}

	surface.Resume()
	if want := []string{"suspend", "resume", "apply"}; !slices.Equal(session.calls, want) {
		t.Fatalf("calls = %q, want %q", session.calls, want)
	}
	type op struct {
		kind   frontend.OpKind
		region frontend.Region
		id     string
		index  int
	}
	var got []op
	for _, o := range session.frames[frames].Ops {
		got = append(got, op{o.Kind, o.Region, o.ID, o.Index})
	}
	want := []op{{frontend.Update, frontend.RegionMain, textID, 0}, {frontend.Update, frontend.RegionDock, "editor", 1}}
	if !slices.Equal(got, want) {
		t.Fatalf("ops after resume = %+v, want %+v", got, want)
	}
	if node := session.tree[frontend.RegionDock][1].node.(frontend.Editor); node.Text != "edited" {
		t.Fatalf("editor node = %#v", node)
	}
	assertReplayIsFresh(t, surface, session)
}

// A handoff during which nothing changed draws no frame after Resume.
func TestTuiSurfaceResumeWithoutChangesSendsNoFrame(t *testing.T) {
	surface, session, _, _ := handoffSurface(t)
	frames := len(session.frames)
	session.calls = nil
	surface.Suspend()
	surface.Resume()
	surface.Resume()
	if len(session.frames) != frames || !slices.Equal(session.calls, []string{"suspend", "resume"}) {
		t.Fatalf("frames %d (want %d), calls %q", len(session.frames), frames, session.calls)
	}
}

// Suspend before Start tells the session nothing, and Stop and Start still
// replace every node.
func TestTuiSurfaceStopAndStartStillResendAndSuspendWaitsForStart(t *testing.T) {
	session := newRecordingSession()
	surface := NewTuiSurfaceWithSize(session, 60, 20, func(err error) { t.Fatalf("apply: %v", err) })
	surface.SetLayout(NewContainer(NewText("a"), NewText("b")), nil)
	surface.Suspend()
	surface.Resume()
	if len(session.calls) != 0 {
		t.Fatalf("calls before Start = %q", session.calls)
	}
	surface.Start()
	surface.Stop()
	surface.Start()
	var kinds []frontend.OpKind
	for _, o := range session.frames[len(session.frames)-1].Ops {
		kinds = append(kinds, o.Kind)
	}
	if want := []frontend.OpKind{frontend.Remove, frontend.Remove, frontend.Insert, frontend.Insert}; !slices.Equal(kinds, want) {
		t.Fatalf("ops after Stop and Start = %q, want %q", kinds, want)
	}
}

// darkThemeTokens are the color and export tokens theme_dark.json sets.
func darkThemeTokens(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile("theme_dark.json")
	if err != nil {
		t.Fatal(err)
	}
	var file struct{ Colors, Export map[string]string }
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	tokens := slices.Concat(slices.Collect(maps.Keys(file.Colors)), slices.Collect(maps.Keys(file.Export)))
	slices.Sort(tokens)
	return tokens
}

func okhslHex(t *testing.T, h, s, l float64) string {
	t.Helper()
	color, err := NewOkhslColor(h, s, l)
	if err != nil {
		t.Fatal(err)
	}
	return ColorToHex(color)
}

// The first frame carries the theme's palette, a later frame only after the
// palette changed: a theme switch carries the new palette even with no op,
// switching back carries it again, and terminal colors that leave the
// palette as it was carry nothing.
func TestTuiSurfaceFramesCarryTheThemeWhenItChanges(t *testing.T) {
	restoreBakedThemeState(t)
	SetTerminalColors(TerminalColors{})
	SetTheme("dark")
	text := NewText("doc")
	surface, session := startedSurface(t, NewContainer(text), nil)

	first := session.frames[0].Theme
	if first == nil || first.Name != "dark" || !first.Dark {
		t.Fatalf("first frame theme = %#v", first)
	}
	if got, want := slices.Sorted(maps.Keys(first.Colors)), darkThemeTokens(t); !slices.Equal(got, want) {
		t.Fatalf("dark tokens = %q, want %q", got, want)
	}
	// theme_dark.json: accent is var violet, okhsl(295 50% 67%); cardBg is
	// okhsl(264 13% 19%).
	if got, want := first.Colors["accent"], okhslHex(t, 295, 0.50, 0.67); got != want {
		t.Fatalf("accent = %q, want %q", got, want)
	}
	if got, want := first.Colors["cardBg"], okhslHex(t, 264, 0.13, 0.19); got != want {
		t.Fatalf("cardBg = %q, want %q", got, want)
	}
	darkColors := maps.Clone(first.Colors)

	text.SetText("doc 2")
	surface.Render()
	if frame := session.frames[len(session.frames)-1]; len(frame.Ops) == 0 || frame.Theme != nil {
		t.Fatalf("frame after a text change = %#v", frame)
	}

	frames := len(session.frames)
	SetTheme("light")
	surface.Render()
	if len(session.frames) != frames+1 {
		t.Fatalf("frames after the switch = %d, want %d", len(session.frames), frames+1)
	}
	light := session.frames[frames]
	if len(light.Ops) != 0 || light.Theme == nil || light.Theme.Name != "light" || light.Theme.Dark {
		t.Fatalf("light frame = %#v", light)
	}
	// theme_light.json: accent is var violet, okhsl(295 60% 46%).
	if got, want := light.Theme.Colors["accent"], okhslHex(t, 295, 0.60, 0.46); got != want {
		t.Fatalf("light accent = %q, want %q", got, want)
	}

	SetTheme("dark")
	surface.Render()
	back := session.frames[len(session.frames)-1].Theme
	if len(session.frames) != frames+2 || back == nil || back.Name != "dark" || !maps.Equal(back.Colors, darkColors) {
		t.Fatalf("frames %d, theme after switching back = %#v", len(session.frames), back)
	}

	white := RgbColor{R: 255, G: 255, B: 255}
	SetTerminalColors(TerminalColors{Background: &white})
	surface.Render()
	if len(session.frames) != frames+2 {
		t.Fatalf("terminal colors that leave the palette drew %#v", session.frames[len(session.frames)-1])
	}
}

// A token the theme draws faint arrives mixed toward the terminal's
// background, as ColorValues resolves it, and a token at the terminal's
// default color is absent.
func TestTuiSurfaceThemePaletteMixesFaintTokensAndOmitsDefaultOnes(t *testing.T) {
	restoreBakedThemeState(t)
	black, white := RgbColor{}, RgbColor{R: 255, G: 255, B: 255}
	SetTerminalColors(TerminalColors{Foreground: &black, Background: &white})
	// The system recipe leaves text at the terminal's default; muted is
	// drawn faint here.
	foregrounds, backgrounds := splitThemeTokenValues(GenerateSystemThemeColors(SystemThemeInput{Foreground: &black, Background: &white}).Colors)
	theme, err := NewTheme(foregrounds, backgrounds, TerminalColorModeTrueColor, ThemeOptions{Name: "faint", Appearance: "light", Dim: []string{"muted"}})
	if err != nil {
		t.Fatal(err)
	}
	activeTheme.Store(theme)
	if _, ok := theme.concreteColors["muted"]; !ok || len(theme.defaultForegroundTokens) == 0 {
		t.Fatalf("theme: concrete %v, default %v; the test needs a concrete muted and a default token", theme.concreteColors, theme.defaultForegroundTokens)
	}
	_, session := startedSurface(t, NewContainer(NewText("doc")), nil)
	palette := session.frames[0].Theme
	if palette == nil || palette.Name != "faint" || palette.Dark {
		t.Fatalf("theme = %#v", palette)
	}
	for token := range theme.dimTokens {
		concrete := theme.concreteColors[token]
		mixed, err := MixColors(concrete, RgbColorValue{R: 255, G: 255, B: 255}, 0.4, ColorMixSpaceOklch)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := palette.Colors[token], ColorToHex(mixed); got != want || want == ColorToHex(concrete) {
			t.Fatalf("faint %s = %q, want %q (unmixed %q)", token, got, want, ColorToHex(concrete))
		}
	}
	for _, token := range slices.Concat(theme.defaultForegroundTokens, theme.defaultBackgroundTokens) {
		if value, ok := palette.Colors[token]; ok {
			t.Fatalf("default token %s = %q, want absent", token, value)
		}
	}
	if len(palette.Colors) != len(theme.concreteColors) {
		t.Fatalf("palette has %d colors, the theme sets %d and no export colors", len(palette.Colors), len(theme.concreteColors))
	}
}

// A token at one of the terminal's sixteen ANSI colors follows the
// terminal's own palette, so the palette leaves it out, as it does a token
// at the terminal's default color: the system theme generated without the
// terminal's colors sends no color at all. A token at a color of the
// 256-color cube or ramp keeps its xterm value.
func TestTuiSurfaceThemePaletteOmitsTheTerminalsAnsiColors(t *testing.T) {
	restoreBakedThemeState(t)
	SetTerminalColors(TerminalColors{})
	SetThemeByName(SystemThemeName)
	if accent, ok := ActiveTheme().concreteColors["accent"].(IndexedColor); !ok || accent.Index >= 16 {
		t.Fatalf("system accent = %#v, want an ANSI color", ActiveTheme().concreteColors["accent"])
	}
	_, session := startedSurface(t, NewContainer(NewText("doc")), nil)
	if palette := session.frames[0].Theme; palette == nil || palette.Name != SystemThemeName || palette.Colors == nil || len(palette.Colors) != 0 {
		t.Fatalf("system theme palette = %#v, want no colors", palette)
	}

	theme := loadThemeBody(t, `{"name":"indexed","colors":{"accent":123,"success":2}}`)
	activeTheme.Store(theme)
	_, session = startedSurface(t, NewContainer(NewText("doc")), nil)
	palette := session.frames[0].Theme
	// Index 123 is cube cell (2, 5, 5): 0x87, 0xff, 0xff.
	if got := palette.Colors["accent"]; got != "#87ffff" {
		t.Fatalf("accent = %q, want #87ffff", got)
	}
	if got, ok := palette.Colors["success"]; ok {
		t.Fatalf("success = %q, want absent", got)
	}
}

// The surface paints nothing on the terminal the session draws on, but it
// writes the terminal color query there, as the ANSI renderer writes it to
// its own, and consumes the replies, so PiG reads that terminal's colors.
func TestTuiSurfaceQueriesTheTerminalColorsOnItsTerminal(t *testing.T) {
	surface, session, text, _ := handoffSurface(t)
	var terminal bytes.Buffer
	surface.SetTerminalOut(&terminal)
	frames := len(session.frames)
	text.SetText("doc 2")
	surface.HideCursor()
	surface.Render()
	if len(session.frames) == frames || terminal.Len() != 0 {
		t.Fatalf("frames %d -> %d, terminal %q", frames, len(session.frames), terminal.String())
	}

	query := surface.QueryTerminalColors(TerminalColorQueryOptions{TimeoutMs: 60000})
	want := "\x1b]10;?\x07\x1b]11;?\x07"
	for i := range 16 {
		want += "\x1b]4;" + strconv.Itoa(i) + ";?\x07"
	}
	if want += "\x1b[c"; terminal.String() != want {
		t.Fatalf("terminal = %q, want %q", terminal.String(), want)
	}
	// Tern's replies in its light appearance, which end with its DA1 reply.
	for _, reply := range append(append([]string{"\x1b]10;rgb:3b3b/3b3b/3b3b\x07", "\x1b]11;rgb:f8f8/f8f8/f8f8\x07"}, paletteReplies...), "\x1b[?62;52;c") {
		if !surface.ConsumeTerminalColorResponse(reply) {
			t.Fatalf("reply %q was not consumed", reply)
		}
	}
	result := <-query
	if result.Err != nil || result.Colors.Background == nil || *result.Colors.Background != (RgbColor{248, 248, 248}) || *result.Colors.Foreground != (RgbColor{59, 59, 59}) || len(result.Colors.Palette) != 16 {
		t.Fatalf("result = %+v", result)
	}
}
