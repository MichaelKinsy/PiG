//go:build !pig_strip_pig_login

package codingagent

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/piglogin"
	"github.com/MichaelKinsy/PiG/tui"
)

// The dialog's logo is the pig head, never Pi's SETUP_LOGO_LINES; the sprite step previews the highlighted sprite's head.
func TestFirstTimeSetupLogoIsThePigHeadAndPreviewsTheSprite(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	piglogin.Refresh()
	c := NewFirstTimeSetupComponent(FirstTimeSetupOptions{})
	mode := tui.ActiveTheme().GetColorMode()
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

// Setup runs on its own startup screen before the interactive TUI, as Pi's showFirstTimeSetup does (main.ts:672-676), and
// saves the theme, the analytics opt-in and the highlighted sprite.
// Pi: packages/coding-agent/src/core/settings-manager.ts:1151 (SettingsManager.getEnableAnalytics).
func TestShowFirstTimeSetupSavesTheChoiceBeforeTheTUI(t *testing.T) {
	restoreTUIKeybindings(t)
	restoreStartupTheme(t)
	pigHome, cwd := t.TempDir(), t.TempDir()
	t.Setenv("PIG_HOME", pigHome)
	piglogin.Refresh()
	t.Cleanup(func() { piglogin.Refresh() })
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
	restoreStartupTheme(t)
	restoreTUIKeybindings(t)
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
	mode := tui.ActiveTheme().GetColorMode()
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

// "Create your own..." in the sprite step shows how to create a sprite, keeps the active sprite and continues setup: it
// starts no model turn and saves no sprite.
func TestFirstTimeSetupCreateYourOwnShowsTheHintAndKeepsTheSprite(t *testing.T) {
	restoreStartupTheme(t)
	restoreTUIKeybindings(t)
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

// first-time-setup.ts:56-130: the theme list, the analytics opt-in text and options, the key hints with "continue"/"finish", the → marker and the clamped j/k/newline navigation.
func TestFirstTimeSetupRendersPisStepsAndNavigates(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	piglogin.Refresh()
	var previews []string
	var submitted []FirstTimeSetupResult
	c := NewFirstTimeSetupComponent(FirstTimeSetupOptions{
		OnThemePreview: func(name string) { previews = append(previews, name) },
		OnSubmit:       func(result FirstTimeSetupResult) { submitted = append(submitted, result) },
	})
	rule := strings.Repeat("─", 100)
	hint := func(confirm string) string {
		return " ↑↓ navigate  enter " + confirm + "  escape/ctrl+c skip setup"
	}
	theme := func(system, dark, light string) []string {
		return []string{" Welcome to PiG, the minimal coding agent.", "", " Pick a theme.", "", system, dark, light, "", hint("continue"), "", rule}
	}
	equal := func(label string, got, want []string) {
		t.Helper()
		if !slices.Equal(got, want) {
			t.Fatalf("%s:\n got %q\nwant %q", label, got, want)
		}
	}
	equal("theme step", firstTimeSetupBody(t, c), theme(" → System (matches your terminal colors)", "   Dark", "   Light"))
	c.HandleInput("k")
	equal("k clamps at the first theme", firstTimeSetupBody(t, c), theme(" → System (matches your terminal colors)", "   Dark", "   Light"))
	c.HandleInput("j")
	equal("j moves down", firstTimeSetupBody(t, c), theme("   System (matches your terminal colors)", " → Dark", "   Light"))
	c.HandleInput("j")
	c.HandleInput("j")
	equal("j clamps at the last theme", firstTimeSetupBody(t, c), theme("   System (matches your terminal colors)", "   Dark", " → Light"))
	if want := []string{"dark", "light"}; !slices.Equal(previews, want) {
		t.Fatalf("theme previews = %v, want %v (only moves that change the selection preview)", previews, want)
	}
	c.HandleInput("\n") // theme -> sprite
	c.HandleInput("\n") // sprite -> analytics
	analytics := func(share, dont string) []string {
		return []string{
			" Welcome to PiG, the minimal coding agent.", "",
			" Opt-in to anonymous usage data sharing?",
			" Opting in stores a tracking identifier in settings.json and enables anonymous",
			" usage analytics. This helps us to better debug, reproduce, and resolve issues",
			" and bugs within PiG. You can observe what is shared using /privacy and make",
			" changes anytime in settings.json.", "",
			share, dont, "", hint("finish"), "", rule,
		}
	}
	equal("analytics step", firstTimeSetupBody(t, c), analytics(" → Share anonymous usage data", "   Don't share"))
	c.HandleInput("j")
	c.HandleInput("j")
	equal("analytics selection clamps", firstTimeSetupBody(t, c), analytics("   Share anonymous usage data", " → Don't share"))
	c.HandleInput("\n")
	if len(submitted) != 1 || submitted[0].Theme != "light" || submitted[0].ShareAnalytics {
		t.Fatalf("submitted = %+v, want one result with theme light and no analytics", submitted)
	}
}

// startup-ui.ts showFirstTimeSetup: setTheme and setEnableAnalytics queue their writes, and a failure is recorded on the startup settings
// manager after main.ts:670 collected its diagnostics, so a settings file that cannot be written fails nothing and prints nothing.
func TestShowFirstTimeSetupReportsNoSettingsWriteFailure(t *testing.T) {
	restoreStartupTheme(t)
	restoreTUIKeybindings(t)
	pigHome, cwd := t.TempDir(), t.TempDir()
	t.Setenv("PIG_HOME", pigHome)
	piglogin.Refresh()
	t.Cleanup(func() { tui.SetTheme("dark"); piglogin.Refresh() })
	agentDir := filepath.Join(pigHome, "agent")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := showFirstTimeSetupWith(NewSettingsManager(cwd, agentDir), StartupUIOptions{AgentDir: agentDir}, tui.NewWithOutput(io.Discard, 100, 30), keysStartupTerminal{[]string{setupEnter, setupEnter, setupEnter}})
	if err != nil {
		t.Fatalf("setup err = %v, want none: Pi records the settings write failure and reports nothing", err)
	}
	if result == nil {
		t.Fatal("setup returned no result")
	}
}

// These tests select a theme and install keybindings, so TestThemeTestsRestoreTheProcessTheme and TestStartupPromptTestsRestoreTheProcessKeybindings run them too; a build that strips what they need does not link them, and neither does it have the entries.
func init() {
	themeRestoringTests["TestShowFirstTimeSetupSavesTheChoiceBeforeTheTUI"] = TestShowFirstTimeSetupSavesTheChoiceBeforeTheTUI
	themeRestoringTests["TestShowFirstTimeSetupSkipSavesNothing"] = TestShowFirstTimeSetupSkipSavesNothing
	themeRestoringTests["TestFirstTimeSetupCreateYourOwnShowsTheHintAndKeepsTheSprite"] = TestFirstTimeSetupCreateYourOwnShowsTheHintAndKeepsTheSprite
	themeRestoringTests["TestShowFirstTimeSetupReportsNoSettingsWriteFailure"] = TestShowFirstTimeSetupReportsNoSettingsWriteFailure
	startupKeybindingTests["TestShowFirstTimeSetupSavesTheChoiceBeforeTheTUI"] = TestShowFirstTimeSetupSavesTheChoiceBeforeTheTUI
	startupKeybindingTests["TestShowFirstTimeSetupSkipSavesNothing"] = TestShowFirstTimeSetupSkipSavesNothing
	startupKeybindingTests["TestFirstTimeSetupCreateYourOwnShowsTheHintAndKeepsTheSprite"] = TestFirstTimeSetupCreateYourOwnShowsTheHintAndKeepsTheSprite
	startupKeybindingTests["TestShowFirstTimeSetupReportsNoSettingsWriteFailure"] = TestShowFirstTimeSetupReportsNoSettingsWriteFailure
}
