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
				fgs[token] = theme.Fg(token, "x")
			}
			var appearance any
			if value := theme.Appearance(); value != "" {
				appearance = string(value)
			}
			return text(map[string]any{"appearance": appearance, "colors": colors, "styles": styles, "fgs": fgs})
		},
	})
}
