package codingagent

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/piglogin"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/tui"
)

func firstTimeSetupText(c *FirstTimeSetupComponent) string {
	return string(tools.StripANSI([]byte(strings.Join(c.Render(100), "\n"))))
}

func firstTimeSetupExtensionSprite(t *testing.T) {
	t.Helper()
	definition, err := extension.ValidateSpriteDefinition(extension.SpriteDefinition{
		ID: "blue-pig", Name: "Blue PiG", Tagline: "Registered by an extension.",
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
	if err := piglogin.Register("first-time-setup-test", definition); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { piglogin.Unregister("first-time-setup-test") })
}

func containsHead(rendered string, head []string) bool {
	for _, line := range head {
		if !strings.Contains(rendered, line) {
			return false
		}
	}
	return true
}

const (
	setupDown  = "\x1b[B"
	setupEnter = "\r"
	setupEsc   = "\x1b"
)

// The dialog's logo is the pig head, never Pi's SETUP_LOGO_LINES; the sprite step previews the highlighted sprite's head.
func TestFirstTimeSetupLogoIsThePigHeadAndPreviewsTheSprite(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	piglogin.Refresh()
	c := NewFirstTimeSetupComponent(FirstTimeSetupOptions{})
	mode := tui.ActiveTheme().ColorMode()
	rendered := strings.Join(c.Render(100), "\n")
	if strings.Contains(rendered, "██████") {
		t.Fatalf("dialog draws Pi's SETUP_LOGO_LINES:\n%s", rendered)
	}
	if !containsHead(rendered, piglogin.HeadLines(piglogin.Active(), mode)) {
		t.Fatalf("theme step does not draw the active sprite's head")
	}
	c.HandleInput(setupEnter)
	c.HandleInput(setupDown)
	next := piglogin.All()[1]
	if !containsHead(strings.Join(c.Render(100), "\n"), piglogin.HeadLines(next, mode)) {
		t.Fatalf("sprite step does not preview %s's head", next.ID)
	}
}

// Theme step previews live and Esc skips setup without submitting.
func TestFirstTimeSetupThemePreviewAndEscSkip(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	piglogin.Refresh()
	var previews []string
	cancelled, submitted := false, false
	c := NewFirstTimeSetupComponent(FirstTimeSetupOptions{
		OnThemePreview: func(name string) { previews = append(previews, name) },
		OnSubmit:       func(FirstTimeSetupResult) { submitted = true },
		OnCancel:       func() { cancelled = true },
	})
	if !strings.Contains(firstTimeSetupText(c), "Pick a theme.") {
		t.Fatalf("first step is not the theme step:\n%s", firstTimeSetupText(c))
	}
	c.HandleInput(setupDown)
	c.HandleInput(setupDown)
	c.HandleInput(setupDown)
	if !slices.Equal(previews, []string{"dark", "light"}) {
		t.Fatalf("theme previews = %v", previews)
	}
	c.HandleInput(setupEnter)
	c.HandleInput(setupEsc)
	if !cancelled || submitted {
		t.Fatalf("Esc: cancelled=%v submitted=%v", cancelled, submitted)
	}
}

// The sprite step follows the theme step, lists the built-in sprites then extension sprites, and submits the choice.
func TestFirstTimeSetupSpriteStepListsExtensionSpritesAndSubmits(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	piglogin.Refresh()
	firstTimeSetupExtensionSprite(t)
	var result *FirstTimeSetupResult
	c := NewFirstTimeSetupComponent(FirstTimeSetupOptions{OnSubmit: func(r FirstTimeSetupResult) { result = &r }})
	c.HandleInput(setupDown) // dark
	c.HandleInput(setupEnter)
	text := firstTimeSetupText(c)
	if !strings.Contains(text, "Pick a sprite.") {
		t.Fatalf("second step is not the sprite step:\n%s", text)
	}
	// The list scrolls: each sprite shows when highlighted, the built-in ones first, then the extension's.
	all := piglogin.All()
	if all[len(all)-1].ID != "blue-pig" {
		t.Fatalf("extension sprite is not after the built-in sprites: %v", all)
	}
	for i := range len(all) {
		if i > 0 {
			c.HandleInput(setupDown)
		}
		text = firstTimeSetupText(c)
		want := all[i].Name + ": " + all[i].Tagline
		if !strings.Contains(text, "→ "+want) {
			t.Fatalf("sprite step row %d does not highlight %q:\n%s", i, want, text)
		}
	}
	c.HandleInput(setupDown)
	if !strings.Contains(firstTimeSetupText(c), "→ "+piglogin.CreateOption) {
		t.Fatal("the last item is not Create your own...")
	}
	c.HandleInput(setupDown)
	if !strings.Contains(firstTimeSetupText(c), "→ "+piglogin.CreateOption) {
		t.Fatal("selection moved past the last item")
	}
	c.HandleInput("k")
	c.HandleInput(setupEnter)
	if !strings.Contains(firstTimeSetupText(c), "Opt-in to anonymous usage data sharing?") {
		t.Fatalf("third step is not the analytics step:\n%s", firstTimeSetupText(c))
	}
	c.HandleInput(setupDown)
	c.HandleInput(setupEnter)
	want := FirstTimeSetupResult{Theme: "dark", Sprite: "blue-pig", ShareAnalytics: false}
	if result == nil || *result != want {
		t.Fatalf("result = %+v, want %+v", result, want)
	}
}

type keysStartupTerminal struct{ keys []string }

func (k keysStartupTerminal) StartWithReadError(input func([]byte), _ func(), _ func(error)) error {
	for _, key := range k.keys {
		input([]byte(key))
	}
	return nil
}
func (keysStartupTerminal) Stop()        {}
func (keysStartupTerminal) Write(string) {}

// Setup runs on its own startup screen before the interactive TUI, as Pi's showFirstTimeSetup does (main.ts:672-676), and
// saves the theme, the analytics opt-in and the highlighted sprite.
func TestShowFirstTimeSetupSavesTheChoiceBeforeTheTUI(t *testing.T) {
	pigHome, cwd := t.TempDir(), t.TempDir()
	t.Setenv("PIG_HOME", pigHome)
	piglogin.Refresh()
	t.Cleanup(func() { tui.SetTheme("dark"); piglogin.Refresh() })
	agentDir := filepath.Join(pigHome, "agent")
	settings := NewSettingsManager(cwd, agentDir)
	sprites := piglogin.All()
	kratos := slices.IndexFunc(sprites, func(v piglogin.Variant) bool { return v.ID == "kratos" })
	keys := []string{setupDown, setupEnter}
	for range kratos {
		keys = append(keys, setupDown)
	}
	keys = append(keys, setupEnter, setupEnter)
	result, err := showFirstTimeSetupWith(settings, StartupUIOptions{AgentDir: agentDir}, tui.NewWithOutput(io.Discard, 100, 30), keysStartupTerminal{keys})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.Sprite != "kratos" || result.Theme != "dark" || !result.ShareAnalytics {
		t.Fatalf("result = %+v", result)
	}
	if got := settings.GetThemeSetting(); got == nil || *got != "dark" {
		t.Fatalf("saved theme = %v, want dark", got)
	}
	if !settings.GetEnableAnalytics() {
		t.Fatal("analytics opt-in was not saved")
	}
	if got := piglogin.LoadVariant(pigHome).ID; got != "kratos" {
		t.Fatalf("saved sprite = %q, want kratos", got)
	}
	if got := piglogin.Active().ID; got != "kratos" {
		t.Fatalf("active sprite = %q, want kratos", got)
	}
}

// Esc skips setup: nothing is saved.
func TestShowFirstTimeSetupSkipSavesNothing(t *testing.T) {
	pigHome, cwd := t.TempDir(), t.TempDir()
	t.Setenv("PIG_HOME", pigHome)
	piglogin.Refresh()
	t.Cleanup(func() { tui.SetTheme("dark"); piglogin.Refresh() })
	agentDir := filepath.Join(pigHome, "agent")
	result, err := showFirstTimeSetupWith(NewSettingsManager(cwd, agentDir), StartupUIOptions{AgentDir: agentDir}, tui.NewWithOutput(io.Discard, 100, 30), keysStartupTerminal{[]string{setupEnter, setupDown, setupEsc}})
	if err != nil || result != nil {
		t.Fatalf("result = %+v, err = %v", result, err)
	}
	if SettingsFileExists(agentDir) {
		t.Fatal("skipped setup wrote settings.json")
	}
	if _, err := os.Stat(piglogin.StatePath(pigHome)); err == nil {
		t.Fatal("skipped setup saved a sprite")
	}
}

// The pig in the dialog is the highlighted sprite on the sprite step and the chosen one after it, and the submitted ID is
// the highlighted sprite's: choosing Kratos never saves another sprite or shows the default on the analytics step.
func TestFirstTimeSetupKeepsTheHighlightedSpriteThroughAnalyticsAndSubmit(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	piglogin.Refresh()
	var got *FirstTimeSetupResult
	c := NewFirstTimeSetupComponent(FirstTimeSetupOptions{OnSubmit: func(r FirstTimeSetupResult) { got = &r }})
	mode := tui.ActiveTheme().ColorMode()
	c.HandleInput(setupEnter)
	sprites := piglogin.All()
	want := slices.IndexFunc(sprites, func(v piglogin.Variant) bool { return v.ID == "kratos" })
	for range want {
		c.HandleInput(setupDown)
	}
	kratosHead := piglogin.HeadLines(sprites[want], mode)
	text := firstTimeSetupText(c)
	if !strings.Contains(text, "→ Kratos") {
		t.Fatalf("sprite step does not show Kratos highlighted:\n%s", text)
	}
	if !containsHead(strings.Join(c.Render(100), "\n"), kratosHead) {
		t.Fatal("sprite step does not draw Kratos's head")
	}
	c.HandleInput(setupEnter)
	if !containsHead(strings.Join(c.Render(100), "\n"), kratosHead) {
		t.Fatal("analytics step does not draw the chosen sprite (Kratos)")
	}
	c.HandleInput(setupEnter)
	if got == nil || got.Sprite != "kratos" {
		t.Fatalf("submitted %+v, want sprite kratos", got)
	}
}

// The hint line uses Pi's keyHint, whose keys are lowercase keyText (keybinding-hints.ts keyHint), not keyDisplayText.
func TestFirstTimeSetupHintUsesLowercaseKeyText(t *testing.T) {
	c := NewFirstTimeSetupComponent(FirstTimeSetupOptions{})
	if text := firstTimeSetupText(c); !strings.Contains(text, " ↑↓ navigate  enter continue  escape/ctrl+c skip setup") {
		t.Fatalf("hint line differs from Pi's:\n%s", text)
	}
}

// "Create your own..." in the sprite step shows how to create a sprite, keeps the active sprite and continues setup: it
// starts no model turn and saves no sprite.
func TestFirstTimeSetupCreateYourOwnShowsTheHintAndKeepsTheSprite(t *testing.T) {
	pigHome, cwd := t.TempDir(), t.TempDir()
	t.Setenv("PIG_HOME", pigHome)
	piglogin.Refresh()
	t.Cleanup(func() { tui.SetTheme("dark"); piglogin.Refresh() })
	agentDir := filepath.Join(pigHome, "agent")
	settings := NewSettingsManager(cwd, agentDir)
	c := NewFirstTimeSetupComponent(FirstTimeSetupOptions{})
	c.HandleInput(setupEnter)
	for range len(piglogin.All()) {
		c.HandleInput(setupDown)
	}
	text := firstTimeSetupText(c)
	if !strings.Contains(text, "→ "+piglogin.CreateOption) || !strings.Contains(strings.Join(strings.Fields(text), " "), piglogin.CreateHint) {
		t.Fatalf("Create your own... does not show the hint:\n%s", text)
	}
	keys := []string{setupEnter}
	for range len(piglogin.All()) {
		keys = append(keys, setupDown)
	}
	keys = append(keys, setupEnter, setupEnter)
	result, err := showFirstTimeSetupWith(settings, StartupUIOptions{AgentDir: agentDir}, tui.NewWithOutput(io.Discard, 100, 30), keysStartupTerminal{keys})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.Sprite != "" || !result.ShareAnalytics {
		t.Fatalf("result = %+v, want no sprite and a finished setup", result)
	}
	if _, err := os.Stat(piglogin.StatePath(pigHome)); err == nil {
		t.Fatal("Create your own... saved a sprite")
	}
	if got := piglogin.Active().ID; got != piglogin.DefaultID {
		t.Fatalf("active sprite = %q, want %q", got, piglogin.DefaultID)
	}
}
