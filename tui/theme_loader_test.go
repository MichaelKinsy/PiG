package tui

import (
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
			"text": "#d4d4d4", "scrollbarTrack": "#505050", "scrollbarThumb": "#d4d4d4",
			"searchMatchBg": "#3a3a4a", "searchMatchText": "#d4d4d4", "thinkingMax": "#ff5fff",
		}},
		{name: "light", want: map[string]string{
			"text": "#1f2328", "scrollbarTrack": "#b0b0b0", "scrollbarThumb": "#1f2328",
			"searchMatchBg": "#d0d0e0", "searchMatchText": "#1f2328", "thinkingMax": "#af005f",
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
	if len(r.Names()) != 2 {
		t.Errorf("Names = %v, want [dark, light]", r.Names())
	}

	custom := &Theme{Name: "custom"}
	r.Add(custom)
	if len(r.Names()) != 3 {
		t.Errorf("Names = %v after Add", r.Names())
	}
	if r.Get("custom") != custom {
		t.Error("Get(custom) should return added theme")
	}
}

func TestHexToBgANSI(t *testing.T) {
	got := hexToBgANSI("#343541")
	want := "\x1b[48;2;52;53;65m"
	if got != want {
		t.Errorf("hexToBgANSI(#343541) = %q, want %q", got, want)
	}
}

func TestHexToFgANSI(t *testing.T) {
	got := hexToFgANSI("#8abeb7")
	want := "\x1b[38;2;138;190;183m"
	if got != want {
		t.Errorf("hexToFgANSI(#8abeb7) = %q, want %q", got, want)
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

// TestLoadThemeFileExplicitEmptyColors mirrors theme.ts: an explicit empty
// color resolves to the terminal default reset, while CSS export receives the
// light or dark default text color. An absent token still emits no escape.
func TestLoadThemeFileExplicitEmptyColors(t *testing.T) {
	for _, tc := range []struct {
		name    string
		wantCSS string
	}{
		{name: "light", wantCSS: "#000000"},
		{name: "custom-dark", wantCSS: "#e5e5e7"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeThemeWithSections(t, tc.name, `"terminalDefault":""`,
				`"accent":"terminalDefault","userMessageBg":""`, "")
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
				{name: "Colors[accent]", got: theme.Colors()["accent"], want: tc.wantCSS},
				{name: "Colors[userMessageBg]", got: theme.Colors()["userMessageBg"], want: tc.wantCSS},
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
