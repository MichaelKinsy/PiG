package modeltypes

import (
	"fmt"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// colorFrom reads a pi-tui Color from its JSON object.
func colorFrom(value map[string]any) (sdk.Color, error) {
	number := func(key string) float64 { v, _ := value[key].(float64); return v }
	switch value["kind"] {
	case "indexed":
		return sdk.IndexedColor{Index: int(number("index"))}, nil
	case "rgb":
		return sdk.RgbColorValue{R: number("r"), G: number("g"), B: number("b")}, nil
	case "oklch":
		return sdk.OklchColorValue{L: number("l"), C: number("c"), H: number("h")}, nil
	}
	return nil, fmt.Errorf("unknown color kind %v", value["kind"])
}

// colorJSON is colorFrom's inverse.
func colorJSON(color sdk.Color) map[string]any {
	switch color := color.(type) {
	case sdk.IndexedColor:
		return map[string]any{"kind": "indexed", "index": color.Index}
	case sdk.RgbColorValue:
		return map[string]any{"kind": "rgb", "r": color.R, "g": color.G, "b": color.B}
	case sdk.OklchColorValue:
		return map[string]any{"kind": "oklch", "l": color.L, "c": color.C, "h": color.H}
	}
	return nil
}

// themeStyleFrom reads upstream's ThemeStyle from its JSON object: fg and bg are a token or a Color.
func themeStyleFrom(options map[string]any) (sdk.ThemeStyle, error) {
	var style sdk.ThemeStyle
	for key, attribute := range map[string]*bool{"bold": &style.Bold, "dim": &style.Dim, "italic": &style.Italic, "underline": &style.Underline, "inverse": &style.Inverse, "strikethrough": &style.Strikethrough} {
		*attribute, _ = options[key].(bool)
	}
	slot := func(key string, token *string, color *sdk.Color) error {
		switch value := options[key].(type) {
		case string:
			*token = value
		case map[string]any:
			parsed, err := colorFrom(value)
			*color = parsed
			return err
		}
		return nil
	}
	if err := slot("fg", &style.FgToken, &style.Fg); err != nil {
		return style, err
	}
	return style, slot("bg", &style.BgToken, &style.Bg)
}

func registerThemeProbe(e *sdk.Extension) {
	e.RegisterTool(sdk.ToolDefinition{
		Name: "theme_probe", Label: "theme_probe", Description: "Reads the host's theme through ctx.ui.theme.", Parameters: sdk.Schema{"type": "object"},
		Execute: func(ctx sdk.Context, args map[string]any) (any, error) {
			theme := ctx.UITheme()
			styles := []any{}
			cases, _ := args["cases"].([]any)
			for _, raw := range cases {
				options, _ := raw.(map[string]any)
				style, err := themeStyleFrom(options)
				if err != nil {
					return nil, err
				}
				if out, err := theme.Style("x", style); err != nil {
					styles = append(styles, map[string]any{"error": err.Error()})
				} else {
					styles = append(styles, map[string]any{"ok": out})
				}
			}
			colors := map[string]any{}
			tokens, _ := args["tokens"].([]any)
			for _, raw := range tokens {
				token, _ := raw.(string)
				if color, ok := theme.Colors()[token]; ok {
					colors[token] = colorJSON(color)
				} else {
					colors[token] = nil
				}
			}
			fgs := map[string]any{}
			fgTokens, _ := args["fgTokens"].([]any)
			for _, raw := range fgTokens {
				token, _ := raw.(string)
				fgs[token] = themeFg(theme, token)
			}
			var appearance any
			if value := theme.Appearance(); value != "" {
				appearance = string(value)
			}
			return text(map[string]any{"appearance": appearance, "colors": colors, "styles": styles, "fgs": fgs, "queries": themeQueries(theme, args)})
		},
	})
}

// themeFg is theme.fg(token, "x"), or "throw:" and the message when the SDK raises, so one unknown token does not hide the others.
func themeFg(theme sdk.UITheme, token string) (result string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			result = fmt.Sprintf("throw:%v", recovered)
		}
	}()
	return theme.Fg(token, "x")
}

// attempt is fn(), or "throw:" and the message when the SDK raises or returns an error.
func attempt(fn func() (string, error)) (result string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			result = fmt.Sprintf("throw:%v", recovered)
		}
	}()
	value, err := fn()
	if err != nil {
		return "throw:" + err.Error()
	}
	return value
}

// themeQueries is the rest of ctx.ui.theme: modifiers, GetFgAnsi/GetBgAnsi/Bg for the tokens the test passes, GetColorMode, the border colors and SourcePath.
func themeQueries(theme sdk.UITheme, args map[string]any) any {
	if enabled, _ := args["queries"].(bool); !enabled {
		return nil
	}
	tokens, _ := args["ansiTokens"].([]any)
	byToken := func(fn func(token string) (string, error)) map[string]any {
		out := map[string]any{}
		for _, raw := range tokens {
			token, _ := raw.(string)
			out[token] = attempt(func() (string, error) { return fn(token) })
		}
		return out
	}
	thinking := map[string]any{}
	levels, _ := args["levels"].([]any)
	for _, raw := range levels {
		level, _ := raw.(string)
		thinking[level] = attempt(func() (string, error) { return theme.GetThinkingBorderColor(level)("x"), nil })
	}
	modifierTexts := map[string]any{}
	texts, _ := args["modifierTexts"].([]any)
	for name, modifier := range map[string]func(string) string{"bold": theme.Bold, "italic": theme.Italic, "underline": theme.Underline, "inverse": theme.Inverse, "strikethrough": theme.Strikethrough} {
		drawn := []any{}
		for _, raw := range texts {
			text, _ := raw.(string)
			drawn = append(drawn, modifier(text))
		}
		modifierTexts[name] = drawn
	}
	var sourcePath any
	if theme.SourcePath != "" {
		sourcePath = theme.SourcePath
	}
	// Theme.sourceInfo: where the resource loader found the theme, nil for one it did not load.
	var sourceInfo any
	if info := theme.SourceInfo; info != nil {
		fields := map[string]any{"path": info.Path, "source": info.Source, "scope": info.Scope, "origin": info.Origin}
		if info.BaseDir != "" {
			fields["baseDir"] = info.BaseDir
		}
		sourceInfo = fields
	}
	return map[string]any{
		"modifiers":     map[string]any{"bold": theme.Bold("x"), "italic": theme.Italic("x"), "underline": theme.Underline("x"), "inverse": theme.Inverse("x"), "strikethrough": theme.Strikethrough("x")},
		"modifierTexts": modifierTexts,
		"fgAnsi":        byToken(theme.GetFgAnsi),
		"bgAnsi":        byToken(theme.GetBgAnsi),
		"bgs":           byToken(func(token string) (string, error) { return theme.Bg(token, "x"), nil }),
		"mode":          theme.GetColorMode(),
		"thinking":      thinking,
		"bash":          attempt(func() (string, error) { return theme.GetBashModeBorderColor()("x"), nil }),
		"sourcePath":    sourcePath,
		"sourceInfo":    sourceInfo,
	}
}

func stringsOf(value any) []string {
	items, _ := value.([]any)
	out := make([]string, 0, len(items))
	for _, item := range items {
		text, _ := item.(string)
		out = append(out, text)
	}
	return out
}

func applyAll(style func(string) string, samples []string) []string {
	out := make([]string, 0, len(samples))
	for _, sample := range samples {
		out = append(out, style(sample))
	}
	return out
}

// registerTuiThemeProbe is the pi-coding-agent theme helpers (getMarkdownTheme, getSelectListTheme, getSettingsListTheme, highlightCode, initTheme) and keyHint family
// over the host's theme and keybindings: the styles of each on the samples the test passes, and the lines highlightCode gets from the host.
func registerTuiThemeProbe(e *sdk.Extension) {
	e.RegisterTool(sdk.ToolDefinition{
		Name: "tui_theme_probe", Label: "tui_theme_probe", Description: "Runs pi-coding-agent's theme and key hint helpers.", Parameters: sdk.Schema{"type": "object"},
		Execute: func(ctx sdk.Context, args map[string]any) (any, error) {
			samples := stringsOf(args["samples"])
			switch args["op"] {
			case "markdown":
				theme := sdk.GetMarkdownTheme()
				styles := map[string]any{
					"heading": applyAll(theme.Heading, samples), "link": applyAll(theme.Link, samples), "linkUrl": applyAll(theme.LinkUrl, samples),
					"code": applyAll(theme.Code, samples), "codeBlock": applyAll(theme.CodeBlock, samples), "codeBlockBorder": applyAll(theme.CodeBlockBorder, samples),
					"quote": applyAll(theme.Quote, samples), "quoteBorder": applyAll(theme.QuoteBorder, samples), "hr": applyAll(theme.Hr, samples),
					"listBullet": applyAll(theme.ListBullet, samples), "bold": applyAll(theme.Bold, samples), "italic": applyAll(theme.Italic, samples),
					"underline": applyAll(theme.Underline, samples), "strikethrough": applyAll(theme.Strikethrough, samples),
				}
				highlight := []any{}
				pairs, _ := args["highlight"].([]any)
				for _, pair := range pairs {
					entry, _ := pair.([]any)
					code, _ := entry[0].(string)
					lang, _ := entry[1].(string)
					highlight = append(highlight, theme.HighlightCode(code, lang))
				}
				return text(map[string]any{"styles": styles, "highlight": highlight})
			case "select":
				theme := sdk.GetSelectListTheme()
				return text(map[string]any{"styles": map[string]any{
					"selectedPrefix": applyAll(theme.SelectedPrefix, samples), "selectedText": applyAll(theme.SelectedText, samples), "description": applyAll(theme.Description, samples),
					"scrollInfo": applyAll(theme.ScrollInfo, samples), "noMatch": applyAll(theme.NoMatch, samples),
				}})
			case "settings":
				theme := sdk.GetSettingsListTheme()
				pairs := func(style func(string, bool) string) [][2]string {
					out := [][2]string{}
					for _, sample := range samples {
						out = append(out, [2]string{style(sample, false), style(sample, true)})
					}
					return out
				}
				return text(map[string]any{"styles": map[string]any{
					"label": pairs(theme.Label), "value": pairs(theme.Value), "description": applyAll(theme.Description, samples), "cursor": theme.Cursor, "hint": applyAll(theme.Hint, samples),
				}})
			case "highlight":
				if name, _ := args["theme"].(string); name != "" {
					if err := sdk.InitTheme(name); err != nil {
						return nil, err
					}
				}
				code, _ := args["code"].(string)
				lang, _ := args["lang"].(string)
				return text(map[string]any{"lines": sdk.HighlightCode(code, lang)})
			case "init_theme":
				name, _ := args["theme"].(string)
				if err := sdk.InitTheme(name); err != nil {
					return nil, err
				}
				theme := sdk.GetMarkdownTheme()
				return text(map[string]any{"bold": applyAll(theme.Bold, samples), "heading": applyAll(theme.Heading, samples)})
			case "key_hint":
				id, _ := args["id"].(string)
				description, _ := args["description"].(string)
				return text(map[string]any{"keyHint": sdk.KeyHint(id, description), "keyText": sdk.KeyText(id)})
			case "raw_key_hint":
				key, _ := args["key"].(string)
				description, _ := args["description"].(string)
				return text(map[string]any{"rawKeyHint": sdk.RawKeyHint(key, description)})
			}
			return nil, fmt.Errorf("unknown op %v", args["op"])
		},
	})
}
