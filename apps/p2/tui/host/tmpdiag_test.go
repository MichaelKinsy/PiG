package host_test

import (
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/prefs"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/host"
	pitui "github.com/MichaelKinsy/PiG/tui"
)

func TestTmpDiag(t *testing.T) {
	for _, key := range []string{"\x1bOQ", "\x1b[1;1Q", "\x1b[13~", "\x0c"} {
		l := startReal(t, launcherOptions{preferences: &prefs.TuiLocalPreferences{Keybindings: pitui.KeybindingsConfig{"app.model.select": {"f2"}}, Settings: prefs.TuiSettings{}, Problems: []string{}}, options: func(o *host.ClientOptions) { o.Capabilities = &host.Capabilities{TrueColor: true, Platform: "linux"} }})
		l.waitScreen(15*time.Second, "New session")
		time.Sleep(500 * time.Millisecond)
		l.terminal.SendInput(key)
		time.Sleep(1500 * time.Millisecond)
		t.Logf("key %q shows model: %v\n%s", key, contains(l.terminal.screen(), "Choose default model"), l.terminal.screen())
	}
}
