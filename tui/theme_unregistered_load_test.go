package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// protoThemeFile is the built-in dark theme named "proto" after mutate changed it.
func protoThemeFile(t *testing.T, mutate func(document map[string]any)) []byte {
	t.Helper()
	data, err := builtinThemes.ReadFile("theme_dark.json")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	document["name"] = "proto"
	if mutate != nil {
		mutate(document)
	}
	if data, err = json.Marshal(document); err != nil {
		t.Fatal(err)
	}
	return data
}

// theme.ts setTheme (703-724) reports the message of the error thrown under loadTheme (557-566). For a name that is neither built in nor registered, loadThemeJson (487-507)
// looks at <custom themes directory>/<name>.json: only a file that does not exist is "Theme not found" (503). A file that exists fails with fs.readFileSync's message (505),
// with parseThemeJsonContent's and the validator's message labelled with the name (477-485, theme-schema.ts:97-141), or with resolveVarRefs' message (88, 91).
// A theme file that fails to load at startup is never registered, so this is the reason the "Failed to load theme" line repeats.
// Each expected text is Pi's: from those lines, and printed by the Pi build b4afdddf44e0 for the same kind of file selected by settings.json.
func TestSetThemeByNameCheckedReportsWhyAnUnregisteredThemeFileFailed(t *testing.T) {
	previousRegistry, previousDir, previousTheme, previousSelected := globalRegistry.Load(), customThemesDir.Load(), activeTheme.Load(), selectedThemeName.Load()
	t.Cleanup(func() {
		globalRegistry.Store(previousRegistry)
		customThemesDir.Store(previousDir)
		activeTheme.Store(previousTheme)
		selectedThemeName.Store(previousSelected)
	})
	colors := func(mutate func(colors map[string]any)) func(map[string]any) {
		return func(document map[string]any) { mutate(document["colors"].(map[string]any)) }
	}
	file := func(data []byte) func(*testing.T, string) {
		return func(t *testing.T, path string) {
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	theme := func(mutate func(map[string]any)) func(*testing.T, string) {
		return func(t *testing.T, path string) { file(protoThemeFile(t, mutate))(t, path) }
	}
	for _, tc := range []struct {
		name string
		// write creates <custom themes directory>/proto.json; nil leaves it missing.
		write func(t *testing.T, path string)
		// registered is a valid theme registered as "proto" before the selection.
		registered bool
		// want is the reason with PATH for the file's path; "" means the theme loads, and active is its name.
		want, active string
	}{
		// A working theme: the file is found by its file name, whatever name its JSON carries (loadThemeJson reads <name>.json).
		{name: "valid file with another name", write: theme(func(document map[string]any) { document["name"] = "other" }), active: "other"},
		{name: "missing file", want: "Theme not found: proto"},
		{name: "directory in place of the file", write: func(t *testing.T, path string) {
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
		}, want: "EISDIR: illegal operation on a directory, read"},
		{name: "unreadable file", write: func(t *testing.T, path string) {
			if runtime.GOOS == "windows" || os.Geteuid() == 0 {
				t.Skip("POSIX permission bits are required to make a file unreadable")
			}
			theme(nil)(t, path)
			if err := os.Chmod(path, 0); err != nil {
				t.Fatal(err)
			}
		}, want: "EACCES: permission denied, open 'PATH'"},
		{name: "invalid JSON", write: file([]byte(`{"name": "proto",`)), want: "Failed to parse theme proto: SyntaxError: Expected double-quoted property name in JSON at position 17 (line 1 column 18)"},
		{name: "not an object", write: file([]byte(`[1,2]`)), want: "Invalid theme \"proto\":\n\n\nOther errors:\n  - /: must be object"},
		{name: "no colors", write: file([]byte(`{"name":"proto"}`)), want: "Invalid theme \"proto\":\n\n\nOther errors:\n  - /: must have required properties colors"},
		{name: "missing required color", write: theme(colors(func(colors map[string]any) { delete(colors, "accent") })), want: "Invalid theme \"proto\":\n\nMissing required color tokens:\n  - accent\n\nPlease add these colors to your theme's \"colors\" object.\nSee the built-in themes (dark.json, light.json) for reference values."},
		{name: "color of the wrong type", write: theme(colors(func(colors map[string]any) { colors["accent"] = true })), want: "Invalid theme \"proto\":\n\n\nOther errors:\n  - /colors/accent: must be string\n  - /colors/accent: must be integer\n  - /colors/accent: must match a schema in anyOf"},
		{name: "unknown variable", write: theme(colors(func(colors map[string]any) { colors["accent"] = "nosuchvar" })), want: "Variable reference not found: nosuchvar"},
		{name: "circular variable", write: theme(func(document map[string]any) {
			vars := document["vars"].(map[string]any)
			vars["loopa"], vars["loopb"] = "loopb", "loopa"
			document["colors"].(map[string]any)["accent"] = "loopa"
		}), want: "Circular variable reference detected: loopa"},
		{name: "name with a slash", write: theme(func(document map[string]any) { document["name"] = "a/b" }), want: `Invalid theme name "a/b": theme names cannot contain "/" because it is reserved for automatic light/dark theme settings.`},
		// loadTheme returns a registered theme before it looks at the directory.
		{name: "registered theme over a broken file", write: file([]byte(`{`)), registered: true, active: "proto"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "proto.json")
			if tc.write != nil {
				tc.write(t, path)
				t.Cleanup(func() { _ = os.Chmod(path, 0o700) })
			}
			registry := NewThemeRegistry()
			if tc.registered {
				other := filepath.Join(t.TempDir(), "registered.json")
				file(protoThemeFile(t, nil))(t, other)
				registered, err := LoadThemeFromPath(other, TerminalColorModeTrueColor)
				if err != nil {
					t.Fatal(err)
				}
				registry.AddFile(registered, other)
			}
			SetThemeRegistry(registry)
			SetCustomThemesDir(dir)
			err := SetThemeByNameChecked("proto")
			if tc.want == "" {
				if err != nil {
					t.Fatalf("error = %q, want the theme to load", err)
				}
				if got := ActiveTheme().Name; got != tc.active {
					t.Fatalf("active theme = %q, want %q", got, tc.active)
				}
				return
			}
			want := strings.Replace(tc.want, "PATH", path, 1)
			if err == nil || err.Error() != want {
				t.Fatalf("error = %v\nwant    %s", err, want)
			}
			if got := ActiveTheme().Name; got != SystemThemeName {
				t.Fatalf("active theme = %q, want the system theme after a failed load", got)
			}
		})
	}
}
