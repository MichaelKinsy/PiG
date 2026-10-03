package tui

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadBuiltinThemeDark(t *testing.T) {
	th, err := LoadBuiltinTheme("dark")
	if err != nil {
		t.Fatalf("LoadBuiltinTheme(dark): %v", err)
	}
	if th.Name != "dark" {
		t.Errorf("Name = %q, want dark", th.Name)
	}
	if th.UserMessageBg == "" {
		t.Error("UserMessageBg should be set")
	}
	if th.Accent == "" {
		t.Error("Accent should be set")
	}
	if th.ToolDiffAdded == "" {
		t.Error("ToolDiffAdded should be set")
	}
}

func TestLoadBuiltinThemeLight(t *testing.T) {
	th, err := LoadBuiltinTheme("light")
	if err != nil {
		t.Fatalf("LoadBuiltinTheme(light): %v", err)
	}
	if th.Name != "light" {
		t.Errorf("Name = %q, want light", th.Name)
	}
}

// TestProductionThemesMatchPinnedBuiltins exercises the SetTheme caller path
// and ensures every field components can observe comes from the embedded
// pinned theme rather than a second hard-coded palette.
func TestProductionThemesMatchPinnedBuiltins(t *testing.T) {
	old := ActiveTheme()
	SetCapabilities(TerminalCapabilities{TrueColor: true})
	t.Cleanup(func() {
		ResetCapabilitiesCache()
		activeTheme.Store(old)
	})

	for _, name := range []string{"dark", "light"} {
		t.Run(name, func(t *testing.T) {
			want, err := LoadBuiltinTheme(name)
			if err != nil {
				t.Fatal(err)
			}
			SetTheme(name)
			if got := ActiveTheme(); !reflect.DeepEqual(got, want) {
				t.Errorf("ActiveTheme after SetTheme(%q) does not match pinned builtin\n got: %#v\nwant: %#v", name, got, want)
			}
		})
	}
}

// TestBuiltinThemeOptionalColorsMatchPinnedThemes locks the current dark/light
// values added with fullscreen scrollbar, search, and thinkingMax support.
func TestBuiltinThemeOptionalColorsMatchPinnedThemes(t *testing.T) {
	for _, tc := range []struct {
		name string
		want map[string]string
	}{
		{name: "dark", want: map[string]string{
			"text": "#dee0e1", "scrollbarTrack": "#484e52", "scrollbarThumb": "#97a0a5",
			"searchMatchBg": "#4e2f1b", "searchMatchText": "#9da5a9", "thinkingMax": "#fe5462",
		}},
		{name: "light", want: map[string]string{
			"text": "#3b3f41", "scrollbarTrack": "#e1e3e4", "scrollbarThumb": "#96a0a4",
			"searchMatchBg": "#ede3dd", "searchMatchText": "#677176", "thinkingMax": "#fe7479",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			theme, err := LoadBuiltinTheme(tc.name)
			if err != nil {
				t.Fatal(err)
			}
			colors := theme.Colors()
			for token, want := range tc.want {
				if got := colors[token]; got != want {
					t.Errorf("Colors[%s] = %q, want %q", token, got, want)
				}
			}
		})
	}
}

func TestLoadThemeFile(t *testing.T) {
	dir := t.TempDir()
	data := strings.Replace(completeThemeJSON(t, "custom", `"accent": "#8abeb7", "success": "green", "error": "red", "userMessageBg": "#333333"`),
		`"colors"`, `"vars": { "red": "#ff0000", "green": "#00ff00" }, "colors"`, 1)
	path := filepath.Join(dir, "custom.json")
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	th, err := LoadThemeFile(path)
	if err != nil {
		t.Fatalf("LoadThemeFile: %v", err)
	}
	if th.Name != "custom" {
		t.Errorf("Name = %q", th.Name)
	}
	// accent is a literal hex (not a var ref)
	if th.Accent == "" {
		t.Error("Accent should be set from literal hex")
	}
	// success resolves through vars
	if th.Success == "" {
		t.Error("Success should resolve through vars")
	}
}

func TestLoadThemeDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.json"), []byte(completeThemeJSON(t, "a", `"accent":"#aaaaaa"`)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.json"), []byte(completeThemeJSON(t, "b", `"accent":"#bbbbbb"`)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "not-a-theme.txt"), []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}

	themes, err := LoadThemeDir(dir)
	if err != nil {
		t.Fatalf("LoadThemeDir: %v", err)
	}
	if len(themes) != 2 {
		t.Errorf("len = %d, want 2", len(themes))
	}
}

func TestThemeRegistry(t *testing.T) {
	r := NewThemeRegistry()
	if len(r.Names()) != 3 {
		t.Errorf("Names = %v, want [system, dark, light]", r.Names())
	}

	custom := &Theme{Name: "custom"}
	r.Add(custom)
	if len(r.Names()) != 4 {
		t.Errorf("Names = %v after Add", r.Names())
	}
	if r.Get("custom") != custom {
		t.Error("Get(custom) should return added theme")
	}
}

// writeThemeWithSections writes a complete, schema-valid theme whose colors
// begin with colorsFragment, adding vars and export sections when non-empty.
func writeThemeWithSections(t *testing.T, name, vars, colorsFragment, export string) string {
	t.Helper()
	data := completeThemeJSON(t, name, colorsFragment)
	if vars != "" {
		data = strings.Replace(data, `"colors"`, `"vars":{`+vars+`},"colors"`, 1)
	}
	if export != "" {
		data = strings.TrimSuffix(data, "}") + `,"export":{` + export + `}}`
	}
	if err := ValidateThemeJSON(name, []byte(data)); err != nil {
		t.Fatalf("fixture must pass the upstream schema: %v", err)
	}
	path := filepath.Join(t.TempDir(), name+".json")
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestLoadThemeFileIndexedColors mirrors theme.ts: an integer ColorValue
// (theme-json.ts Integer 0..255) survives var resolution and emits the indexed
// escapes of fgAnsi/bgAnsi (`38;5;N` / `48;5;N`); HTML export colors convert
// it with ansi256ToHex (getResolvedThemeColors, getThemeExportColors).
func TestLoadThemeFileIndexedColors(t *testing.T) {
	// The hex color is emitted as 24-bit SGR only when the terminal reports truecolor.
	preserveCapabilityState(t)
	SetCapabilities(TerminalCapabilities{TrueColor: true})
	path := writeThemeWithSections(t, "indexed",
		`"idx":34,"alias":"idx","cardIdx":255`,
		`"accent":123,"userMessageBg":236,"success":"idx","error":"alias","thinkingText":1.2e2`,
		`"pageBg":16,"cardBg":"cardIdx","infoBg":""`)
	th, err := LoadThemeFile(path)
	if err != nil {
		t.Fatalf("LoadThemeFile: %v", err)
	}
	for _, tc := range []struct{ name, got, want string }{
		{"Accent", th.Accent, "\x1b[38;5;123m"},
		{"Fg(accent)", th.Fg("accent"), "\x1b[38;5;123m"},
		{"UserMessageBg", th.UserMessageBg, "\x1b[48;5;236m"},
		{"Bg(userMessageBg)", th.Bg("userMessageBg"), "\x1b[48;5;236m"},
		{"Success (var)", th.Success, "\x1b[38;5;34m"},
		{"Error (var chain)", th.Error, "\x1b[38;5;34m"},
		{"ThinkingText (1.2e2)", th.ThinkingText, "\x1b[38;5;120m"},
		{"Border (hex)", th.Border, "\x1b[38;2;128;128;128m"},
		{"FgText", th.FgText("accent", "x"), "\x1b[38;5;123mx\x1b[39m"},
		{"Colors[accent]", th.Colors()["accent"], "#87ffff"},
		{"Colors[userMessageBg]", th.Colors()["userMessageBg"], "#303030"},
		{"Colors[success]", th.Colors()["success"], "#00af00"},
		{"ExportPageBg", th.ExportPageBg, "#000000"},
		{"ExportCardBg (var)", th.ExportCardBg, "#eeeeee"},
		{"ExportInfoBg (empty)", th.ExportInfoBg, ""},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
	fg, bg := th.ANSIPalette()
	if fg["success"] != "\x1b[38;5;34m" || bg["userMessageBg"] != "\x1b[48;5;236m" {
		t.Errorf("ANSIPalette fg[success]=%q bg[userMessageBg]=%q", fg["success"], bg["userMessageBg"])
	}
}

// TestLoadThemeFileExplicitEmptyColors mirrors theme.ts Theme: an explicit empty color resolves to the terminal default reset, while the color values the export reads are the terminal's default colors, or a guess by the theme's appearance (upstream 0.99.1 GUESSED_DEFAULT_COLORS) when the terminal reported none. An absent token still emits no escape.
func TestLoadThemeFileExplicitEmptyColors(t *testing.T) {
	SetTerminalColors(TerminalColors{})
	for _, tc := range []struct {
		appearance     string
		wantFg, wantBg string
	}{
		{appearance: "light", wantFg: "#000000", wantBg: "#ffffff"},
		{appearance: "dark", wantFg: "#e5e5e7", wantBg: "#000000"},
	} {
		t.Run(tc.appearance, func(t *testing.T) {
			path := writeThemeWithSections(t, "custom-"+tc.appearance, `"terminalDefault":""`,
				`"accent":"terminalDefault","userMessageBg":""`, "")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			data = []byte(strings.Replace(string(data), `"vars"`, `"appearance":"`+tc.appearance+`","vars"`, 1))
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			theme, err := LoadThemeFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, check := range []struct{ name, got, want string }{
				{name: "Accent var", got: theme.Accent, want: SGRFgReset},
				{name: "Fg", got: theme.Fg("accent"), want: SGRFgReset},
				{name: "FgText", got: theme.FgText("accent", "x"), want: SGRFgReset + "x" + SGRFgReset},
				{name: "UserMessageBg", got: theme.UserMessageBg, want: SGRBgReset},
				{name: "Bg", got: theme.Bg("userMessageBg"), want: SGRBgReset},
				{name: "Colors[accent]", got: theme.Colors()["accent"], want: tc.wantFg},
				{name: "Colors[userMessageBg]", got: theme.Colors()["userMessageBg"], want: tc.wantBg},
				{name: "missing Fg", got: theme.Fg("not-a-token"), want: ""},
				{name: "missing Bg", got: theme.Bg("not-a-token"), want: ""},
			} {
				if check.got != check.want {
					t.Errorf("%s = %q, want %q", check.name, check.got, check.want)
				}
			}
		})
	}
}

// TestLoadThemeFileVarReferenceErrors mirrors theme.ts resolveVarRefs: a
// missing or circular reference fails createTheme.
func TestLoadThemeFileVarReferenceErrors(t *testing.T) {
	for _, tc := range []struct{ name, vars, colors, want string }{
		{"missing", `"a":1`, `"accent":"nope"`, "Variable reference not found: nope"},
		{"circular", `"a":"b","b":"a"`, `"accent":"a"`, "Circular variable reference detected: a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadThemeFile(writeThemeWithSections(t, tc.name, tc.vars, tc.colors, ""))
			if err == nil || err.Error() != tc.want {
				t.Fatalf("LoadThemeFile error = %v, want %q", err, tc.want)
			}
		})
	}
}

// TestLoadThemeFileExportVarErrorLeavesExportUnset mirrors
// getThemeExportColors' catch: an unresolvable export reference drops every
// export color while the theme itself still loads.
func TestLoadThemeFileExportVarErrorLeavesExportUnset(t *testing.T) {
	th, err := LoadThemeFile(writeThemeWithSections(t, "export-missing", "", "", `"pageBg":"#101010","cardBg":"nope"`))
	if err != nil {
		t.Fatalf("LoadThemeFile: %v", err)
	}
	if th.ExportPageBg != "" || th.ExportCardBg != "" || th.ExportInfoBg != "" {
		t.Fatalf("export colors = %q %q %q, want all unset", th.ExportPageBg, th.ExportCardBg, th.ExportInfoBg)
	}
}

// Ports packages/coding-agent/test/theme-export.test.ts:38,72.
// Export values use the same recursive vars as colors; palette indexes become hex and an empty export color stays unset.
func TestThemeExportColorsUpstream(t *testing.T) {
	for _, tc := range []struct {
		name   string
		vars   map[string]any
		export map[string]any
		want   map[string]string
	}{
		{
			name: "custom-export-vars",
			vars: map[string]any{
				"pageBgVar": "#112233", "pageBgAlias": "pageBgVar", "infoBgVar": "#445566", "cardBgVar": "#223344",
			},
			export: map[string]any{"pageBg": "pageBgAlias", "cardBg": "cardBgVar", "infoBg": "infoBgVar"},
			want:   map[string]string{"pageBg": "#112233", "cardBg": "#223344", "infoBg": "#445566"},
		},
		{
			name: "custom-export-recursive",
			vars: map[string]any{
				"deepPageBg": "#abcdef", "pageBgAlias": "deepPageBg", "cardBgAnsi": 24,
			},
			export: map[string]any{"pageBg": "pageBgAlias", "cardBg": "cardBgAnsi", "infoBg": ""},
			// The native Theme fields use an empty string for Pi's undefined export color; the HTML exporter applies its fallback only then.
			want: map[string]string{"pageBg": "#abcdef", "cardBg": "#005f87", "infoBg": ""},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dark, err := builtinThemes.ReadFile("theme_dark.json")
			if err != nil {
				t.Fatal(err)
			}
			var theme map[string]any
			if err := json.Unmarshal(dark, &theme); err != nil {
				t.Fatal(err)
			}
			theme["name"] = tc.name
			maps.Copy(theme["vars"].(map[string]any), tc.vars)
			theme["export"] = tc.export
			data, err := json.MarshalIndent(theme, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			agentDir := filepath.Join(t.TempDir(), "agent")
			themesDir := filepath.Join(agentDir, "themes")
			if err := os.MkdirAll(themesDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(themesDir, tc.name+".json"), data, 0o644); err != nil {
				t.Fatal(err)
			}
			registry := NewThemeRegistry()
			if err := registry.LoadDir(themesDir); err != nil {
				t.Fatal(err)
			}
			loaded := registry.Get(tc.name)
			if loaded == nil {
				t.Fatalf("theme %q was not loaded from the agent themes directory", tc.name)
			}
			got := map[string]string{"pageBg": loaded.ExportPageBg, "cardBg": loaded.ExportCardBg, "infoBg": loaded.ExportInfoBg}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("export colors = %v, want %v", got, tc.want)
			}
		})
	}
}

// Pi's startup listing and getAllThemes report the file every theme loaded
// from, a single theme file as well as a themes directory, under the theme's
// own name.
func TestThemeRegistryRecordsSourceFiles(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "single.json")
	if err := os.WriteFile(file, []byte(completeThemeJSON(t, "from-file", `"accent":"#aaaaaa"`)), 0o644); err != nil {
		t.Fatal(err)
	}
	themesDir := filepath.Join(dir, "themes")
	if err := os.MkdirAll(themesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	renamed := filepath.Join(themesDir, "file-name.json")
	if err := os.WriteFile(renamed, []byte(completeThemeJSON(t, "theme-name", `"accent":"#bbbbbb"`)), 0o644); err != nil {
		t.Fatal(err)
	}

	r := NewThemeRegistry()
	th, err := LoadThemeFile(file)
	if err != nil {
		t.Fatal(err)
	}
	r.AddFile(th, file)
	if err := r.LoadDir(themesDir); err != nil {
		t.Fatal(err)
	}
	if got := r.PathOf("from-file"); got != file {
		t.Errorf("PathOf(from-file) = %q, want %q", got, file)
	}
	if got := r.PathOf("theme-name"); got != renamed {
		t.Errorf("PathOf(theme-name) = %q, want %q", got, renamed)
	}
	// A theme added without a file replaces the recorded source.
	r.Add(&Theme{Name: "from-file"})
	if got := r.PathOf("from-file"); got != "" {
		t.Errorf("PathOf after pathless Add = %q, want empty", got)
	}
}
