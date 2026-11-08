package host_test

import (
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/host"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/prefs"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/theme"
)

func localTheme(name string) prefs.TuiLocalPreferences {
	local := prefs.EmptyPreferences()
	local.Theme = name
	return local
}

// TestLauncherPostsCapabilitiesPreferencesAndTerminalColorsAsDataBeforeActivationAndOnChange ports tui-theme.test.ts "launcher posts capabilities,
// preferences and terminal colors as data before activation and on change". Pi observes the sandboxed worker's message log (`capabilities`,
// `preferences`, `activate`, then `terminalColors`, a later `preferences`, and never an `input` for a terminal reply). The runtime is in this process, so
// the intention (the visitor's capabilities and preferences are what the runtime starts with, the launcher feeds its later changes and its terminal colors,
// and a terminal reply is consumed by the launcher, never typed into the runtime) is checked on what the runtime observes.
func TestLauncherPostsCapabilitiesPreferencesAndTerminalColorsAsDataBeforeActivationAndOnChange(t *testing.T) {
	var mu sync.Mutex
	var notify func(prefs.TuiLocalPreferences)
	var inputs []string
	l := startLauncher(t, launcherOptions{options: func(options *host.ClientOptions) {
		options.Capabilities = &host.Capabilities{TrueColor: true, Platform: "darwin"}
		options.Preferences = &host.PreferenceSource{
			Current: localTheme("light"),
			Subscribe: func(listener func(prefs.TuiLocalPreferences)) func() {
				mu.Lock()
				notify = listener
				mu.Unlock()
				return func() {}
			},
		}
		options.InputTap = func(data string) {
			mu.Lock()
			inputs = append(inputs, data)
			mu.Unlock()
		}
	}})
	// Before activation: the first frame already used the visitor's color mode (true color) and platform; a worker told afterwards would first have drawn
	// in 256 colors.
	first := l.terminal.written()
	if !strings.Contains(first, "\x1b[38;2;") || strings.Contains(first, "\x1b[38;5;") {
		t.Fatalf("the first frame was not drawn with the capabilities it was started with: %q", first)
	}
	state := func() (platform, setting string, colors theme.TerminalState, name string) {
		l.client.Do(func() {
			appearance := l.client.Runtime().Appearance
			platform, setting, colors, name = appearance.Capabilities.Platform, appearance.ThemeSetting, appearance.Terminal, appearance.Theme.Current().Name
		})
		return
	}
	if platform, setting, _, name := state(); platform != "darwin" || setting != "light" || name != "light" {
		t.Fatalf("capabilities and preferences as data: platform %q theme setting %q theme %q", platform, setting, name)
	}
	// A change of the local preferences is posted to the runtime.
	mu.Lock()
	changed := notify
	mu.Unlock()
	if changed == nil {
		t.Fatal("the launcher never subscribed to the preferences")
	}
	changed(localTheme("dark"))
	poll(t, func() bool { _, setting, _, name := state(); return setting == "dark" && name == "dark" })
	// A terminal reply to the launcher's query is consumed by the launcher, never forwarded as input, and its colors reach the runtime.
	l.terminal.SendInput("\x1b]11;rgb:0000/0000/0000\x07")
	poll(t, func() bool {
		_, _, colors, _ := state()
		return colors.Colors.Background != nil && colors.Colors.Background.R == 0
	})
	mu.Lock()
	defer mu.Unlock()
	if len(inputs) != 0 {
		t.Fatalf("a terminal reply reached the runtime as input: %q", inputs)
	}
}
