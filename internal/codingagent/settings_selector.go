// Ports packages/coding-agent/src/modes/interactive/components/settings-selector.ts
package codingagent

import (
	"fmt"
	"maps"
	"slices"
	"strconv"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

// SettingsConfig is upstream's SettingsConfig: the values the selector shows.
type SettingsConfig struct {
	AutoCompact             bool
	DefaultModel            string
	CurrentModel            *ai.Model
	AvailableDefaultModels  []*ai.Model
	ShowImages              bool
	ImageWidthCells         int
	AutoResizeImages        bool
	BlockImages             bool
	EnableSkillCommands     bool
	SteeringMode            string
	FollowUpMode            string
	Transport               string
	HttpIdleTimeoutMs       int
	CacheWarmingMode        CacheWarmingMode
	ThinkingLevel           string
	AvailableThinkingLevels []string
	ModelThinkingLevels     map[string]string
	CurrentTheme            string
	TerminalTheme           tui.TerminalTheme
	AvailableThemes         []string
	HideThinkingBlock       bool
	MermaidRenderingMode    MermaidRenderingMode
	ShowCacheMissNotices    bool
	CollapseChangelog       bool
	EnableInstallTelemetry  bool
	DoubleEscapeAction      string
	TreeFilterMode          string
	ShowHardwareCursor      bool
	EditorPaddingX          int
	OutputPad               OutputPad
	AutocompleteMaxVisible  int
	QuietStartup            QuietStartup
	DefaultProjectTrust     string
	ClearOnShrink           bool
	ShowTerminalProgress    bool
	TuiMode                 string
	FullscreenExitOutput    FullscreenExitOutput
	FullscreenScrollbar     string
	FullscreenCopyOnSelect  bool
	// FullscreenWheelScrollLines is upstream's WheelScrollLines: "auto" or a line count.
	FullscreenWheelScrollLines WheelScrollLines
	Warnings                   WarningSettings
	// MaskSecretInput feeds the PiG-only "Mask secret input" row.
	// pig divergence (D80): configurable input privacy leaves the upstream setting order intact.
	MaskSecretInput bool
}

// SettingsCallbacks is upstream's SettingsCallbacks. The selector skips a callback that is nil; upstream's TypeScript callers always supply every one.
type SettingsCallbacks struct {
	OnAutoCompactChange                func(enabled bool)
	OnShowImagesChange                 func(enabled bool)
	OnImageWidthCellsChange            func(width int)
	OnAutoResizeImagesChange           func(enabled bool)
	OnBlockImagesChange                func(blocked bool)
	OnEnableSkillCommandsChange        func(enabled bool)
	OnSteeringModeChange               func(mode string)
	OnFollowUpModeChange               func(mode string)
	OnTransportChange                  func(transport string)
	OnHttpIdleTimeoutMsChange          func(timeoutMs int)
	OnCacheWarmingModeChange           func(mode CacheWarmingMode)
	OnModelThinkingLevelChange         func(provider, modelID, level string)
	OnModelThinkingLevelRemove         func(provider, modelID string)
	OnThemeChange                      func(theme string)
	OnThemePreview                     func(theme string)
	OnHideThinkingBlockChange          func(hidden bool)
	OnMermaidRenderingModeChange       func(mode MermaidRenderingMode)
	OnShowCacheMissNoticesChange       func(shown bool)
	OnCollapseChangelogChange          func(collapsed bool)
	OnEnableInstallTelemetryChange     func(enabled bool)
	OnDoubleEscapeActionChange         func(action string)
	OnTreeFilterModeChange             func(mode string)
	OnShowHardwareCursorChange         func(enabled bool)
	OnEditorPaddingXChange             func(padding int)
	OnOutputPadChange                  func(padding OutputPad)
	OnAutocompleteMaxVisibleChange     func(maxVisible int)
	OnQuietStartupChange               func(quiet QuietStartup)
	OnDefaultProjectTrustChange        func(defaultProjectTrust string)
	OnClearOnShrinkChange              func(enabled bool)
	OnShowTerminalProgressChange       func(enabled bool)
	OnTuiModeChange                    func(mode string)
	OnFullscreenExitOutputChange       func(output FullscreenExitOutput)
	OnFullscreenScrollbarChange        func(mode string)
	OnFullscreenCopyOnSelectChange     func(enabled bool)
	OnFullscreenWheelScrollLinesChange func(lines WheelScrollLines)
	OnWarningsChange                   func(warnings WarningSettings)
	// OnMaskSecretInputChange handles the PiG-only "Mask secret input" row.
	// pig divergence (D80): configurable input privacy leaves the upstream setting order intact.
	OnMaskSecretInputChange func(enabled bool)
	OnCancel                func()
}

// defaultProjectTrustLabels is upstream's DEFAULT_PROJECT_TRUST_LABELS in its key order.
var defaultProjectTrustLabels = []struct{ value, label string }{
	{"ask", "Ask"},
	{"always", "Always trust"},
	{"never", "Never trust"},
}

func defaultProjectTrustLabel(value string) string {
	for _, entry := range defaultProjectTrustLabels {
		if entry.value == value {
			return entry.label
		}
	}
	return ""
}

func boolSettingValue(enabled bool) string {
	if enabled {
		return "true"
	}
	return "false"
}

// settingsParseInt is JavaScript's parseInt(value, 10) for the values the rows offer.
func settingsParseInt(value string) int {
	//portlint:allow numbers the values come from the rows own fixed option lists, which hold plain decimal integers
	n, _ := strconv.Atoi(value)
	return n
}

func modelThinkingOverridesSummary(overrides map[string]string) string {
	if len(overrides) == 0 {
		return "none"
	}
	return fmt.Sprintf("%d configured", len(overrides))
}

// WarningSettingsSubmenu is upstream's WarningSettingsSubmenu: a settings list of the individual warnings.
type warningSettingsSubmenu struct {
	tui.BaseComponent
	list  *tui.SettingsList
	state WarningSettings
}

func newWarningSettingsSubmenu(warnings WarningSettings, onChange func(WarningSettings), onCancel func()) *warningSettingsSubmenu {
	menu := &warningSettingsSubmenu{state: warnings}
	// upstream: `(this.state.anthropicExtraUsage ?? true)`: a warning the settings never set is on.
	if !menu.state.anthropicExtraUsageSet {
		menu.state.AnthropicExtraUsage = true
	}
	items := []tui.SettingItem{{
		ID:           "anthropic-extra-usage",
		Label:        "Anthropic extra usage",
		Description:  "Warn when Anthropic subscription auth may use paid extra usage",
		CurrentValue: warningBoolString(menu.state.AnthropicExtraUsage),
		Values:       []string{"true", "false"},
	}}
	menu.list = tui.NewSettingsList(items, min(len(items), 10), tui.GetSettingsListTheme(), func(id, value string) {
		if id == "anthropic-extra-usage" {
			menu.state.AnthropicExtraUsage = value == "true"
			menu.state.anthropicExtraUsageSet = true
			onChange(menu.state)
		}
	}, onCancel)
	return menu
}

func (m *warningSettingsSubmenu) Render(width int) []string { return m.list.Render(width) }
func (m *warningSettingsSubmenu) HandleInput(data string)   { m.list.HandleInput(data) }

// selectSubmenuStep reports a SelectSubmenu's selection change, selection and cancel as upstream's constructor callbacks do.
type selectSubmenuStep struct {
	tui.BaseComponent
	submenu  *tui.SelectSubmenuComponent
	onSelect func(string)
	onCancel func()
}

func newSelectSubmenuStep(submenu *tui.SelectSubmenuComponent, onSelect func(string), onCancel func(), onHighlight func(string)) *selectSubmenuStep {
	submenu.OnSelectionChange = onHighlight
	return &selectSubmenuStep{submenu: submenu, onSelect: onSelect, onCancel: onCancel}
}

func (s *selectSubmenuStep) Render(width int) []string { return s.submenu.Render(width) }

func (s *selectSubmenuStep) HandleInput(data string) {
	s.submenu.HandleInput(data)
	if !s.submenu.Done() {
		return
	}
	if s.submenu.Cancelled() {
		s.onCancel()
		return
	}
	s.onSelect(s.submenu.SelectedValue())
}

// automaticThemeMenu is the automatic theme menu's content: upstream's showAutomaticMenu heading above its settings list, which renders an open child picker in its own place.
type automaticThemeMenu struct {
	tui.BaseComponent
	list *tui.SettingsList
}

func (m *automaticThemeMenu) Render(width int) []string {
	th := tui.ActiveTheme()
	// Upstream showAutomaticMenu adds the heading rows as Text(..., 0, 0), so they wrap to the render width.
	text := func(content string) []string { return tui.NewPaddedText(content, 0, 0, nil).Render(width) }
	lines := text(th.Bold(th.Fg("accent", "Automatic Theme")))
	lines = append(lines, "")
	lines = append(lines, text(th.Fg("muted", "Choose themes for terminal light and dark appearance."))...)
	lines = append(lines, text(th.Fg("muted", "Light/dark detection requires terminal support."))...)
	lines = append(lines, "")
	return append(lines, m.list.Render(width)...)
}

// themeSubmenu is upstream's ThemeSubmenu: the theme row's single and automatic theme menus. Browsing previews a theme through OnThemePreview, Esc restores the original setting, and the chosen setting reaches the row through onDone.
type themeSubmenu struct {
	tui.BaseComponent
	callbacks            SettingsCallbacks
	availableThemes      []string
	terminalTheme        tui.TerminalTheme
	onDone               func(*string)
	originalThemeSetting string
	mode                 string
	singleTheme          string
	lightTheme           string
	darkTheme            string
	content              tui.Component
	input                tui.InputHandler
}

func newThemeSubmenu(currentThemeSetting string, terminalTheme tui.TerminalTheme, availableThemes []string, callbacks SettingsCallbacks, onDone func(*string)) *themeSubmenu {
	menu := &themeSubmenu{callbacks: callbacks, availableThemes: availableThemes, terminalTheme: terminalTheme, onDone: onDone, originalThemeSetting: currentThemeSetting}
	_, _, isAuto := tui.ParseAutoThemeSetting(currentThemeSetting)
	menu.lightTheme, menu.darkTheme = defaultAutomaticThemeNames(currentThemeSetting, availableThemes)
	fixedTheme := currentThemeSetting
	if isAuto || containsSlash(currentThemeSetting) {
		fixedTheme = ""
	}
	menu.mode = "single"
	if isAuto {
		menu.mode = "automatic"
		fixedTheme = menu.activeAutomaticTheme()
	}
	menu.singleTheme = preferredThemeName(availableThemes, fixedTheme, tui.SystemThemeName)
	if isAuto {
		menu.showAutomaticMenu()
	} else {
		menu.showSingleMenu()
	}
	return menu
}

func containsSlash(value string) bool {
	for i := range len(value) {
		if value[i] == '/' {
			return true
		}
	}
	return false
}

func (m *themeSubmenu) Render(width int) []string { return m.content.Render(width) }

func (m *themeSubmenu) HandleInput(data string) {
	if m.input != nil {
		m.input.HandleInput(data)
	}
	m.Invalidate()
}

func (m *themeSubmenu) preview(theme string) {
	if m.callbacks.OnThemePreview != nil {
		m.callbacks.OnThemePreview(theme)
	}
}

func (m *themeSubmenu) setContent(content tui.Component, input tui.InputHandler) {
	m.content, m.input = content, input
}

func (m *themeSubmenu) showSingleMenu() {
	m.mode = "single"
	submenu := tui.NewSelectSubmenu("Theme", "Select a theme, or choose automatic to follow terminal appearance.", themeSelectItemsWithAutomatic(m.availableThemes, m.singleTheme), m.singleTheme)
	step := newSelectSubmenuStep(submenu, func(value string) {
		if value == automaticThemeValue {
			m.mode = "automatic"
			m.preview(m.themeSetting())
			m.showAutomaticMenu()
			return
		}
		m.singleTheme = value
		m.apply(value)
	}, m.cancel, func(value string) {
		if value == automaticThemeValue {
			m.preview(m.automaticThemeSetting())
			return
		}
		m.preview(value)
	})
	m.setContent(step, step)
}

func (m *themeSubmenu) showAutomaticMenu() {
	m.mode = "automatic"
	items := []tui.SettingItem{
		{
			ID:           "light-theme",
			Label:        "Light theme",
			Description:  "Theme to use in automatic mode when the terminal is light",
			CurrentValue: m.lightTheme,
			Submenu: func(currentValue string, doneWithOptions func(*string, *tui.SubmenuDoneOptions)) tui.Component {
				done := func(value *string) { doneWithOptions(value, nil) }
				return m.createThemeSelect("Light Theme", "Select the theme to use for light terminal appearance", currentValue, done, func(value string) {
					m.lightTheme = value
					m.preview(m.themeSetting())
					done(&value)
				})
			},
		},
		{
			ID:           "dark-theme",
			Label:        "Dark theme",
			Description:  "Theme to use in automatic mode when the terminal is dark",
			CurrentValue: m.darkTheme,
			Submenu: func(currentValue string, doneWithOptions func(*string, *tui.SubmenuDoneOptions)) tui.Component {
				done := func(value *string) { doneWithOptions(value, nil) }
				return m.createThemeSelect("Dark Theme", "Select the theme to use for dark terminal appearance", currentValue, done, func(value string) {
					m.darkTheme = value
					m.preview(m.themeSetting())
					done(&value)
				})
			},
		},
		{ID: "apply", Label: "Apply", Description: "Save and go back", CurrentValue: "save and go back", Values: []string{"save and go back"}},
		{ID: "single-mode", Label: "Change mode", Description: "Switch to one theme for light and dark", CurrentValue: "switch to single theme", Values: []string{"switch to single theme"}},
	}
	list := tui.NewSettingsList(items, min(len(items), 10), tui.GetSettingsListTheme(), func(id, _ string) {
		switch id {
		case "single-mode":
			m.mode = "single"
			m.singleTheme = m.activeAutomaticTheme()
			m.preview(m.singleTheme)
			m.showSingleMenu()
		case "apply":
			m.apply(m.automaticThemeSetting())
		}
	}, m.cancel)
	m.setContent(&automaticThemeMenu{list: list}, list)
}

// createThemeSelect restores the parent's pending automatic pair on cancel (settings-selector.ts createThemeSelect).
func (m *themeSubmenu) createThemeSelect(title, description, currentValue string, done func(*string), onSelect func(string)) tui.Component {
	submenu := tui.NewSelectSubmenu(title, description, themeSelectItems(m.availableThemes, currentValue), currentValue)
	return newSelectSubmenuStep(submenu, onSelect, func() {
		m.preview(m.themeSetting())
		done(nil)
	}, m.preview)
}

func (m *themeSubmenu) themeSetting() string {
	if m.mode == "automatic" {
		return m.automaticThemeSetting()
	}
	return m.singleTheme
}

func (m *themeSubmenu) activeAutomaticTheme() string {
	if m.terminalTheme == "light" {
		return m.lightTheme
	}
	return m.darkTheme
}

func (m *themeSubmenu) automaticThemeSetting() string { return m.lightTheme + "/" + m.darkTheme }

func (m *themeSubmenu) apply(themeSetting string) { m.onDone(&themeSetting) }

func (m *themeSubmenu) cancel() {
	m.preview(m.originalThemeSetting)
	m.onDone(nil)
}

// newModelThinkingSubmenu is the "Default thinking level per model" submenu: a model picker, then a level picker, looping after each choice.
func newModelThinkingSubmenu(config SettingsConfig, overrides map[string]string, onChange func(model *ai.Model, level string), onDone func()) *SteppedSubmenu {
	byKey := make(map[string]*ai.Model, len(config.AvailableDefaultModels))
	for _, model := range config.AvailableDefaultModels {
		byKey[modelSpec(model)] = model
	}
	currentDefaultModelKey := ""
	if _, ok := byKey[config.DefaultModel]; ok {
		currentDefaultModelKey = config.DefaultModel
	}
	currentModelKey := modelSpec(config.CurrentModel)
	steps := []SteppedSubmenuStep{
		{
			Key:         "model",
			Title:       func(map[string]string) string { return "Per-Model Thinking Level" },
			Description: func(map[string]string) string { return "Select a model to configure" },
			Options: func(map[string]string) []tui.SelectItem {
				sorted := slices.Clone(config.AvailableDefaultModels)
				collator := collate.New(language.Und)
				slices.SortStableFunc(sorted, func(a, b *ai.Model) int {
					aKey, bKey := modelSpec(a), modelSpec(b)
					if aKey == currentModelKey {
						return -1
					}
					if bKey == currentModelKey {
						return 1
					}
					if aKey == currentDefaultModelKey {
						return -1
					}
					if bKey == currentDefaultModelKey {
						return 1
					}
					return collator.CompareString(a.ProviderMeta.ProviderID, b.ProviderMeta.ProviderID)
				})
				items := make([]tui.SelectItem, 0, len(sorted))
				for _, model := range sorted {
					key := modelSpec(model)
					items = append(items, tui.SelectItem{Value: key, Label: model.ID + " " + tui.ActiveTheme().Muted + "[" + model.ProviderMeta.ProviderID + "]\x1b[39m", Description: overrides[key]})
				}
				if len(items) == 0 {
					items = append(items, tui.SelectItem{Value: "__none__", Label: "No models available", Description: "Log in to a provider or configure an API key first"})
				}
				return items
			},
			Preselect: func(map[string]string) string {
				if currentModelKey != "" {
					return currentModelKey
				}
				return currentDefaultModelKey
			},
			// upstream: packages/coding-agent/src/modes/interactive/components/settings-selector.ts:MODEL_PICKER_LAYOUT
			Layout: tui.SelectSubmenuOptions{Searchable: true, MinPrimaryColumnWidth: 12, MaxPrimaryColumnWidth: 46},
		},
		{
			Key: "level",
			Title: func(ctx map[string]string) string {
				if model := byKey[ctx["model"]]; model != nil {
					return fmt.Sprintf("Thinking Level for %s [%s]", model.ID, model.ProviderMeta.ProviderID)
				}
				return "Thinking Level for " + ctx["model"]
			},
			Description: func(map[string]string) string { return "Select default thinking level for this model" },
			Options: func(ctx map[string]string) []tui.SelectItem {
				model := byKey[ctx["model"]]
				if model == nil {
					return nil
				}
				levels := levelsForModel(model)
				items := make([]tui.SelectItem, 0, len(levels)+1)
				for _, level := range levels {
					label := "  " + level
					if level == overrides[ctx["model"]] {
						label = "✓ " + level
					}
					items = append(items, tui.SelectItem{Value: level, Label: label, Description: thinkingDescriptions[level]})
				}
				if _, exists := overrides[ctx["model"]]; exists {
					items = append(items, tui.SelectItem{Value: modelThinkingClearOverrideValue, Label: "  (clear override)", Description: "Revert to global default (" + config.ThinkingLevel + ")"})
				}
				return items
			},
			Preselect: func(ctx map[string]string) string { return overrides[ctx["model"]] },
		},
	}
	return NewSteppedSubmenu(steps, func(ctx map[string]string) {
		model := byKey[ctx["model"]]
		if model == nil {
			return
		}
		level := ctx["level"]
		onChange(model, level)
		if level == modelThinkingClearOverrideValue {
			delete(overrides, ctx["model"])
		} else {
			overrides[ctx["model"]] = level
		}
	}, onDone, SteppedSubmenuOptions{Loop: true})
}

// SettingsSelectorComponent is upstream's SettingsSelectorComponent: a bordered settings list whose changes reach the callbacks.
type SettingsSelectorComponent struct {
	*tui.Container
	settingsList *tui.SettingsList
}

// NewSettingsSelectorComponent builds the selector's rows from config in upstream's order and routes each change to its callback.
func NewSettingsSelectorComponent(config SettingsConfig, callbacks SettingsCallbacks) *SettingsSelectorComponent {
	supportsImages := tui.GetCapabilities().Images != ""
	currentWarnings := config.Warnings
	currentModelThinkingLevels := maps.Clone(config.ModelThinkingLevels)
	if currentModelThinkingLevels == nil {
		currentModelThinkingLevels = map[string]string{}
	}

	toggle := func(id, label, description string, current bool) tui.SettingItem {
		return tui.SettingItem{ID: id, Label: label, Description: description, CurrentValue: boolSettingValue(current), Values: []string{"true", "false"}}
	}
	cycle := func(id, label, description, current string, values ...string) tui.SettingItem {
		return tui.SettingItem{ID: id, Label: label, Description: description, CurrentValue: current, Values: values}
	}
	timeoutLabels := make([]string, len(httpIdleTimeoutChoices))
	for i, choice := range httpIdleTimeoutChoices {
		timeoutLabels[i] = choice.label
	}
	cacheWarmingModes := make([]string, len(CacheWarmingModes))
	for i, mode := range CacheWarmingModes {
		cacheWarmingModes[i] = string(mode)
	}
	trustLabels := make([]string, len(defaultProjectTrustLabels))
	for i, entry := range defaultProjectTrustLabels {
		trustLabels[i] = entry.label
	}

	// Upstream builds the image, padding and cursor rows by splicing them in after "autocompact"; the order below is the result of those splices.
	items := []tui.SettingItem{toggle("autocompact", "Auto-compact", "Automatically compact context when it gets too large", config.AutoCompact)}
	if supportsImages {
		items = append(items,
			toggle("show-images", "Show images", "Render images inline in terminal", config.ShowImages),
			cycle("image-width-cells", "Image width", "Preferred inline image width in terminal cells", strconv.Itoa(config.ImageWidthCells), "60", "80", "120"),
		)
	}
	items = append(items,
		toggle("auto-resize-images", "Auto-resize images", "Resize large images to 2000x2000 max for better model compatibility", config.AutoResizeImages),
		toggle("block-images", "Block images", "Prevent images from being sent to LLM providers", config.BlockImages),
		toggle("skill-commands", "Skill commands", "Register skills as /skill:name commands", config.EnableSkillCommands),
		toggle("show-hardware-cursor", "Show hardware cursor", "Show the terminal cursor while still positioning it for IME support", config.ShowHardwareCursor),
		cycle("editor-padding", "Editor padding", "Horizontal padding for input editor (0-3)", strconv.Itoa(config.EditorPaddingX), "0", "1", "2", "3"),
		cycle("output-padding", "Output padding", "Horizontal padding for messages, tool output, and command output", strconv.Itoa(int(config.OutputPad)), "0", "1"),
		cycle("autocomplete-max-visible", "Autocomplete max items", "Max visible items in autocomplete dropdown (3-20)", strconv.Itoa(config.AutocompleteMaxVisible), "3", "5", "7", "10", "15", "20"),
		toggle("clear-on-shrink", "Clear on shrink", "Clear empty rows when content shrinks (may cause flicker)", config.ClearOnShrink),
		toggle("terminal-progress", "Terminal progress", "Show OSC 9;4 progress indicators in the terminal tab bar", config.ShowTerminalProgress),
		cycle("steering-mode", "Steering mode", "Enter while streaming queues steering messages. 'one-at-a-time': deliver one, wait for response. 'all': deliver all at once.", config.SteeringMode, "one-at-a-time", "all"),
		cycle("follow-up-mode", "Follow-up mode", tui.ActionKeyDisplayTextOr("app.message.followUp", "alt+enter")+" queues follow-up messages until agent stops. 'one-at-a-time': deliver one, wait for response. 'all': deliver all at once.", config.FollowUpMode, "one-at-a-time", "all"),
		cycle("transport", "Transport", "Preferred transport for providers that support multiple transports", config.Transport, "sse", "websocket", "websocket-cached", "auto"),
		cycle("http-idle-timeout", "HTTP idle timeout", "Maximum idle gap while waiting for HTTP headers or body chunks. Disable for local models that pause longer than five minutes.", formatHTTPIdleTimeoutMs(config.HttpIdleTimeoutMs), timeoutLabels...),
		cycle("cache-warming-mode", "Cache warming", "off; streaming while the agent runs; idle also between runs while continuation stays profitable", string(config.CacheWarmingMode), cacheWarmingModes...),
		toggle("hide-thinking", "Hide thinking", "Hide thinking blocks in assistant responses", config.HideThinkingBlock),
		cycle("mermaid-rendering", "Mermaid diagrams", "Render Mermaid code blocks as Unicode diagrams", string(config.MermaidRenderingMode), string(MermaidRenderingOff), string(MermaidRenderingFinal), string(MermaidRenderingStreaming)),
		toggle("cache-miss-notices", "Cache miss notices", "Show transcript notices for cache costs and provider recovery diagnostics", config.ShowCacheMissNotices),
		toggle("collapse-changelog", "Collapse changelog", "Show condensed changelog after updates", config.CollapseChangelog),
		cycle("quiet-startup", "Quiet startup", "Disable verbose printing at startup (header: keep only the startup header)", config.QuietStartup.String(), "true", "header", "false"),
		toggle("install-telemetry", "Install telemetry", "Send an anonymous version/update ping after changelog-detected updates", config.EnableInstallTelemetry),
		cycle("default-project-trust", "Default project trust", "Fallback behavior when no extension or saved trust decision decides project trust", defaultProjectTrustLabel(config.DefaultProjectTrust), trustLabels...),
		cycle("double-escape-action", "Double-escape action", "Action when pressing Escape twice with empty editor", config.DoubleEscapeAction, doubleEscapeActions()...),
		cycle("tree-filter-mode", "Tree filter mode", "Default filter when opening /tree", config.TreeFilterMode, "default", "no-tools", "user-only", "labeled-only", "all"),
		// pig divergence (D80): configurable input privacy leaves the upstream setting order intact.
		toggle("mask-secret-input", "Mask secret input", "PiG default: hide secret input with a count and last four characters. Differs from Pi; false restores Pi's plain-text behavior.", config.MaskSecretInput),
		tui.SettingItem{
			ID: "warnings", Label: "Warnings", Description: "Enable or disable individual warnings", CurrentValue: "configure",
			Submenu: func(_ string, doneWithOptions func(*string, *tui.SubmenuDoneOptions)) tui.Component {
				done := func(value *string) { doneWithOptions(value, nil) }
				return newWarningSettingsSubmenu(currentWarnings, func(warnings WarningSettings) {
					currentWarnings = warnings
					if callbacks.OnWarningsChange != nil {
						callbacks.OnWarningsChange(warnings)
					}
				}, func() { done(nil) })
			},
		},
		tui.SettingItem{
			ID: "model-thinking", Label: "Default thinking level per model",
			Description:  "Override the default thinking level for specific models. " + tui.ActionKeyDisplayText("app.thinking.cycle") + " cycles in-session.",
			CurrentValue: modelThinkingOverridesSummary(currentModelThinkingLevels),
			Submenu: func(_ string, doneWithOptions func(*string, *tui.SubmenuDoneOptions)) tui.Component {
				done := func(value *string) { doneWithOptions(value, nil) }
				return newModelThinkingSubmenu(config, currentModelThinkingLevels, func(model *ai.Model, level string) {
					if level == modelThinkingClearOverrideValue {
						if callbacks.OnModelThinkingLevelRemove != nil {
							callbacks.OnModelThinkingLevelRemove(model.ProviderMeta.ProviderID, model.ID)
						}
						return
					}
					if callbacks.OnModelThinkingLevelChange != nil {
						callbacks.OnModelThinkingLevelChange(model.ProviderMeta.ProviderID, model.ID, level)
					}
				}, func() {
					summary := modelThinkingOverridesSummary(currentModelThinkingLevels)
					done(&summary)
				})
			},
		},
		cycle("tui-mode", "TUI mode", "Interface layout; regular mode uses the terminal's normal scrollback", config.TuiMode, "regular", "fullscreen"),
		cycle("fullscreen-exit-output", "Fullscreen exit output", "Print the transcript or only a session resume hint when exiting fullscreen mode", string(config.FullscreenExitOutput), string(FullscreenExitOutputTranscript), string(FullscreenExitOutputResumeHint)),
		cycle("fullscreen-scrollbar", "Fullscreen scrollbar", "Scrollbar behavior in fullscreen mode; has no effect in regular mode", config.FullscreenScrollbar, "auto", "always", "hidden"),
		toggle("fullscreen-copy-on-select", "Fullscreen copy on select", "Automatically copy selected text in fullscreen mode; disable to copy selections with Ctrl+X", config.FullscreenCopyOnSelect),
		cycle("fullscreen-wheel-scroll-lines", "Fullscreen wheel scrolling", "Lines per mouse-wheel event in fullscreen mode; 'auto' speeds up fast wheel spins where the terminal does not", wheelScrollLinesLabel(config.FullscreenWheelScrollLines), wheelScrollLinesValues(config.FullscreenWheelScrollLines)...),
		tui.SettingItem{
			ID: "theme", Label: "Theme", Description: "Color theme for the interface", CurrentValue: config.CurrentTheme,
			Submenu: func(currentValue string, doneWithOptions func(*string, *tui.SubmenuDoneOptions)) tui.Component {
				done := func(value *string) { doneWithOptions(value, nil) }
				return newThemeSubmenu(currentValue, config.TerminalTheme, config.AvailableThemes, callbacks, done)
			},
		},
	)
	// pig additive (D92): the rows of a stripped built-in are not offered.
	items = slices.DeleteFunc(items, func(item tui.SettingItem) bool { return settingStripped(item.ID) })

	selector := &SettingsSelectorComponent{}
	selector.settingsList = tui.NewSettingsList(items, 10, tui.GetSettingsListTheme(), func(id, newValue string) { selector.dispatch(callbacks, id, newValue) }, callbacks.OnCancel, tui.SettingsListOptions{EnableSearch: true})
	selector.Container = tui.NewContainer(tui.NewDynamicBorder(), selector.settingsList, tui.NewDynamicBorder())
	return selector
}

// IsDirty and NeedsRedraw report the list's invalidation, because input changes the list without invalidating the container that holds it and a parent container caches this component's rendered lines until it is dirty.
func (s *SettingsSelectorComponent) IsDirty() bool {
	return s.Container.IsDirty() || s.settingsList.IsDirty()
}

// pig additive (D91): SurfaceLive reports that the list changes without invalidating the
// selector, as IsDirty does, so a TuiSurface rebuilds it every frame.
func (s *SettingsSelectorComponent) SurfaceLive() bool { return true }

// NeedsRedraw clears the container's invalidation and reports the list's without clearing it. A parent container consumes the flag before it renders this component, and the list is a child of the embedded container, which re-renders it only while the list is dirty; clearing the list's flag here would leave that container's cached lines in place.
func (s *SettingsSelectorComponent) NeedsRedraw() bool {
	container := s.Container.NeedsRedraw()
	return container || s.settingsList.IsDirty()
}

// GetSettingsList returns the list that takes the selector's input.
func (s *SettingsSelectorComponent) GetSettingsList() *tui.SettingsList { return s.settingsList }

// dispatch is the onChange switch of upstream's SettingsList.
func (s *SettingsSelectorComponent) dispatch(callbacks SettingsCallbacks, id, newValue string) {
	switch id {
	case "autocompact":
		if callbacks.OnAutoCompactChange != nil {
			callbacks.OnAutoCompactChange(newValue == "true")
		}
	case "show-images":
		if callbacks.OnShowImagesChange != nil {
			callbacks.OnShowImagesChange(newValue == "true")
		}
	case "image-width-cells":
		if callbacks.OnImageWidthCellsChange != nil {
			callbacks.OnImageWidthCellsChange(settingsParseInt(newValue))
		}
	case "auto-resize-images":
		if callbacks.OnAutoResizeImagesChange != nil {
			callbacks.OnAutoResizeImagesChange(newValue == "true")
		}
	case "block-images":
		if callbacks.OnBlockImagesChange != nil {
			callbacks.OnBlockImagesChange(newValue == "true")
		}
	case "skill-commands":
		if callbacks.OnEnableSkillCommandsChange != nil {
			callbacks.OnEnableSkillCommandsChange(newValue == "true")
		}
	case "steering-mode":
		if callbacks.OnSteeringModeChange != nil {
			callbacks.OnSteeringModeChange(newValue)
		}
	case "follow-up-mode":
		if callbacks.OnFollowUpModeChange != nil {
			callbacks.OnFollowUpModeChange(newValue)
		}
	case "transport":
		if callbacks.OnTransportChange != nil {
			callbacks.OnTransportChange(newValue)
		}
	case "http-idle-timeout":
		if choice, ok := parseHTTPIdleTimeoutLabel(newValue); ok && callbacks.OnHttpIdleTimeoutMsChange != nil {
			callbacks.OnHttpIdleTimeoutMsChange(choice)
		}
	case "cache-warming-mode":
		if callbacks.OnCacheWarmingModeChange != nil {
			callbacks.OnCacheWarmingModeChange(CacheWarmingMode(newValue))
		}
	case "hide-thinking":
		if callbacks.OnHideThinkingBlockChange != nil {
			callbacks.OnHideThinkingBlockChange(newValue == "true")
		}
	case "mermaid-rendering":
		if callbacks.OnMermaidRenderingModeChange != nil {
			callbacks.OnMermaidRenderingModeChange(MermaidRenderingMode(newValue))
		}
	case "cache-miss-notices":
		if callbacks.OnShowCacheMissNoticesChange != nil {
			callbacks.OnShowCacheMissNoticesChange(newValue == "true")
		}
	case "collapse-changelog":
		if callbacks.OnCollapseChangelogChange != nil {
			callbacks.OnCollapseChangelogChange(newValue == "true")
		}
	case "quiet-startup":
		if callbacks.OnQuietStartupChange != nil {
			quiet := QuietStartupFalse
			switch newValue {
			case "header":
				quiet = QuietStartupHeader
			case "true":
				quiet = QuietStartupTrue
			}
			callbacks.OnQuietStartupChange(quiet)
		}
	case "install-telemetry":
		if callbacks.OnEnableInstallTelemetryChange != nil {
			callbacks.OnEnableInstallTelemetryChange(newValue == "true")
		}
	case "default-project-trust":
		for _, entry := range defaultProjectTrustLabels {
			if entry.label == newValue && callbacks.OnDefaultProjectTrustChange != nil {
				callbacks.OnDefaultProjectTrustChange(entry.value)
			}
		}
	case "double-escape-action":
		if callbacks.OnDoubleEscapeActionChange != nil {
			callbacks.OnDoubleEscapeActionChange(newValue)
		}
	case "tree-filter-mode":
		if callbacks.OnTreeFilterModeChange != nil {
			callbacks.OnTreeFilterModeChange(newValue)
		}
	case "mask-secret-input":
		if callbacks.OnMaskSecretInputChange != nil {
			callbacks.OnMaskSecretInputChange(newValue == "true")
		}
	case "show-hardware-cursor":
		if callbacks.OnShowHardwareCursorChange != nil {
			callbacks.OnShowHardwareCursorChange(newValue == "true")
		}
	case "editor-padding":
		if callbacks.OnEditorPaddingXChange != nil {
			callbacks.OnEditorPaddingXChange(settingsParseInt(newValue))
		}
	case "output-padding":
		if callbacks.OnOutputPadChange != nil {
			padding := OutputPadOne
			if newValue == "0" {
				padding = OutputPadNone
			}
			callbacks.OnOutputPadChange(padding)
		}
	case "autocomplete-max-visible":
		if callbacks.OnAutocompleteMaxVisibleChange != nil {
			callbacks.OnAutocompleteMaxVisibleChange(settingsParseInt(newValue))
		}
	case "clear-on-shrink":
		if callbacks.OnClearOnShrinkChange != nil {
			callbacks.OnClearOnShrinkChange(newValue == "true")
		}
	case "terminal-progress":
		if callbacks.OnShowTerminalProgressChange != nil {
			callbacks.OnShowTerminalProgressChange(newValue == "true")
		}
	case "tui-mode":
		if callbacks.OnTuiModeChange != nil {
			callbacks.OnTuiModeChange(newValue)
		}
	case "fullscreen-exit-output":
		if callbacks.OnFullscreenExitOutputChange != nil {
			callbacks.OnFullscreenExitOutputChange(FullscreenExitOutput(newValue))
		}
	case "fullscreen-scrollbar":
		if callbacks.OnFullscreenScrollbarChange != nil {
			callbacks.OnFullscreenScrollbarChange(newValue)
		}
	case "fullscreen-copy-on-select":
		if callbacks.OnFullscreenCopyOnSelectChange != nil {
			callbacks.OnFullscreenCopyOnSelectChange(newValue == "true")
		}
	case "fullscreen-wheel-scroll-lines":
		if callbacks.OnFullscreenWheelScrollLinesChange != nil {
			callbacks.OnFullscreenWheelScrollLinesChange(parseWheelScrollLines(newValue))
		}
	case "theme":
		if callbacks.OnThemeChange != nil {
			callbacks.OnThemeChange(newValue)
		}
	}
}
