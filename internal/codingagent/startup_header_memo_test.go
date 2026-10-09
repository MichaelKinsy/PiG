package codingagent

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/piglogin"
	"github.com/MichaelKinsy/PiG/tui"
)

// headerMemoState is every input of the built-in header that a test can change.
type headerMemoState struct {
	km                    *KeybindingsManager
	expanded, showDetails bool
}

func newHeaderMemoKeybindings() *KeybindingsManager {
	km := &KeybindingsManager{definitions: appKeybindingDefinitions, ordered: appKeybindingOrder, platform: tui.HostKeybindingPlatform()}
	km.rebuild()
	return km
}

func newHeaderMemoMode(state headerMemoState) *InteractiveMode {
	m := &InteractiveMode{
		opts:        InteractiveModeOptions{LoginVisible: true},
		keybindings: state.km,
		extHeader:   newSpecialLinesComponent(nil),
	}
	m.setBuiltInHeader(state.expanded)
	m.builtInHeaderShowDetails = state.showDetails
	return m
}

// freshBuiltInHeader draws the header for state on a mode that has drawn nothing before, so no kept render can answer.
func freshBuiltInHeader(state headerMemoState, width int) ([]string, builtInHeaderLogoArea) {
	m := newHeaderMemoMode(state)
	lines := m.extHeader.Render(width)
	return lines, *m.builtInHeaderLogo.Load()
}

func registerHeaderMemoSprite(t *testing.T, body string) {
	t.Helper()
	definition, err := extension.ValidateSpriteDefinition(extension.SpriteDefinition{
		ID: "memo-pig", Name: "Memo PiG", Tagline: "Registered by a header memo test.",
		Mascot: []string{
			"................", "...OOOO..OOOO...", "...OeeO..OeeO...", "..OeePPPPPPeeO..",
			".OPPPpPPPPpPPPO.", ".OPPWWPPPPWWPPO.", ".OPPWKPPPPKWPPO.", ".OPbbPssssPbbPO.",
			".OPPPsKssKsPPPO.", ".OPPPPssssPPPPO.", ".OPPPPPPPPPPPPO.", "..OPPPPPPPPPPO..",
			"...OOOOOOOOOO...", "................",
		},
		Palette: map[string]string{
			"O": "#18141E", "K": "#18141E", "W": "#FFFFFF", "P": body,
			"p": "#8FB2F5", "s": "#3D6BC4", "b": "#F49AA6", "e": "#4A7BD8",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := piglogin.Register("header-memo-test", definition); err != nil {
		t.Fatal(err)
	}
}

// restoreHeaderMemoGlobals puts back the process-wide inputs the header memo tests change.
func restoreHeaderMemoGlobals(t *testing.T) {
	t.Helper()
	isolatePigHome(t)
	caps, theme, halfBlock := tui.GetCapabilities(), tui.ActiveTheme().Name, supportsHalfBlockMark
	t.Cleanup(func() {
		supportsHalfBlockMark = halfBlock
		piglogin.Unregister("header-memo-test")
		tui.SetCapabilities(caps)
		tui.SetTheme(theme)
		tui.RefreshActiveThemeColorMode()
		piglogin.Refresh()
	})
	tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true})
	tui.SetTheme("dark")
	supportsHalfBlockMark = func() bool { return true }
}

// Every input of the built-in header, changed once each, draws the header again: the kept lines and logo area equal a first draw with the new inputs, and differ from the lines before the change.
func TestBuiltInHeaderRedrawsWhenAnInputChanges(t *testing.T) {
	restoreHeaderMemoGlobals(t)
	state := headerMemoState{km: newHeaderMemoKeybindings()}
	m := newHeaderMemoMode(state)
	width := 120
	previous := m.extHeader.Render(width)
	if again := m.extHeader.Render(width); &again[0] != &previous[0] {
		t.Fatal("an unchanged header was drawn again instead of kept")
	}
	steps := []struct {
		name   string
		change func()
	}{
		{"width", func() { width = 96 }},
		{"theme", func() { tui.SetTheme("light") }},
		{"color mode", func() {
			tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: false})
			tui.RefreshActiveThemeColorMode()
		}},
		{"color mode back", func() {
			tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true})
			tui.RefreshActiveThemeColorMode()
		}},
		{"keybindings", func() {
			state.km.SetUserBindings(map[string][]KeyID{"app.interrupt": {"ctrl+x"}})
		}},
		{"sprite", func() {
			if err := piglogin.Activate(piglogin.Variants[1].ID); err != nil {
				t.Fatal(err)
			}
		}},
		{"registered sprite", func() {
			registerHeaderMemoSprite(t, "#5B8DEF")
			if err := piglogin.Activate("memo-pig"); err != nil {
				t.Fatal(err)
			}
		}},
		{"registered sprite replaced", func() { registerHeaderMemoSprite(t, "#D94F70") }},
		// No frame between the two calls, so the sprite ID drawn stays memo-pig: only the revision tells the new sprite.
		{"registered sprite unregistered and registered again", func() {
			piglogin.Unregister("header-memo-test")
			registerHeaderMemoSprite(t, "#3FA34D")
		}},
		{"registered sprite unregistered", func() { piglogin.Unregister("header-memo-test") }},
		{"expanded", func() { m.setAllToolsExpanded(true); state.expanded = true }},
		{"expanded back", func() { m.setAllToolsExpanded(false); state.expanded = false }},
		{"show details", func() {
			m.toolMu.Lock()
			m.builtInHeaderShowDetails = true
			m.toolMu.Unlock()
			state.showDetails = true
		}},
		{"half-block mark", func() { supportsHalfBlockMark = func() bool { return false } }},
	}
	for _, step := range steps {
		step.change()
		got := m.extHeader.Render(width)
		want, wantLogo := freshBuiltInHeader(state, width)
		if !slices.Equal(got, want) {
			t.Fatalf("%s: kept header differs from a first draw:\n got %q\nwant %q", step.name, got, want)
		}
		if logo := *m.builtInHeaderLogo.Load(); logo != wantLogo {
			t.Fatalf("%s: logo area %+v, first draw %+v", step.name, logo, wantLogo)
		}
		if slices.Equal(got, previous) {
			t.Fatalf("%s: the change left the header unchanged, so the step proves nothing", step.name)
		}
		previous = got
	}
}

// Over every combination of the inputs, a header drawn after other headers equals a first draw: keeping the lines never changes a byte.
func TestBuiltInHeaderKeptLinesMatchAFirstDraw(t *testing.T) {
	restoreHeaderMemoGlobals(t)
	km := newHeaderMemoKeybindings()
	m := newHeaderMemoMode(headerMemoState{km: km})
	for _, halfBlock := range []bool{true, false} {
		supportsHalfBlockMark = func() bool { return halfBlock }
		for _, trueColor := range []bool{true, false} {
			tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: trueColor})
			for _, theme := range []string{"dark", "light"} {
				tui.SetTheme(theme)
				tui.RefreshActiveThemeColorMode()
				for _, sprite := range piglogin.Variants[:2] {
					if err := piglogin.Activate(sprite.ID); err != nil {
						t.Fatal(err)
					}
					for _, expanded := range []bool{false, true} {
						for _, showDetails := range []bool{false, true} {
							state := headerMemoState{km: km, expanded: expanded, showDetails: showDetails}
							m.toolMu.Lock()
							m.builtInHeaderExpanded, m.builtInHeaderShowDetails = expanded, showDetails
							m.toolMu.Unlock()
							for _, width := range []int{1, 8, 30, 38, 39, 40, 60, 80, 120, 200} {
								want, wantLogo := freshBuiltInHeader(state, width)
								for range 2 {
									if got := m.extHeader.Render(width); !slices.Equal(got, want) {
										t.Fatalf("halfBlock=%v trueColor=%v theme=%s sprite=%s expanded=%v details=%v width=%d:\n got %q\nwant %q",
											halfBlock, trueColor, theme, sprite.ID, expanded, showDetails, width, got, want)
									}
									if logo := *m.builtInHeaderLogo.Load(); logo != wantLogo {
										t.Fatalf("width %d: logo area %+v, first draw %+v", width, logo, wantLogo)
									}
								}
							}
						}
					}
				}
			}
		}
	}
}
