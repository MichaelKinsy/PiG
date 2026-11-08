package prefs

// Server-side TUI defaults (p2-tui-port.md section 4.2). Guest-local preferences from the launcher override them; privileged-action bindings never
// come from the server. Types and validation only.
//
// pi: apps/p2/src/runtime/tui-preferences.ts (TuiSettingsSchema and its Static type; the Chord contract `TuiPreferences` is in contract.go)

import (
	"math"
	"strings"
)

// PluginSettingValue is one plugin setting's value: a bool, a number or a string of at most 200 characters. The launcher never runs plugin code:
// this shape is all it checks.
type PluginSettingValue any

// QuietStartup is `quietStartup`: a bool, or "header".
type QuietStartup struct {
	// Header is true for the value "header"; otherwise On is the bool.
	Header bool
	On     bool
}

// Images is `terminal.images`: "kitty", "iterm2" or false.
type Images struct {
	// Protocol is "kitty" or "iterm2"; "" is `false`.
	Protocol string
}

// WheelLines is `fullscreenWheelScrollLines`: "auto" or an integer 1-100.
type WheelLines struct {
	// Auto is true for "auto"; otherwise Lines is the count.
	Auto  bool
	Lines int
}

// TerminalSettings is the `terminal` object of TuiSettings.
type TerminalSettings struct {
	ClearOnShrink        *bool   `json:"clearOnShrink,omitempty"`
	ShowTerminalProgress *bool   `json:"showTerminalProgress,omitempty"`
	ShowImages           *bool   `json:"showImages,omitempty"`
	ImageWidthCells      *int    `json:"imageWidthCells,omitempty"`
	Hyperlinks           *bool   `json:"hyperlinks,omitempty"`
	Images               *Images `json:"images,omitempty"`
	TrueColor            *bool   `json:"trueColor,omitempty"`
}

// MarkdownSettings is the `markdown` object of TuiSettings.
type MarkdownSettings struct {
	CodeBlockIndent *string `json:"codeBlockIndent,omitempty"`
	Mermaid         *string `json:"mermaid,omitempty"`
}

// TuiSettings is TuiSettingsSchema's Static type: pi `settings.json` keys marked G in section 1.14, spelled (and nested) as pi spells them, plus
// p2's plugin settings. A nil pointer is an absent key.
type TuiSettings struct {
	QuietStartup               *QuietStartup                           `json:"quietStartup,omitempty"`
	EditorPaddingX             *int                                    `json:"editorPaddingX,omitempty"`
	OutputPad                  *int                                    `json:"outputPad,omitempty"`
	AutocompleteMaxVisible     *int                                    `json:"autocompleteMaxVisible,omitempty"`
	ShowHardwareCursor         *bool                                   `json:"showHardwareCursor,omitempty"`
	HideThinkingBlock          *bool                                   `json:"hideThinkingBlock,omitempty"`
	DoubleEscapeAction         *string                                 `json:"doubleEscapeAction,omitempty"`
	Terminal                   *TerminalSettings                       `json:"terminal,omitempty"`
	Markdown                   *MarkdownSettings                       `json:"markdown,omitempty"`
	FullscreenExitOutput       *string                                 `json:"fullscreenExitOutput,omitempty"`
	FullscreenScrollbar        *string                                 `json:"fullscreenScrollbar,omitempty"`
	FullscreenCopyOnSelect     *bool                                   `json:"fullscreenCopyOnSelect,omitempty"`
	FullscreenWheelScrollLines *WheelLines                             `json:"fullscreenWheelScrollLines,omitempty"`
	Plugins                    map[string]map[string]PluginSettingValue `json:"plugins,omitempty"`
}

// TuiSettingField is a plugin's row in `/settings`: the values it cycles and the value it means when unset. It is stored at
// `plugins.<plugin>.<name>` of the visitor's settings; read it with PluginSetting.
type TuiSettingField struct {
	Plugin      string
	Name        string
	Label       string
	Description string
	Values      []PluginSettingValue
	Fallback    PluginSettingValue
}

// PluginSetting is pluginSetting: a plugin setting's value, or the field's fallback when unset or not one of its values.
func PluginSetting(settings *TuiSettings, field TuiSettingField) PluginSettingValue {
	if settings != nil {
		if value, ok := settings.Plugins[field.Plugin][field.Name]; ok {
			for _, candidate := range field.Values {
				if candidate == value {
					return value
				}
			}
		}
	}
	return field.Fallback
}

// TuiServerPreferences is TuiPreferencesSchema's Static type: what the server serves as TUI defaults.
type TuiServerPreferences struct {
	Keybindings map[string][]string `json:"keybindings"`
	// Theme is a pi theme setting: a built-in name, "system" or a "light/dark" pair. Custom theme files are guest-local.
	Theme    string      `json:"theme,omitempty"`
	Settings TuiSettings `json:"settings"`
}

func isObject(value any) (map[string]any, bool) {
	object, ok := value.(map[string]any)
	return object, ok
}

func asBool(value any) (*bool, bool) {
	b, ok := value.(bool)
	if !ok {
		return nil, false
	}
	return &b, true
}

// asInt is a pi-schema int(min, max): a JSON number with no fraction within the bounds.
func asInt(value any, minimum, maximum int) (*int, bool) {
	n, ok := value.(float64)
	if !ok || n != math.Trunc(n) || n < float64(minimum) || n > float64(maximum) {
		return nil, false
	}
	i := int(n)
	return &i, true
}

func asEnum(value any, allowed ...string) (*string, bool) {
	s, ok := value.(string)
	if !ok {
		return nil, false
	}
	for _, candidate := range allowed {
		if s == candidate {
			return &s, true
		}
	}
	return nil, false
}

// settingsKeys is TuiSettingsSchema's properties in declaration order.
var settingsKeys = []string{
	"quietStartup", "editorPaddingX", "outputPad", "autocompleteMaxVisible", "showHardwareCursor", "hideThinkingBlock", "doubleEscapeAction",
	"terminal", "markdown", "fullscreenExitOutput", "fullscreenScrollbar", "fullscreenCopyOnSelect", "fullscreenWheelScrollLines", "plugins",
}

func jsLength(text string) int {
	n := 0
	for _, r := range text {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// terminalChildren are the properties of the `terminal` object.
var terminalChildren = []string{"clearOnShrink", "showTerminalProgress", "showImages", "imageWidthCells", "hyperlinks", "images", "trueColor"}

func (s *TerminalSettings) set(name string, value any) bool {
	switch name {
	case "clearOnShrink":
		b, ok := asBool(value)
		s.ClearOnShrink = b
		return ok
	case "showTerminalProgress":
		b, ok := asBool(value)
		s.ShowTerminalProgress = b
		return ok
	case "showImages":
		b, ok := asBool(value)
		s.ShowImages = b
		return ok
	case "imageWidthCells":
		i, ok := asInt(value, 1, 500)
		s.ImageWidthCells = i
		return ok
	case "hyperlinks":
		b, ok := asBool(value)
		s.Hyperlinks = b
		return ok
	case "images":
		if b, isBool := value.(bool); isBool && !b {
			s.Images = &Images{}
			return true
		}
		e, ok := asEnum(value, "kitty", "iterm2")
		if ok {
			s.Images = &Images{Protocol: *e}
		}
		return ok
	case "trueColor":
		b, ok := asBool(value)
		s.TrueColor = b
		return ok
	}
	return false
}

func (s *MarkdownSettings) set(name string, value any) bool {
	switch name {
	case "codeBlockIndent":
		text, ok := value.(string)
		if !ok || jsLength(text) > 8 || strings.Trim(text, " \t") != "" {
			return false
		}
		s.CodeBlockIndent = &text
		return true
	case "mermaid":
		e, ok := asEnum(value, "off", "final", "streaming")
		s.Mermaid = e
		return ok
	}
	return false
}

func validPlugins(value any) (map[string]map[string]PluginSettingValue, bool) {
	object, ok := isObject(value)
	if !ok || len(object) > 64 {
		return nil, false
	}
	plugins := make(map[string]map[string]PluginSettingValue, len(object))
	for name, raw := range object {
		inner, ok := isObject(raw)
		if !ok || len(inner) > 32 {
			return nil, false
		}
		values := make(map[string]PluginSettingValue, len(inner))
		for setting, item := range inner {
			switch typed := item.(type) {
			case bool, float64:
				values[setting] = typed
			case string:
				if jsLength(typed) > 200 {
					return nil, false
				}
				values[setting] = typed
			default:
				return nil, false
			}
		}
		plugins[name] = values
	}
	return plugins, true
}

// setSetting validates and stores one top-level non-object key; false means the value is invalid.
func (s *TuiSettings) setSetting(key string, value any) bool {
	switch key {
	case "quietStartup":
		if b, ok := value.(bool); ok {
			s.QuietStartup = &QuietStartup{On: b}
			return true
		}
		if text, ok := value.(string); ok && text == "header" {
			s.QuietStartup = &QuietStartup{Header: true}
			return true
		}
		return false
	case "editorPaddingX":
		i, ok := asInt(value, 0, 3)
		s.EditorPaddingX = i
		return ok
	case "outputPad":
		i, ok := asInt(value, 0, 1)
		s.OutputPad = i
		return ok
	case "autocompleteMaxVisible":
		i, ok := asInt(value, 3, 20)
		s.AutocompleteMaxVisible = i
		return ok
	case "showHardwareCursor":
		b, ok := asBool(value)
		s.ShowHardwareCursor = b
		return ok
	case "hideThinkingBlock":
		b, ok := asBool(value)
		s.HideThinkingBlock = b
		return ok
	case "doubleEscapeAction":
		e, ok := asEnum(value, "tree", "fork", "none")
		s.DoubleEscapeAction = e
		return ok
	case "fullscreenExitOutput":
		e, ok := asEnum(value, "transcript", "resume-hint")
		s.FullscreenExitOutput = e
		return ok
	case "fullscreenScrollbar":
		e, ok := asEnum(value, "auto", "always", "hidden")
		s.FullscreenScrollbar = e
		return ok
	case "fullscreenCopyOnSelect":
		b, ok := asBool(value)
		s.FullscreenCopyOnSelect = b
		return ok
	case "fullscreenWheelScrollLines":
		if text, ok := value.(string); ok && text == "auto" {
			s.FullscreenWheelScrollLines = &WheelLines{Auto: true}
			return true
		}
		i, ok := asInt(value, 1, 100)
		if ok {
			s.FullscreenWheelScrollLines = &WheelLines{Lines: *i}
		}
		return ok
	case "plugins":
		plugins, ok := validPlugins(value)
		s.Plugins = plugins
		return ok
	}
	return false
}

// SanitizeSettings is sanitizeSettings: it keeps only whitelisted, well-typed settings; invalid keys are reported by name (`terminal.showImages`).
// raw is a decoded JSON value.
func SanitizeSettings(raw any) (settings TuiSettings, invalid []string) {
	object, ok := isObject(raw)
	if !ok {
		return settings, invalid
	}
	for _, key := range settingsKeys {
		value, present := object[key]
		if !present {
			continue
		}
		switch key {
		case "terminal":
			if inner, isObj := isObject(value); isObj {
				var terminal TerminalSettings
				count := 0
				for _, name := range terminalChildren {
					child, has := inner[name]
					if !has {
						continue
					}
					if terminal.set(name, child) {
						count++
					} else {
						invalid = append(invalid, key+"."+name)
					}
				}
				if count > 0 {
					settings.Terminal = &terminal
				}
			} else {
				invalid = append(invalid, key)
			}
		case "markdown":
			if inner, isObj := isObject(value); isObj {
				var markdown MarkdownSettings
				count := 0
				for _, name := range []string{"codeBlockIndent", "mermaid"} {
					child, has := inner[name]
					if !has {
						continue
					}
					if markdown.set(name, child) {
						count++
					} else {
						invalid = append(invalid, key+"."+name)
					}
				}
				if count > 0 {
					settings.Markdown = &markdown
				}
			} else {
				invalid = append(invalid, key)
			}
		default:
			if !settings.setSetting(key, value) {
				invalid = append(invalid, key)
			}
		}
	}
	return settings, invalid
}

// MergeSettings is mergeSettings: later sources override earlier ones; nested `terminal`/`markdown` objects merge per key, `plugins` per setting.
func MergeSettings(layers ...TuiSettings) TuiSettings {
	var merged TuiSettings
	for _, layer := range layers {
		terminal, markdown, plugins := layer.Terminal, layer.Markdown, layer.Plugins
		rest := layer
		rest.Terminal, rest.Markdown, rest.Plugins = nil, nil, nil
		mergeTopLevel(&merged, rest)
		if terminal != nil {
			next := TerminalSettings{}
			if merged.Terminal != nil {
				next = *merged.Terminal
			}
			mergeTerminal(&next, *terminal)
			merged.Terminal = &next
		}
		if markdown != nil {
			next := MarkdownSettings{}
			if merged.Markdown != nil {
				next = *merged.Markdown
			}
			if markdown.CodeBlockIndent != nil {
				next.CodeBlockIndent = markdown.CodeBlockIndent
			}
			if markdown.Mermaid != nil {
				next.Mermaid = markdown.Mermaid
			}
			merged.Markdown = &next
		}
		if plugins != nil {
			all := make(map[string]map[string]PluginSettingValue, len(merged.Plugins)+len(plugins))
			for name, values := range merged.Plugins {
				all[name] = values
			}
			for name, values := range plugins {
				combined := make(map[string]PluginSettingValue, len(all[name])+len(values))
				for setting, value := range all[name] {
					combined[setting] = value
				}
				for setting, value := range values {
					combined[setting] = value
				}
				all[name] = combined
			}
			merged.Plugins = all
		}
	}
	return merged
}

func mergeTopLevel(dst *TuiSettings, src TuiSettings) {
	if src.QuietStartup != nil {
		dst.QuietStartup = src.QuietStartup
	}
	if src.EditorPaddingX != nil {
		dst.EditorPaddingX = src.EditorPaddingX
	}
	if src.OutputPad != nil {
		dst.OutputPad = src.OutputPad
	}
	if src.AutocompleteMaxVisible != nil {
		dst.AutocompleteMaxVisible = src.AutocompleteMaxVisible
	}
	if src.ShowHardwareCursor != nil {
		dst.ShowHardwareCursor = src.ShowHardwareCursor
	}
	if src.HideThinkingBlock != nil {
		dst.HideThinkingBlock = src.HideThinkingBlock
	}
	if src.DoubleEscapeAction != nil {
		dst.DoubleEscapeAction = src.DoubleEscapeAction
	}
	if src.FullscreenExitOutput != nil {
		dst.FullscreenExitOutput = src.FullscreenExitOutput
	}
	if src.FullscreenScrollbar != nil {
		dst.FullscreenScrollbar = src.FullscreenScrollbar
	}
	if src.FullscreenCopyOnSelect != nil {
		dst.FullscreenCopyOnSelect = src.FullscreenCopyOnSelect
	}
	if src.FullscreenWheelScrollLines != nil {
		dst.FullscreenWheelScrollLines = src.FullscreenWheelScrollLines
	}
}

func mergeTerminal(dst *TerminalSettings, src TerminalSettings) {
	if src.ClearOnShrink != nil {
		dst.ClearOnShrink = src.ClearOnShrink
	}
	if src.ShowTerminalProgress != nil {
		dst.ShowTerminalProgress = src.ShowTerminalProgress
	}
	if src.ShowImages != nil {
		dst.ShowImages = src.ShowImages
	}
	if src.ImageWidthCells != nil {
		dst.ImageWidthCells = src.ImageWidthCells
	}
	if src.Hyperlinks != nil {
		dst.Hyperlinks = src.Hyperlinks
	}
	if src.Images != nil {
		dst.Images = src.Images
	}
	if src.TrueColor != nil {
		dst.TrueColor = src.TrueColor
	}
}
