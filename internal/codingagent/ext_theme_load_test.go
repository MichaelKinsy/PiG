package codingagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// initTheme(name) installs the theme of that name in the terminal's color mode, and the system theme for an absent or unknown name. The palettes of dark and light are the
// ones Pi's initTheme left as its global theme (test/parity/sdk-vectors/pi-coding-agent/initTheme.json).
func TestLoadExtensionThemeMatchesPiInitTheme(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "test", "parity", "sdk-vectors", "pi-coding-agent", "initTheme.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Cases []struct {
			Name   string
			Args   []any
			Result struct {
				Name    string
				Palette *struct {
					Name        string
					Foregrounds map[string]string
					Backgrounds map[string]string
					Mode        string
				}
			}
		}
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	previousRegistry, previousCaps := tui.ActiveThemeRegistry(), tui.GetCapabilities()
	t.Cleanup(func() { tui.SetThemeRegistry(previousRegistry); tui.SetCapabilities(previousCaps) })
	tui.SetThemeRegistry(tui.NewThemeRegistry())
	for _, c := range file.Cases {
		mode, _ := c.Args[1].(string)
		tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: mode == "truecolor"})
		var name *string
		if s, ok := c.Args[0].(string); ok {
			name = &s
		}
		palette, ok := LoadExtensionTheme(name).(map[string]any)
		if !ok {
			t.Fatalf("%s: the palette is %T", c.Name, LoadExtensionTheme(name))
		}
		if palette["name"] != c.Result.Name {
			t.Errorf("%s: theme %v, Pi %q", c.Name, palette["name"], c.Result.Name)
		}
		if c.Result.Palette == nil {
			continue
		}
		if palette["mode"] != c.Result.Palette.Mode {
			t.Errorf("%s: mode %v, Pi %q", c.Name, palette["mode"], c.Result.Palette.Mode)
		}
		for key, want := range map[string]map[string]string{"foregrounds": c.Result.Palette.Foregrounds, "backgrounds": c.Result.Palette.Backgrounds} {
			if got, _ := palette[key].(map[string]string); !reflect.DeepEqual(got, want) {
				t.Errorf("%s: %s = %v\nPi %v", c.Name, key, got, want)
			}
		}
	}
}
