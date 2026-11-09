package codingagent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// themeState is the process-global theme state a test can leave changed: the active theme's name and the color mode it was built in, the registry themes are
// looked up in, and the terminal capabilities its color mode follows. restoreStartupTheme puts all four back.
type themeState struct {
	name         string
	colorMode    tui.TerminalColorMode
	registry     *tui.ThemeRegistry
	capabilities tui.TerminalCapabilities
}

func captureThemeState() themeState {
	theme := tui.ActiveTheme()
	return themeState{name: theme.Name, colorMode: theme.GetColorMode(), registry: tui.ActiveThemeRegistry(), capabilities: tui.GetCapabilities()}
}

// startupThemeState is the state at the start of the test binary, recorded by TestMain before any test runs.
var startupThemeState themeState

// themeStateDrift names what differs between the startup state and the current one, or is empty when nothing does.
func themeStateDrift() string {
	now := captureThemeState()
	var drift []string
	if now.name != startupThemeState.name {
		drift = append(drift, fmt.Sprintf("active theme %q, the process started with %q", now.name, startupThemeState.name))
	}
	if now.colorMode != startupThemeState.colorMode {
		drift = append(drift, fmt.Sprintf("active theme color mode %q, the process started with %q", now.colorMode, startupThemeState.colorMode))
	}
	if now.registry != startupThemeState.registry {
		drift = append(drift, "the theme registry is not the one the process started with")
	}
	if now.capabilities != startupThemeState.capabilities {
		drift = append(drift, fmt.Sprintf("terminal capabilities %+v, the process started with %+v", now.capabilities, startupThemeState.capabilities))
	}
	return strings.Join(drift, "; ")
}

// TestThemeStateIsAsTheProcessStarted is the runtime half of TestNoTestLeavesAThemeSelected. It fails when an earlier test of this binary left the theme state
// (active theme, its color mode, the theme registry or the terminal capabilities) different from what the process started with. It cannot say which test did.
// To find the test, run each test alone followed by this one, for example
//
//	go test -c -o codingagent.test ./internal/codingagent && cd internal/codingagent &&
//	../../codingagent.test -test.list '.*' | grep -v '^TestThemeStateIsAsTheProcessStarted$' |
//	  xargs -P8 -I{} sh -c '../../codingagent.test -test.run "^({}|TestThemeStateIsAsTheProcessStarted)\$" >/dev/null 2>&1 || echo {}'
//
// and the names printed fail (or fail themselves). Production code a test starts can change the state without the test's source selecting anything (a startup
// prompt replaces the registry, a reload selects the system theme), which the static guard cannot see; this probe sees it, but only for the tests that ran before it,
// and TestMain runs the same comparison after the last test.
func TestThemeStateIsAsTheProcessStarted(t *testing.T) {
	if drift := themeStateDrift(); drift != "" {
		t.Fatalf("an earlier test left the theme state changed: %s; start it with restoreStartupTheme(t) (startup_ui_theme_test.go)", drift)
	}
}

// TestRestoreStartupThemeRestoresEveryAspectOfTheThemeState pins what the guards in this file rely on: after a test selects another theme, replaces the registry
// and changes the terminal capabilities, restoreStartupTheme puts the theme name, its color mode, the registry and the capabilities back, from whatever state it
// found: the process's own, a theme built in a color mode its capabilities no longer imply, and an instance the registry does not hold.
func TestRestoreStartupThemeRestoresEveryAspectOfTheThemeState(t *testing.T) {
	restoreStartupTheme(t)
	outsideTheme := func(t *testing.T) *tui.Theme {
		data, err := os.ReadFile(filepath.Join("..", "..", "tui", "theme_dark.json"))
		if err != nil {
			t.Fatal(err)
		}
		var document map[string]any
		if err := json.Unmarshal(data, &document); err != nil {
			t.Fatal(err)
		}
		document["name"] = "outside-the-registry"
		data, err = json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "theme.json")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		theme, err := tui.LoadThemeFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return theme
	}
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T)
	}{
		{"the state the process started with", func(*testing.T) {}},
		{"light in truecolor under another registry", func(*testing.T) {
			tui.SetThemeRegistry(tui.NewThemeRegistry())
			tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true})
			tui.SetThemeByName("light")
		}},
		{"dark built in 256 colors while the capabilities report truecolor", func(*testing.T) {
			tui.SetCapabilities(tui.TerminalCapabilities{})
			tui.SetThemeByName("dark")
			tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true})
		}},
		{"an instance the registry does not hold", func(t *testing.T) {
			tui.SetThemeInstance(outsideTheme(t))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			restoreStartupTheme(t)
			tc.setup(t)
			before := captureThemeState()
			t.Run("a test that changes everything", func(t *testing.T) {
				restoreStartupTheme(t)
				tui.SetThemeRegistry(tui.NewThemeRegistry())
				tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: !before.capabilities.TrueColor, Hyperlinks: !before.capabilities.Hyperlinks})
				tui.SetThemeByName(tui.SystemThemeName)
			})
			if after := captureThemeState(); after != before {
				t.Errorf("after the test, the theme state is %+v, want %+v", after, before)
			}
		})
	}
}
