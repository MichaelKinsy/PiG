package codingagent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/piglogin"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
	"github.com/MichaelKinsy/PiG/tui"
)

// Ports packages/coding-agent/src/modes/interactive/components/first-time-setup.ts

// FirstTimeSetupQuestion is the question the first-time setup dialog asks.
const FirstTimeSetupQuestion = "Opt-in to anonymous usage data sharing?"

// FirstTimeSetupResult is the dialog's submitted choice.
type FirstTimeSetupResult struct {
	Sprite         string
	ShareAnalytics bool
}

// FirstTimeSetupOptions are the dialog's callbacks.
type FirstTimeSetupOptions struct {
	OnSubmit func(result FirstTimeSetupResult)
	OnCancel func()
}

type firstTimeSetupStep string

const (
	firstTimeSetupStepSprite    firstTimeSetupStep = "sprite"
	firstTimeSetupStepAnalytics firstTimeSetupStep = "analytics"
)

var firstTimeSetupAnalyticsOptions = []struct {
	value bool
	label string
}{
	{true, "Share anonymous usage data"},
	{false, "Don't share"},
}

// FirstTimeSetupComponent is the first-time setup dialog: sprite (when pig-login is linked) and analytics opt-in.
// pig divergence (D88): the dialog's logo is the pig head of the sprite in view, not Pi's piLogoLines, and a sprite step
// precedes the analytics step; its options are the built-in sprites, then the extension-registered ones (piglogin.All).
type FirstTimeSetupComponent struct {
	*tui.Container
	step           firstTimeSetupStep
	spriteIndex    int
	analyticsIndex int
	sprites        []piglogin.Variant
	options        FirstTimeSetupOptions
	done           bool
}

// firstTimeSetupSpriteRows is how many sprite rows the sprite step shows at once, so the dialog fits a small terminal.
const firstTimeSetupSpriteRows = 8

// NewFirstTimeSetupComponent builds the dialog over the sprites offered at construction, starting on the active sprite.
// pig additive (D92): pig-login owns /sprite, so with it stripped there are no sprites to offer and no sprite step.
func NewFirstTimeSetupComponent(options FirstTimeSetupOptions) *FirstTimeSetupComponent {
	c := &FirstTimeSetupComponent{Container: tui.NewContainer(), step: firstTimeSetupStepAnalytics, options: options}
	if !pigstrip.Has(pigstrip.ListExtensions, piglogin.Name) {
		c.sprites = piglogin.All()
	}
	if len(c.sprites) > 0 {
		c.step = firstTimeSetupStepSprite
	}
	active := piglogin.Active().ID
	for i, sprite := range c.sprites {
		if sprite.ID == active {
			c.spriteIndex = i
		}
	}
	c.update()
	return c
}

// Invalidate rebuilds on theme changes, e.g. when the system theme receives the terminal's colors.
func (c *FirstTimeSetupComponent) Invalidate() {
	c.update()
	c.Container.Invalidate()
}

// Done reports whether the dialog was submitted or skipped.
func (c *FirstTimeSetupComponent) Done() bool { return c.done }

// logoSprite is the highlighted sprite, which stays the dialog's pig after the sprite step. "Create your own..." shows the
// active sprite, which it keeps.
func (c *FirstTimeSetupComponent) logoSprite() piglogin.Variant {
	if c.spriteIndex < len(c.sprites) {
		return c.sprites[c.spriteIndex]
	}
	return piglogin.Active()
}

func (c *FirstTimeSetupComponent) update() {
	t := tui.ActiveTheme()
	c.Clear()
	c.Add(tui.NewDynamicBorder())
	c.Add(tui.NewSpacer(1))
	c.Add(tui.NewPaddedText(strings.Join(piglogin.HeadLines(c.logoSprite(), t.GetColorMode()), "\n"), 1, 0, nil))
	c.Add(tui.NewSpacer(1))
	// pig divergence (D88): the welcome names PiG where Pi's names APP_NAME.
	c.Add(tui.NewPaddedText(t.Fg("accent", tui.ChalkBold("Welcome to PiG, the minimal coding agent.")), 1, 0, nil))
	c.Add(tui.NewSpacer(1))

	switch c.step {
	case firstTimeSetupStepSprite:
		c.Add(tui.NewPaddedText(t.Fg("text", "Pick a sprite."), 1, 0, nil))
		c.Add(tui.NewPaddedText(t.Fg("muted", "The pig your header shows. Change it anytime with /sprite."), 1, 0, nil))
		c.Add(tui.NewSpacer(1))
		labels := make([]string, len(c.sprites)+1)
		for i, sprite := range c.sprites {
			labels[i] = sprite.Name + ": " + sprite.Tagline
		}
		labels[len(c.sprites)] = piglogin.CreateOption
		c.addScrolledOptionList(labels, c.spriteIndex, firstTimeSetupSpriteRows)
		if c.spriteIndex == len(c.sprites) {
			// Enter keeps the active sprite and continues setup.
			c.Add(tui.NewSpacer(1))
			c.Add(tui.NewPaddedText(t.Fg("muted", piglogin.CreateHint), 1, 0, nil))
		}
	default:
		c.Add(tui.NewPaddedText(t.Fg("text", FirstTimeSetupQuestion), 1, 0, nil))
		c.Add(tui.NewPaddedText(t.Fg("muted", "Opting in stores a tracking identifier in settings.json and enables anonymous\nusage analytics. This helps us to better debug, reproduce, and resolve issues\nand bugs within PiG. You can observe what is shared using /privacy and make\nchanges anytime in settings.json."), 1, 0, nil))
		c.Add(tui.NewSpacer(1))
		labels := make([]string, len(firstTimeSetupAnalyticsOptions))
		for i, option := range firstTimeSetupAnalyticsOptions {
			labels[i] = option.label
		}
		c.addOptionList(labels, c.analyticsIndex)
	}

	confirm := "continue"
	if c.step == firstTimeSetupStepAnalytics {
		confirm = "finish"
	}
	c.Add(tui.NewSpacer(1))
	// Pi's keyHint shows keyText: the bound keys, not capitalized (keybinding-hints.ts:42-44).
	c.Add(tui.NewPaddedText(tui.RawKeyHint("↑↓", "navigate")+"  "+tui.RawKeyHint(strings.Join(tui.GetTUIKeybindings().GetKeys(tui.KBSelectConfirm), "/"), confirm)+"  "+tui.RawKeyHint(strings.Join(tui.GetTUIKeybindings().GetKeys(tui.KBSelectCancel), "/"), "skip setup"), 1, 0, nil))
	c.Add(tui.NewSpacer(1))
	c.Add(tui.NewDynamicBorder())
}

func (c *FirstTimeSetupComponent) addOptionList(labels []string, selectedIndex int) {
	t := tui.ActiveTheme()
	for i, label := range labels {
		prefix, text := "  ", t.Fg("text", label)
		if i == selectedIndex {
			prefix, text = t.Fg("accent", "→ "), t.Fg("accent", label)
		}
		c.Add(tui.NewPaddedText(prefix+text, 1, 0, nil))
	}
}

// addScrolledOptionList shows at most rows options around the selection, then the selection's position when some are hidden.
func (c *FirstTimeSetupComponent) addScrolledOptionList(labels []string, selectedIndex, rows int) {
	if len(labels) <= rows {
		c.addOptionList(labels, selectedIndex)
		return
	}
	start := max(0, min(selectedIndex-rows/2, len(labels)-rows))
	c.addOptionList(labels[start:start+rows], selectedIndex-start)
	c.Add(tui.NewPaddedText(tui.ActiveTheme().Fg("muted", fmt.Sprintf("  (%d/%d)", selectedIndex+1, len(labels))), 1, 0, nil))
}

func (c *FirstTimeSetupComponent) moveSelection(delta int) {
	switch c.step {
	case firstTimeSetupStepSprite:
		c.spriteIndex = max(0, min(len(c.sprites), c.spriteIndex+delta))
	default:
		c.analyticsIndex = max(0, min(len(firstTimeSetupAnalyticsOptions)-1, c.analyticsIndex+delta))
	}
	c.update()
}

// HandleInput moves, advances, submits or cancels.
func (c *FirstTimeSetupComponent) HandleInput(data string) {
	kb := tui.GetKeybindings()
	switch {
	case kb.Matches(data, tui.KBSelectUp) || data == "k":
		c.moveSelection(-1)
	case kb.Matches(data, tui.KBSelectDown) || data == "j":
		c.moveSelection(1)
	case kb.Matches(data, tui.KBSelectConfirm) || data == "\n":
		switch c.step {
		case firstTimeSetupStepSprite:
			c.step = firstTimeSetupStepAnalytics
			c.update()
		default:
			result := FirstTimeSetupResult{ShareAnalytics: firstTimeSetupAnalyticsOptions[c.analyticsIndex].value}
			if c.spriteIndex < len(c.sprites) {
				result.Sprite = c.sprites[c.spriteIndex].ID
			}
			c.done = true
			if c.options.OnSubmit != nil {
				c.options.OnSubmit(result)
			}
		}
	case kb.Matches(data, tui.KBSelectCancel):
		c.done = true
		if c.options.OnCancel != nil {
			c.options.OnCancel()
		}
	}
}

// FirstTimeSetupSettings is the settings store a submitted first-time setup writes.
type FirstTimeSetupSettings interface {
	SetEnableAnalytics(enabled bool) error
}

// ShowFirstTimeSetup shows the first-time setup dialog on its own screen before the interactive TUI starts, as Pi's
// showFirstTimeSetup does (cli/startup-ui.ts, main.ts), and persists a submitted result: the analytics opt-in in settings,
// and the sprite exactly as /sprite set saves it. It returns the submitted result, or nil when setup was skipped.
// A settings write failure is recorded by the settings manager, not returned, as Pi's queued writes are.
// pig divergence (D88): the sprite step offers the built-in sprites; extensions are not loaded yet, so their sprites are
// chosen later with /sprite.
func ShowFirstTimeSetup(settings FirstTimeSetupSettings, opts StartupUIOptions) (*FirstTimeSetupResult, error) {
	ui := tui.New()
	ui.SetLogDirectory(opts.AgentDir)
	ui.SetShowHardwareCursor(opts.Settings.GetShowHardwareCursor())
	ui.SetClearOnShrink(opts.Settings.GetClearOnShrink())
	return showFirstTimeSetupWith(settings, opts, ui, tui.NewStdioProcessTerminal())
}

func showFirstTimeSetupWith(settings FirstTimeSetupSettings, opts StartupUIOptions, ui *tui.TuiMainScreen, terminal startupTerminal) (*FirstTimeSetupResult, error) {
	configureStartupTui(opts)
	var result *FirstTimeSetupResult
	component := NewFirstTimeSetupComponent(FirstTimeSetupOptions{
		OnSubmit: func(r FirstTimeSetupResult) { result = &r },
	})
	blocked := DialogBlockedStatus(tui.ProgramStatusKindPermission, FirstTimeSetupQuestion)
	if _, err := runStartupComponentWithBlocked(component, opts, true, ui, terminal, &blocked); err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	var errs []error
	// startup-ui.ts showFirstTimeSetup: setEnableAnalytics queues its write, and a failure is recorded on the startup settings
	// manager after main.ts collected its diagnostics, so Pi reports none.
	// upstream: packages/coding-agent/src/core/settings-manager.ts:enqueueWrite
	_ = settings.SetEnableAnalytics(result.ShareAnalytics)
	if result.Sprite != "" {
		if err := piglogin.Activate(result.Sprite); err != nil {
			errs = append(errs, err)
		}
	}
	return result, errors.Join(errs...)
}

// SettingsFileExists reports whether agentDir holds settings.json: Pi's first-time setup test (startup-ui.ts).
func SettingsFileExists(agentDir string) bool {
	_, err := os.Stat(filepath.Join(agentDir, "settings.json"))
	return err == nil
}
