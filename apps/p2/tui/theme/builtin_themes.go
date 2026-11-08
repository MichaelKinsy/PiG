package theme

// Copied from coding-agent interactive/theme/{dark,light}.json (p2's builtin-themes.ts carries them as modules; Go embeds the same documents).

import (
	_ "embed"
	"encoding/json"
)

//go:embed dark.json
var darkJSON []byte

//go:embed light.json
var lightJSON []byte

func mustParse(data []byte) ThemeJSON {
	var theme ThemeJSON
	if err := json.Unmarshal(data, &theme); err != nil {
		panic("builtin theme: " + err.Error())
	}
	return theme
}

// DarkTheme and LightTheme are builtin-themes.ts DARK_THEME and LIGHT_THEME.
var (
	DarkTheme  = mustParse(darkJSON)
	LightTheme = mustParse(lightJSON)
)
