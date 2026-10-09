package codingagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os/exec"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/tui"
)

type settingsOracleModel struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Provider  string   `json:"provider"`
	Reasoning bool     `json:"reasoning"`
	API       string   `json:"api"`
	BaseURL   string   `json:"baseUrl"`
	Context   int      `json:"contextWindow"`
	MaxTokens int      `json:"maxTokens"`
	Input     []string `json:"input"`
}

// settingsOracleConfig is Pi's SettingsConfig by its own field names; both sides build from it.
type settingsOracleConfig struct {
	AutoCompact                bool                  `json:"autoCompact"`
	DefaultModel               string                `json:"defaultModel"`
	CurrentModel               *settingsOracleModel  `json:"currentModel,omitempty"`
	AvailableDefaultModels     []settingsOracleModel `json:"availableDefaultModels"`
	ShowImages                 bool                  `json:"showImages"`
	ImageWidthCells            int                   `json:"imageWidthCells"`
	AutoResizeImages           bool                  `json:"autoResizeImages"`
	BlockImages                bool                  `json:"blockImages"`
	EnableSkillCommands        bool                  `json:"enableSkillCommands"`
	SteeringMode               string                `json:"steeringMode"`
	FollowUpMode               string                `json:"followUpMode"`
	Transport                  string                `json:"transport"`
	HttpIdleTimeoutMs          int                   `json:"httpIdleTimeoutMs"`
	CacheWarmingMode           string                `json:"cacheWarmingMode"`
	ThinkingLevel              string                `json:"thinkingLevel"`
	AvailableThinkingLevels    []string              `json:"availableThinkingLevels"`
	ModelThinkingLevels        map[string]string     `json:"modelThinkingLevels"`
	CurrentTheme               string                `json:"currentTheme"`
	TerminalTheme              string                `json:"terminalTheme"`
	AvailableThemes            []string              `json:"availableThemes"`
	HideThinkingBlock          bool                  `json:"hideThinkingBlock"`
	MermaidRenderingMode       string                `json:"mermaidRenderingMode"`
	ShowCacheMissNotices       bool                  `json:"showCacheMissNotices"`
	CollapseChangelog          bool                  `json:"collapseChangelog"`
	EnableInstallTelemetry     bool                  `json:"enableInstallTelemetry"`
	DoubleEscapeAction         string                `json:"doubleEscapeAction"`
	TreeFilterMode             string                `json:"treeFilterMode"`
	ShowHardwareCursor         bool                  `json:"showHardwareCursor"`
	EditorPaddingX             int                   `json:"editorPaddingX"`
	OutputPad                  int                   `json:"outputPad"`
	AutocompleteMaxVisible     int                   `json:"autocompleteMaxVisible"`
	QuietStartup               json.RawMessage       `json:"quietStartup"`
	DefaultProjectTrust        string                `json:"defaultProjectTrust"`
	ClearOnShrink              bool                  `json:"clearOnShrink"`
	ShowTerminalProgress       bool                  `json:"showTerminalProgress"`
	TuiMode                    string                `json:"tuiMode"`
	FullscreenExitOutput       string                `json:"fullscreenExitOutput"`
	FullscreenScrollbar        string                `json:"fullscreenScrollbar"`
	FullscreenCopyOnSelect     bool                  `json:"fullscreenCopyOnSelect"`
	FullscreenWheelScrollLines json.RawMessage       `json:"fullscreenWheelScrollLines"`
	Warnings                   json.RawMessage       `json:"warnings"`
	MaskSecretInput            bool                  `json:"maskSecretInput"`
}

type settingsOracleProbe struct {
	Theme  string               `json:"theme"`
	Width  int                  `json:"width"`
	Images bool                 `json:"images"`
	Config settingsOracleConfig `json:"config"`
	Keys   []string             `json:"keys"`
}

type settingsOracleResult struct {
	Events [][]any    `json:"events"`
	Steps  []int      `json:"steps"`
	Frames [][]string `json:"frames"`
}

func (c settingsOracleConfig) goModel(m settingsOracleModel) *ai.Model {
	return &ai.Model{ID: m.ID, DisplayName: m.Name, ProviderMeta: ai.ProviderMetadata{ProviderID: m.Provider, Reasoning: m.Reasoning}}
}

func (c settingsOracleConfig) settingsConfig(t *testing.T) SettingsConfig {
	t.Helper()
	config := SettingsConfig{
		AutoCompact: c.AutoCompact, DefaultModel: c.DefaultModel, ShowImages: c.ShowImages, ImageWidthCells: c.ImageWidthCells,
		AutoResizeImages: c.AutoResizeImages, BlockImages: c.BlockImages, EnableSkillCommands: c.EnableSkillCommands,
		SteeringMode: c.SteeringMode, FollowUpMode: c.FollowUpMode, Transport: c.Transport, HttpIdleTimeoutMs: c.HttpIdleTimeoutMs,
		CacheWarmingMode: CacheWarmingMode(c.CacheWarmingMode), ThinkingLevel: c.ThinkingLevel, AvailableThinkingLevels: c.AvailableThinkingLevels,
		ModelThinkingLevels: c.ModelThinkingLevels, CurrentTheme: c.CurrentTheme, TerminalTheme: tui.TerminalTheme(c.TerminalTheme),
		AvailableThemes: c.AvailableThemes, HideThinkingBlock: c.HideThinkingBlock, MermaidRenderingMode: MermaidRenderingMode(c.MermaidRenderingMode),
		ShowCacheMissNotices: c.ShowCacheMissNotices, CollapseChangelog: c.CollapseChangelog, EnableInstallTelemetry: c.EnableInstallTelemetry,
		DoubleEscapeAction: c.DoubleEscapeAction, TreeFilterMode: c.TreeFilterMode, ShowHardwareCursor: c.ShowHardwareCursor,
		EditorPaddingX: c.EditorPaddingX, OutputPad: OutputPad(c.OutputPad), AutocompleteMaxVisible: c.AutocompleteMaxVisible,
		DefaultProjectTrust: c.DefaultProjectTrust, ClearOnShrink: c.ClearOnShrink, ShowTerminalProgress: c.ShowTerminalProgress,
		TuiMode: c.TuiMode, FullscreenExitOutput: FullscreenExitOutput(c.FullscreenExitOutput), FullscreenScrollbar: c.FullscreenScrollbar,
		FullscreenCopyOnSelect: c.FullscreenCopyOnSelect, MaskSecretInput: c.MaskSecretInput,
	}
	if c.CurrentModel != nil {
		config.CurrentModel = c.goModel(*c.CurrentModel)
	}
	for _, m := range c.AvailableDefaultModels {
		config.AvailableDefaultModels = append(config.AvailableDefaultModels, c.goModel(m))
	}
	if err := json.Unmarshal(c.QuietStartup, &config.QuietStartup); err != nil {
		t.Fatal(err)
	}
	var wheel any
	_ = json.Unmarshal(c.FullscreenWheelScrollLines, &wheel)
	if s, ok := wheel.(string); ok && s == "auto" {
		config.FullscreenWheelScrollLines = WheelScrollLines{Auto: true}
	} else {
		config.FullscreenWheelScrollLines = WheelScrollLines{Lines: wheel.(float64)}
	}
	if err := json.Unmarshal(c.Warnings, &config.Warnings); err != nil {
		t.Fatal(err)
	}
	return config
}

func settingsOracleCallbacks(record func(name string, args ...any)) SettingsCallbacks {
	json2 := func(v any) any { out, _ := json.Marshal(v); var back any; _ = json.Unmarshal(out, &back); return back }
	return SettingsCallbacks{
		OnAutoCompactChange:            func(v bool) { record("onAutoCompactChange", v) },
		OnShowImagesChange:             func(v bool) { record("onShowImagesChange", v) },
		OnImageWidthCellsChange:        func(v int) { record("onImageWidthCellsChange", v) },
		OnAutoResizeImagesChange:       func(v bool) { record("onAutoResizeImagesChange", v) },
		OnBlockImagesChange:            func(v bool) { record("onBlockImagesChange", v) },
		OnEnableSkillCommandsChange:    func(v bool) { record("onEnableSkillCommandsChange", v) },
		OnSteeringModeChange:           func(v string) { record("onSteeringModeChange", v) },
		OnFollowUpModeChange:           func(v string) { record("onFollowUpModeChange", v) },
		OnTransportChange:              func(v string) { record("onTransportChange", v) },
		OnHttpIdleTimeoutMsChange:      func(v int) { record("onHttpIdleTimeoutMsChange", v) },
		OnCacheWarmingModeChange:       func(v CacheWarmingMode) { record("onCacheWarmingModeChange", string(v)) },
		OnModelThinkingLevelChange:     func(p, m, l string) { record("onModelThinkingLevelChange", p, m, l) },
		OnModelThinkingLevelRemove:     func(p, m string) { record("onModelThinkingLevelRemove", p, m) },
		OnThemeChange:                  func(v string) { record("onThemeChange", v) },
		OnThemePreview:                 func(v string) { record("onThemePreview", v) },
		OnHideThinkingBlockChange:      func(v bool) { record("onHideThinkingBlockChange", v) },
		OnMermaidRenderingModeChange:   func(v MermaidRenderingMode) { record("onMermaidRenderingModeChange", string(v)) },
		OnShowCacheMissNoticesChange:   func(v bool) { record("onShowCacheMissNoticesChange", v) },
		OnCollapseChangelogChange:      func(v bool) { record("onCollapseChangelogChange", v) },
		OnEnableInstallTelemetryChange: func(v bool) { record("onEnableInstallTelemetryChange", v) },
		OnDoubleEscapeActionChange:     func(v string) { record("onDoubleEscapeActionChange", v) },
		OnTreeFilterModeChange:         func(v string) { record("onTreeFilterModeChange", v) },
		OnShowHardwareCursorChange:     func(v bool) { record("onShowHardwareCursorChange", v) },
		OnEditorPaddingXChange:         func(v int) { record("onEditorPaddingXChange", v) },
		OnOutputPadChange:              func(v OutputPad) { record("onOutputPadChange", int(v)) },
		OnAutocompleteMaxVisibleChange: func(v int) { record("onAutocompleteMaxVisibleChange", v) },
		OnQuietStartupChange:           func(v QuietStartup) { record("onQuietStartupChange", json2(v)) },
		OnDefaultProjectTrustChange:    func(v string) { record("onDefaultProjectTrustChange", v) },
		OnClearOnShrinkChange:          func(v bool) { record("onClearOnShrinkChange", v) },
		OnShowTerminalProgressChange:   func(v bool) { record("onShowTerminalProgressChange", v) },
		OnTuiModeChange:                func(v string) { record("onTuiModeChange", v) },
		OnFullscreenExitOutputChange:   func(v FullscreenExitOutput) { record("onFullscreenExitOutputChange", string(v)) },
		OnFullscreenScrollbarChange:    func(v string) { record("onFullscreenScrollbarChange", v) },
		OnFullscreenCopyOnSelectChange: func(v bool) { record("onFullscreenCopyOnSelectChange", v) },
		OnFullscreenWheelScrollLinesChange: func(v WheelScrollLines) {
			if v.Auto {
				record("onFullscreenWheelScrollLinesChange", "auto")
			} else {
				record("onFullscreenWheelScrollLinesChange", v.Lines)
			}
		},
		OnWarningsChange:        func(v WarningSettings) { record("onWarningsChange", json2(v)) },
		OnMaskSecretInputChange: func(v bool) { record("onMaskSecretInputChange", v) },
		OnCancel:                func() { record("onCancel") },
	}
}

func settingsOracleBase() settingsOracleConfig {
	models := []settingsOracleModel{
		{ID: "alpha-1", Name: "Alpha One", Provider: "alpha", Reasoning: true, API: "openai-completions", BaseURL: "https://a", Context: 100000, MaxTokens: 8000, Input: []string{"text"}},
		{ID: "beta-2", Name: "Beta Two", Provider: "beta", Reasoning: false, API: "openai-completions", BaseURL: "https://b", Context: 100000, MaxTokens: 8000, Input: []string{"text"}},
		{ID: "gamma-3", Name: "Gamma Three", Provider: "alpha", Reasoning: true, API: "openai-completions", BaseURL: "https://a", Context: 100000, MaxTokens: 8000, Input: []string{"text"}},
	}
	return settingsOracleConfig{
		AutoCompact: true, DefaultModel: "alpha/alpha-1", CurrentModel: &models[1], AvailableDefaultModels: models,
		ShowImages: true, ImageWidthCells: 60, AutoResizeImages: true, BlockImages: false, EnableSkillCommands: true,
		SteeringMode: "one-at-a-time", FollowUpMode: "all", Transport: "auto", HttpIdleTimeoutMs: 300000, CacheWarmingMode: "streaming",
		ThinkingLevel: "medium", AvailableThinkingLevels: []string{"off", "minimal", "low", "medium", "high"},
		ModelThinkingLevels: map[string]string{"alpha/alpha-1": "high"}, CurrentTheme: "dark", TerminalTheme: "dark",
		AvailableThemes: []string{"dark", "light", "system"}, HideThinkingBlock: false, MermaidRenderingMode: "ascii",
		ShowCacheMissNotices: true, CollapseChangelog: false, EnableInstallTelemetry: true, DoubleEscapeAction: "tree",
		TreeFilterMode: "default", ShowHardwareCursor: false, EditorPaddingX: 1, OutputPad: 1, AutocompleteMaxVisible: 5,
		QuietStartup: json.RawMessage(`false`), DefaultProjectTrust: "ask", ClearOnShrink: false, ShowTerminalProgress: true,
		TuiMode: "inline", FullscreenExitOutput: "keep", FullscreenScrollbar: "auto", FullscreenCopyOnSelect: true,
		FullscreenWheelScrollLines: json.RawMessage(`"auto"`), Warnings: json.RawMessage(`{"anthropicExtraUsage":true}`), MaskSecretInput: true,
	}
}

// settings-selector.ts against pinned Pi: the whole /settings screen (every row, its cycling values, submenus for theme, warnings
// and per-model thinking, search typing, cancel) driven by seeded key sequences; every callback with its arguments and every frame
// must agree. Pi has no "Mask secret input" row (D80), so the oracle driver inserts that one row into its own copy of the module.
func TestSettingsSelectorMatchesPi(t *testing.T) {
	base := settingsOracleBase()
	variants := []settingsOracleConfig{base}
	{
		v := settingsOracleBase()
		v.CurrentTheme, v.TerminalTheme, v.QuietStartup, v.FullscreenWheelScrollLines = "light/dark", "light", json.RawMessage(`"header"`), json.RawMessage(`7`)
		v.ThinkingLevel, v.ModelThinkingLevels, v.Warnings, v.DefaultProjectTrust = "off", map[string]string{"alpha/alpha-1": "low", "alpha/gamma-3": "xhigh"}, json.RawMessage(`{"anthropicExtraUsage":false}`), "never"
		v.CurrentModel, v.TuiMode, v.MaskSecretInput = nil, "fullscreen", false
		variants = append(variants, v)
	}
	{
		v := settingsOracleBase()
		v.AvailableDefaultModels, v.CurrentModel, v.DefaultModel, v.ModelThinkingLevels = []settingsOracleModel{}, nil, "", map[string]string{}
		v.CurrentTheme, v.AutoCompact, v.ShowImages, v.QuietStartup = "system", false, false, json.RawMessage(`true`)
		v.Warnings = json.RawMessage(`{}`)
		variants = append(variants, v)
	}
	alphabet := []string{"\x1b[A", "\x1b[A", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\r", "\r", "\r", "\r", "\x1b", " ", "\x1b[C", "\x1b[D", "\x1b[6~", "\x1b[5~", "\x7f", "t", "h", "e", "m", "a", "l", "p", "d", "\n", "\x03", "\t"}
	rng := rand.New(rand.NewSource(20261008))
	var probes []settingsOracleProbe
	for _, theme := range []string{"dark", "light"} {
		for vi, config := range variants {
			for _, images := range []bool{true, false} {
				for s := range 24 {
					keys := make([]string, 12+rng.Intn(40))
					for i := range keys {
						keys[i] = alphabet[rng.Intn(len(alphabet))]
					}
					if s%6 == 0 { // walk straight down every row once
						keys = append([]string{}, make([]string, 0)...)
						for range 45 {
							keys = append(keys, "\x1b[B", "\r")
						}
					}
					probes = append(probes, settingsOracleProbe{Theme: theme, Width: 100, Images: images, Config: config, Keys: keys})
					_ = vi
				}
			}
		}
	}
	// Open each row in turn and wander inside it: submenus (theme single and automatic, warnings, per-model thinking) need depth.
	inner := []string{"\x1b[A", "\x1b[B", "\x1b[B", "\r", "\r", "\x1b", " ", "\x1b[C", "\x7f", "s", "y", "l", "a"}
	for _, config := range variants {
		for row := range 48 {
			for range 2 {
				keys := slices.Repeat([]string{"\x1b[B"}, row)
				keys = append(keys, "\r")
				for range 4 + rng.Intn(10) {
					keys = append(keys, inner[rng.Intn(len(inner))])
				}
				probes = append(probes, settingsOracleProbe{Theme: "dark", Width: 100, Images: true, Config: config, Keys: keys})
			}
		}
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/settings_selector.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []settingsOracleResult
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	previousTheme, previousCaps, previousKitty := tui.ActiveTheme(), tui.GetCapabilities(), tui.IsKittyProtocolActive()
	tui.SetKittyProtocolActive(false)
	t.Cleanup(func() {
		tui.SetCapabilities(previousCaps)
		tui.SetTheme(previousTheme.Name)
		tui.SetKittyProtocolActive(previousKitty)
	})
	reached := map[string]int{}
	for _, result := range expected {
		for _, event := range result.Events {
			reached[event[0].(string)]++
		}
	}
	t.Logf("callbacks reached by Pi: %v", reached)
	failures := 0
	for i, probe := range probes {
		caps := tui.TerminalCapabilities{TrueColor: true}
		if probe.Images {
			caps.Images = "kitty"
		}
		tui.SetCapabilities(caps)
		tui.SetTheme(probe.Theme)
		useKeybindings(t, nil)
		var events [][]any
		record := func(name string, args ...any) {
			event := []any{name}
			for _, arg := range args {
				out, _ := json.Marshal(arg)
				var back any
				_ = json.Unmarshal(out, &back)
				event = append(event, back)
			}
			events = append(events, event)
		}
		selector := NewSettingsSelectorComponent(probe.Config.settingsConfig(t), settingsOracleCallbacks(record))
		list := selector.GetSettingsList()
		got := settingsOracleResult{Steps: []int{len(events)}, Frames: [][]string{selector.Render(probe.Width)}}
		for _, key := range probe.Keys {
			list.HandleInput(key)
			got.Steps = append(got.Steps, len(events))
			got.Frames = append(got.Frames, selector.Render(probe.Width))
		}
		got.Events = events
		want := expected[i]
		eventsEqual := reflect.DeepEqual(got.Events, want.Events) || (len(got.Events) == 0 && len(want.Events) == 0)
		if !eventsEqual || !reflect.DeepEqual(got.Steps, want.Steps) || !reflect.DeepEqual(got.Frames, want.Frames) {
			failures++
			if failures <= 4 {
				t.Errorf("probe %d theme=%s images=%v variant keys=%d:\nevents differ=%v steps differ=%v\n%s", i, probe.Theme, probe.Images, len(probe.Keys), !eventsEqual, !reflect.DeepEqual(got.Steps, want.Steps), firstFrameDifference(got.Frames, want.Frames))
				if !eventsEqual {
					for e := range min(len(got.Events), len(want.Events)) {
						if !reflect.DeepEqual(got.Events[e], want.Events[e]) {
							t.Errorf("first differing event %d: Pig %v, Pi %v", e, got.Events[e], want.Events[e])
							break
						}
					}
				}
			}
		}
	}
	if failures > 4 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
	_ = fmt.Sprint
	_ = strings.TrimSpace
}
