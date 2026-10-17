package configschema

import "github.com/MichaelKinsy/PiG/tui"

// Mirrors packages/coding-agent/src/modes/interactive/theme/theme-schema.ts.

func colorValue(description string) *Schema {
	var opts []Opt
	if description != "" {
		opts = []Opt{D(description)}
	}
	return Union([]*Schema{
		String(D("Hex color (#RGB or #RRGGBB), OKLCH or OKHSL color, variable reference, or empty string for terminal default")),
		Integer(Opt{"minimum", 0}, Opt{"maximum", 255}, D("256-color palette index (0-255)")),
	}, opts...)
}

func themeColorsSchema() *Schema {
	var props []Prop
	for _, token := range tui.ThemeColorTokens() {
		prop := Prop{Name: token.Name, Schema: colorValue(token.Description), Optional: token.Optional}
		props = append(props, prop)
	}
	return Object(props, D("Theme color definitions (scrollbar, thinkingMax, and search highlight colors are optional and use compatible fallbacks)"), Opt{"additionalProperties", false})
}

func themeJSONSchema() *Schema {
	return Object([]Prop{
		Optional("$schema", String(D("JSON schema reference"))),
		P("name", String(Opt{"pattern", "^[^/]+$"}, D("Theme name. Must not contain '/' because it is reserved for automatic light/dark theme settings."))),
		Optional("appearance", Union(literals("dark", "light"), D("Background the theme is designed for. Detected from the theme colors when omitted."))),
		Optional("vars", Record(colorValue(""), D("Reusable color variables"))),
		P("colors", themeColorsSchema()),
		Optional("export", Object([]Prop{
			Optional("pageBg", colorValue("Page background color")),
			Optional("cardBg", colorValue("Card/container background color")),
			Optional("infoBg", colorValue("Info sections background (system prompt, notices)")),
		}, D("Optional colors for HTML export (defaults derived from userMessageBg if not specified)"), Opt{"additionalProperties", false})),
	}, Opt{"title", "Pi Coding Agent Theme"}, D("Theme schema for Pi coding agent"), Opt{"additionalProperties", false})
}

func themeArtifact() Artifact {
	return Artifact{Path: "schemas/theme.schema.json", Schema: themeJSONSchema(), Definitions: []Definition{{"ColorValue", colorValue("")}}}
}
