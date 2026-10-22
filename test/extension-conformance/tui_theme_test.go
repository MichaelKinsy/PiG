package extensionconformance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/tui"
)

// pi-coding-agent's theme helpers (getMarkdownTheme, getSelectListTheme, getSettingsListTheme, highlightCode, initTheme) and keyHint family for every SDK against the
// host: the Node runtime answers with Pi's own functions, the Go, Rust and Python SDKs with their ports (and, for highlightCode, the host's highlight.js port over the
// highlight.code call). The expected values are what the pinned Pi package returned for the same palette and keybindings
// (test/parity/sdk-vectors/pi-coding-agent/<export>.json, which carry the palette Pi ran with), so an SDK that returns a constant, never asks the host, or
// highlights with another theme fails.

type tuiVector struct {
	Name   string
	Args   []any
	Result json.RawMessage
}

func loadTuiVectors(t *testing.T, export string) []tuiVector {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(findModuleRoot(t), "test", "parity", "sdk-vectors", "pi-coding-agent", export+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var file struct{ Cases []tuiVector }
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Cases) == 0 {
		t.Fatalf("%s has no cases", export)
	}
	return file.Cases
}

// tuiSamples are the texts the vectors' styles list a result for, in the order of automation/gen/sdk-vector-lib/theme-state.mjs.
var tuiSamples = []string{"", "x", "to do it", "a\nb", "漢字", "\x1b[1m", "  ", "line\x1b[22m with close", "\x1b[3mi\x1b[23m", "multi\nline\x1b[1m\nbold", "a\r\nb", "\n"}

// installActiveTheme makes the palette Pi ran with the active theme the host draws with (the theme its highlighter and widgets read), and puts back the theme that was
// active when the test ends. The active theme is the process's: a fused Go extension and every other test of this package share it.
func installActiveTheme(t *testing.T, raw json.RawMessage) {
	t.Helper()
	var palette struct {
		Name        string
		Foregrounds map[string]string
		Backgrounds map[string]string
		Mode        string
	}
	if err := json.Unmarshal(raw, &palette); err != nil {
		t.Fatal(err)
	}
	theme, err := tui.NewThemeFromANSIPalette(palette.Name, palette.Foregrounds, palette.Backgrounds, tui.TerminalColorMode(palette.Mode))
	if err != nil {
		t.Fatal(err)
	}
	previous := tui.ActiveTheme()
	t.Cleanup(func() { tui.SetThemeInstance(previous) })
	tui.SetThemeInstance(theme)
}

// installPiTheme makes the palette Pi ran with the host's theme, as the extension sees it (the state the host publishes) and as the host draws with it (the active
// theme its highlighter reads), and restores the active theme when the test ends.
func (r *modelTypesRig) installPiTheme(t *testing.T, raw json.RawMessage) {
	t.Helper()
	installActiveTheme(t, raw)
	var published map[string]any
	if err := json.Unmarshal(raw, &published); err != nil {
		t.Fatal(err)
	}
	r.setTheme(published)
}

// A test that installs a theme for the host puts the active theme back as it found it, on a terminal without truecolor too. The host's theme is the process's, which
// the widget goldens (drawn by Pi in the dark truecolor theme) read: SetThemeInstance once made a 256-color copy of the theme it was given back, so in a container
// without COLORTERM the first of these tests left the dark theme in 256 colors and every cell of TestConformance_ComponentKitWidgetsMatchPi failed on its first row.
func TestInstalledThemeIsPutBackAsFound(t *testing.T) {
	var want struct{ Palette json.RawMessage }
	if err := json.Unmarshal(loadTuiVectors(t, "getMarkdownTheme")[0].Result, &want); err != nil {
		t.Fatal(err)
	}
	previousCapabilities, previousTheme := tui.GetCapabilities(), tui.ActiveTheme()
	t.Cleanup(func() {
		tui.SetCapabilities(previousCapabilities)
		tui.SetThemeInstance(previousTheme)
	})
	tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true})
	tui.SetTheme("dark")
	found := tui.ActiveTheme()
	if found.GetColorMode() != tui.TerminalColorModeTrueColor {
		t.Fatalf("the theme the test starts from is %s, want truecolor", found.GetColorMode())
	}
	tui.SetCapabilities(tui.TerminalCapabilities{})
	t.Run("install", func(t *testing.T) {
		installActiveTheme(t, want.Palette)
		if tui.ActiveTheme() == found {
			t.Fatal("the palette was not installed")
		}
	})
	if got := tui.ActiveTheme(); got != found {
		t.Fatalf("the active theme after the test is %q in %s, want the %q theme in %s it found", got.Name, got.GetColorMode(), found.Name, found.GetColorMode())
	}
}

// resetTheme ends what an SDK installed with initTheme: the Go SDK of a fused extension shares this process, and its global theme outlives the rig that installed
// it until the host publishes another palette.
func (r *modelTypesRig) resetTheme(t *testing.T) {
	t.Helper()
	r.setTheme(map[string]any{"name": "reset", "foregrounds": map[string]any{}, "backgrounds": map[string]any{}})
	r.toolWith("tui_theme_probe", map[string]any{"op": "markdown", "samples": []any{}, "highlight": []any{}})
}

func isNode(t *testing.T) bool { return strings.Contains(t.Name(), "/node-") }

func samplesArg() []any {
	out := make([]any, len(tuiSamples))
	for i, sample := range tuiSamples {
		out[i] = sample
	}
	return out
}

// asJSON normalizes a decoded report so it compares with a vector's result.
func asJSON(t *testing.T, value any) any {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func decodeRaw(t *testing.T, raw json.RawMessage) any {
	t.Helper()
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestMarkdownThemeAcrossSDKs(t *testing.T) {
	cases := loadTuiVectors(t, "getMarkdownTheme")
	eachModelTypesPlacementAfter(t, nil, printModeRig, func(t *testing.T, r *modelTypesRig) {
		for _, c := range cases {
			var want struct {
				Palette   json.RawMessage
				Styles    map[string]any
				Highlight []struct {
					Code  string
					Lang  any
					Lines []string
				}
			}
			if err := json.Unmarshal(c.Result, &want); err != nil {
				t.Fatal(err)
			}
			r.installPiTheme(t, want.Palette)
			var pairs []any
			for _, h := range want.Highlight {
				lang := h.Lang
				if _, ok := lang.(string); !ok {
					lang = nil
				}
				pairs = append(pairs, []any{h.Code, lang})
			}
			report := r.toolWith("tui_theme_probe", map[string]any{"op": "markdown", "samples": samplesArg(), "highlight": pairs})
			if got := asJSON(t, report["styles"]); !reflect.DeepEqual(got, asJSON(t, want.Styles)) {
				t.Errorf("%s: styles = %v\nPi %v", c.Name, got, want.Styles)
			}
			highlight, _ := report["highlight"].([]any)
			if len(highlight) != len(want.Highlight) {
				t.Fatalf("%s: %d highlights for %d codes", c.Name, len(highlight), len(want.Highlight))
			}
			for i, h := range want.Highlight {
				if !reflect.DeepEqual(highlight[i], asJSON(t, h.Lines)) {
					t.Errorf("%s: highlightCode(%q, %v) = %q\nPi %q", c.Name, h.Code, h.Lang, highlight[i], h.Lines)
				}
			}
		}
	})
}

func TestSelectListThemeAcrossSDKs(t *testing.T) {
	cases := loadTuiVectors(t, "getSelectListTheme")
	eachModelTypesPlacement(t, func(t *testing.T, r *modelTypesRig) {
		for _, c := range cases {
			var want struct {
				Palette json.RawMessage
				Styles  map[string]any
			}
			if err := json.Unmarshal(c.Result, &want); err != nil {
				t.Fatal(err)
			}
			r.installPiTheme(t, want.Palette)
			report := r.toolWith("tui_theme_probe", map[string]any{"op": "select", "samples": samplesArg()})
			if got := asJSON(t, report["styles"]); !reflect.DeepEqual(got, asJSON(t, want.Styles)) {
				t.Errorf("%s: styles = %v\nPi %v", c.Name, got, want.Styles)
			}
		}
	})
}

func TestSettingsListThemeAcrossSDKs(t *testing.T) {
	cases := loadTuiVectors(t, "getSettingsListTheme")
	eachModelTypesPlacement(t, func(t *testing.T, r *modelTypesRig) {
		for _, c := range cases {
			var want struct {
				Palette json.RawMessage
				Styles  map[string]any
			}
			if err := json.Unmarshal(c.Result, &want); err != nil {
				t.Fatal(err)
			}
			r.installPiTheme(t, want.Palette)
			report := r.toolWith("tui_theme_probe", map[string]any{"op": "settings", "samples": samplesArg()})
			if got := asJSON(t, report["styles"]); !reflect.DeepEqual(got, asJSON(t, want.Styles)) {
				t.Errorf("%s: styles = %v\nPi %v", c.Name, got, want.Styles)
			}
		}
	})
}

// highlightCode draws with the theme the extension holds. The Node runtime holds the host's palette as Pi's global theme; the Go, Rust and Python SDKs hold it too, and
// the row has them install a theme of their own with initTheme, which the host does not have active: the lines come out in the installed theme's colors only if the SDK
// sent its palette with the call.
func TestHighlightCodeAcrossSDKs(t *testing.T) {
	cases := loadTuiVectors(t, "highlightCode")
	eachModelTypesPlacementAfter(t, nil, printModeRig, func(t *testing.T, r *modelTypesRig) {
		palettes := map[string]json.RawMessage{}
		for _, c := range cases {
			var want struct{ Palette json.RawMessage }
			_ = json.Unmarshal(c.Result, &want)
			name, _ := c.Args[2].(string)
			mode, _ := c.Args[3].(string)
			palettes[name+" "+mode] = want.Palette
		}
		subprocess.SetThemeLoader(func(name *string) any {
			if name == nil {
				return nil
			}
			return decodeRaw(t, palettes[*name])
		})
		t.Cleanup(func() { subprocess.SetThemeLoader(nil) })
		dark := palettes["dark truecolor"]
		for _, c := range cases {
			var want struct {
				Palette json.RawMessage
				Lines   []string
			}
			if err := json.Unmarshal(c.Result, &want); err != nil {
				t.Fatal(err)
			}
			args := map[string]any{"op": "highlight", "code": c.Args[0], "samples": []any{}}
			if lang, ok := c.Args[1].(string); ok {
				args["lang"] = lang
			}
			if isNode(t) {
				r.installPiTheme(t, want.Palette)
			} else {
				// The host draws with the dark theme; the SDK installs the theme Pi ran with.
				r.installPiTheme(t, dark)
				name, _ := c.Args[2].(string)
				mode, _ := c.Args[3].(string)
				args["theme"] = name + " " + mode
			}
			report := r.toolWith("tui_theme_probe", args)
			if got := asJSON(t, report["lines"]); !reflect.DeepEqual(got, asJSON(t, want.Lines)) {
				t.Errorf("%s: highlightCode = %q\nPi %q", c.Name, got, want.Lines)
			}
		}
		r.resetTheme(t)
	})
}

// initTheme installs the host's theme of the name asked: the global theme every helper reads afterwards, as Pi's initTheme sets its global theme. The Node runtime's
// global theme is Pi's own, so the row runs the Go, Rust and Python SDKs.
func TestInitThemeAcrossSDKs(t *testing.T) {
	cases := loadTuiVectors(t, "getMarkdownTheme")
	eachModelTypesPlacementOf(t, []string{"go", "rust", "python"}, func(t *testing.T, r *modelTypesRig) {
		palettes := map[string]json.RawMessage{}
		wants := map[string]struct{ Bold, Heading any }{}
		for _, c := range cases {
			var want struct {
				Palette json.RawMessage
				Styles  map[string]any
			}
			if err := json.Unmarshal(c.Result, &want); err != nil {
				t.Fatal(err)
			}
			palettes[c.Name] = want.Palette
			wants[c.Name] = struct{ Bold, Heading any }{want.Styles["bold"], want.Styles["heading"]}
		}
		var asked []string
		subprocess.SetThemeLoader(func(name *string) any {
			if name == nil {
				asked = append(asked, "<undefined>")
				return nil
			}
			asked = append(asked, *name)
			return decodeRaw(t, palettes[*name])
		})
		t.Cleanup(func() { subprocess.SetThemeLoader(nil) })
		// The host's own theme is the first one; the SDK holds another after initTheme.
		first := cases[0]
		for _, c := range cases[1:] {
			r.installPiTheme(t, palettes[first.Name])
			report := r.toolWith("tui_theme_probe", map[string]any{"op": "init_theme", "theme": c.Name, "samples": samplesArg()})
			if got := asJSON(t, report["bold"]); !reflect.DeepEqual(got, wants[c.Name].Bold) {
				t.Errorf("%s: bold = %v\nPi %v", c.Name, got, wants[c.Name].Bold)
			}
			if got := asJSON(t, report["heading"]); !reflect.DeepEqual(got, wants[c.Name].Heading) {
				t.Errorf("%s: heading = %v\nPi %v", c.Name, got, wants[c.Name].Heading)
			}
		}
		if len(asked) != len(cases)-1 || asked[0] != cases[1].Name {
			t.Errorf("the host was asked for %v", asked)
		}
		r.resetTheme(t)
	})
}

// installPiKeybindings publishes the keybinding table Pi ran with, as the host does (ready state and every state snapshot).
func (r *modelTypesRig) installPiKeybindings(t *testing.T, defs map[string]any, user map[string]any) {
	t.Helper()
	r.bridge.SetKeybindingsFunc(func() any { return map[string]any{"definitions": defs, "userBindings": user} })
	t.Cleanup(func() { r.bridge.SetKeybindingsFunc(nil) })
}

// vectorPlatform is Pi's process.platform for the process the SDKs run in: the vectors are recorded for each platform in the case sources.
func vectorPlatform() string {
	return map[string]string{"linux": "linux", "darwin": "darwin", "windows": "win32"}[runtime.GOOS]
}

// keyHint and keyText read the host's keybindings and theme; the platform is the one the SDK process runs on, so a vector for another platform is run by the unit tests.
func TestKeyHintAcrossSDKs(t *testing.T) {
	cases := loadTuiVectors(t, "keyHint")
	eachModelTypesPlacement(t, func(t *testing.T, r *modelTypesRig) {
		ran := 0
		for _, c := range cases {
			var want struct {
				Palette  json.RawMessage
				Platform string
				Defs     map[string]any
				User     map[string]any
				Text     string
			}
			if err := json.Unmarshal(c.Result, &want); err != nil {
				t.Fatal(err)
			}
			if want.Platform != vectorPlatform() {
				continue
			}
			ran++
			r.installPiTheme(t, want.Palette)
			r.installPiKeybindings(t, want.Defs, want.User)
			report := r.toolWith("tui_theme_probe", map[string]any{"op": "key_hint", "id": c.Args[4], "description": c.Args[5], "samples": []any{}})
			if report["keyHint"] != want.Text {
				t.Errorf("%s: keyHint = %q\nPi %q", c.Name, report["keyHint"], want.Text)
			}
		}
		if ran == 0 {
			t.Fatalf("no vector was recorded for process.platform %q (the SDK process's platform on %s)", vectorPlatform(), runtime.GOOS)
		}
	})
}

func TestRawKeyHintAcrossSDKs(t *testing.T) {
	cases := loadTuiVectors(t, "rawKeyHint")
	eachModelTypesPlacement(t, func(t *testing.T, r *modelTypesRig) {
		ran := 0
		for _, c := range cases {
			var want struct {
				Palette  json.RawMessage
				Platform string
				Text     string
			}
			if err := json.Unmarshal(c.Result, &want); err != nil {
				t.Fatal(err)
			}
			if want.Platform != vectorPlatform() {
				continue
			}
			ran++
			r.installPiTheme(t, want.Palette)
			report := r.toolWith("tui_theme_probe", map[string]any{"op": "raw_key_hint", "key": c.Args[3], "description": c.Args[4], "samples": []any{}})
			if report["rawKeyHint"] != want.Text {
				t.Errorf("%s: rawKeyHint = %q\nPi %q", c.Name, report["rawKeyHint"], want.Text)
			}
		}
		if ran == 0 {
			t.Fatalf("no vector was recorded for process.platform %q (the SDK process's platform on %s)", vectorPlatform(), runtime.GOOS)
		}
	})
}

// printModeRig reports print mode to the extension. The Pi vectors of highlightCode and getMarkdownTheme come from a Pi that holds highlight.js's eager language set; Pi's
// interactive mode registers every language once startup has emitted session_start and resources_discover (interactive-mode.ts:1100-1122), after which html, markdown, sql
// and diff come out highlighted. A Node runtime that serves a tui-mode host loads the full set the same way, so the rows that compare with the eager vectors run in a mode that keeps it.
func printModeRig(t *testing.T, r *modelTypesRig) {
	t.Helper()
	r.host.SetMode("print")
}
