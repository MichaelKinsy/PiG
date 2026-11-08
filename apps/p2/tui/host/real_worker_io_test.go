package host_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/host"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/prefs"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/theme"
	pitui "github.com/MichaelKinsy/PiG/tui"
)

func TestRealWorkerTheWheelScrollsTheTranscriptADragSelectsItTheReleaseCopiesTheSelectedTextOnce(t *testing.T) {
	l := startReal(t, launcherOptions{mouse: host.MouseOn, columns: 80, rows: 16})
	l.waitScreen(15*time.Second, "New session")
	if !contains(l.terminal.written(), "\x1b[?1000h") {
		t.Fatal("pi-tui captures the mouse")
	}
	l.openNewestSession()
	for _, word := range []string{"alpha", "bravo", "charlie", "delta"} {
		l.typed(word)
		l.terminal.SendInput(keyEnter)
		l.waitScreen(20*time.Second, "Echo: "+word)
	}
	poll(t, func() bool { return !contains(l.terminal.screen(), "Echo: alpha") })
	// Wheel up scrolls the transcript back (pi: "auto" lines per notch).
	for index := 0; index < 30 && !contains(l.terminal.screen(), "Echo: alpha"); index++ {
		l.terminal.SendInput(wheelUp(10, 4))
		time.Sleep(40 * time.Millisecond)
	}
	l.waitScreen(5*time.Second, "Echo: alpha")
	// Drag across "Echo: alpha" selects it in pi-tui; the release copies it, once.
	rows := l.terminal.Viewport()
	row, column := -1, -1
	for index, line := range rows {
		if at := strings.Index(line, "Echo: alpha"); at >= 0 {
			row, column = index, at
			break
		}
	}
	l.terminal.SendInput(press(column+1, row+1))
	l.terminal.SendInput(drag(column+6, row+1))
	l.terminal.SendInput(drag(column+11, row+1))
	if len(l.copied()) != 0 {
		t.Fatalf("copied before the release: %v", l.copied())
	}
	l.terminal.SendInput(release(column+11, row+1))
	poll(t, func() bool { return len(l.copied()) == 1 })
	if l.copied()[0] != "Echo: alpha" {
		t.Fatalf("copied %q", l.copied())
	}
	time.Sleep(1500 * time.Millisecond)
	if len(l.copied()) != 1 {
		t.Fatalf("copied more than once: %v", l.copied())
	}
	l.terminal.clear()
	l.client.Close()
	if !contains(l.terminal.written(), "\x1b[?1000l") {
		t.Fatal("mouse modes undone on exit")
	}
}

func TestRealWorkerAttachAndCtrlVUploadAfterYChipsShowThemBackspaceRemovesOneTheNextSendCarriesThem(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "report.txt")
	if err := os.WriteFile(file, []byte("quarterly numbers"), 0o600); err != nil {
		t.Fatal(err)
	}
	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}
	l := startReal(t, launcherOptions{actionOptions: func(options *host.LauncherActionOptions) {
		options.ReadClipboard = &host.ClipboardReaders{
			Files: func() []string { return nil },
			Image: func() *host.ClipboardImage { return &host.ClipboardImage{Bytes: png, MimeType: "image/png"} },
			Text:  func() string { return "" },
		}
	}})
	l.openNewestSession()
	l.typed("/attach " + file)
	l.terminal.SendInput(keyEnter)
	l.waitScreen(10*time.Second, "Show this local file for upload")
	l.terminal.SendInput("y")
	l.waitScreen(10*time.Second, "Upload a local file to this session")
	if !contains(l.terminal.screen(), "17 B") {
		t.Fatal("the confirmation shows the size")
	}
	l.terminal.SendInput("y")
	l.waitScreen(10*time.Second, "[report.txt 17 B]")
	l.typed("summarize")
	l.terminal.SendInput(keyEnter)
	l.waitScreen(20*time.Second, "Echo: summarize")
	poll(t, func() bool { return !contains(l.terminal.screen(), "[report.txt 17 B]") })
	// The message carried the upload (the server's input entry lists it).
	l.waitScreen(10*time.Second, "report.txt")
	// Ctrl+V with an image: the launcher's confirmation, then a chip from the launcher's `attached` answer.
	l.terminal.SendInput("\x16")
	l.waitScreen(10*time.Second, "Upload the clipboard image to this session")
	l.terminal.SendInput("y")
	chip := regexp.MustCompile(`\[clipboard-[^\]]+\.png 8 B\]`)
	pollFor(t, 10*time.Second, func() bool { return chip.MatchString(l.terminal.screen()) })
	// Backspace on the empty editor removes the last attachment.
	l.terminal.SendInput("\x7f")
	poll(t, func() bool { return !contains(l.terminal.screen(), ".png 8 B]") })
}

func TestSettingsEndToEndTheLauncherShowsTheDiffWritesPrefsJSONAndTheWatcherAppliesTheNewTheme(t *testing.T) {
	directory := t.TempDir()
	sources := host.LocalPreferenceSources{AgentDir: filepath.Join(directory, "agent"), ConfigDir: filepath.Join(directory, "p2-tui")}
	current := host.LoadLocalPreferences(sources)
	l := startReal(t, launcherOptions{columns: 100, rows: 40, options: func(options *host.ClientOptions) {
		options.Capabilities = &host.Capabilities{TrueColor: true, Platform: "linux"}
		options.Preferences = &host.PreferenceSource{Current: current, Subscribe: func(listener func(prefs.TuiLocalPreferences)) func() {
			return host.WatchLocalPreferences(sources, listener, current)
		}}
	}, actionOptions: func(options *host.LauncherActionOptions) { options.PrefsDirectory = sources.ConfigDir }})
	l.openNewestSession()
	l.typed("/settings")
	l.terminal.SendInput(keyEnter)
	l.waitScreen(10*time.Second, "Hide thinking")
	// Theme → light (system, dark, light), then Hide thinking off, then Esc proposes both.
	l.terminal.SendInput(keyEnter)
	l.waitScreen(10*time.Second, "Preview")
	l.terminal.SendInput(keyDown)
	l.terminal.SendInput(keyDown)
	l.terminal.SendInput(keyEnter)
	l.waitScreen(10*time.Second, "Hide thinking")
	l.terminal.SendInput(keyDown)
	l.terminal.SendInput(keyEnter)
	l.terminal.SendInput(keyEsc)
	l.waitScreen(10*time.Second, "Save your TUI preferences on this computer")
	screen := l.terminal.screen()
	if !contains(screen, "+ settings.hideThinkingBlock: false") || !contains(screen, `+ theme: "light"`) {
		t.Fatalf("the diff is not shown:\n%s", screen)
	}
	// The light theme's muted color (the live system theme uses the terminal's palette indices instead).
	light, err := theme.CreateTheme(theme.LightTheme, pitui.TerminalColorModeTrueColor, theme.TerminalState{})
	if err != nil {
		t.Fatal(err)
	}
	muted := light.GetFgAnsi("muted")
	l.terminal.clear()
	l.terminal.SendInput("y")
	poll(t, func() bool {
		text, err := os.ReadFile(filepath.Join(sources.ConfigDir, "prefs.json"))
		if err != nil {
			return false
		}
		var saved struct{ Theme string }
		return json.Unmarshal(text, &saved) == nil && saved.Theme == "light"
	})
	// The launcher's watcher posts the saved file; the runtime repaints in the light theme.
	pollFor(t, 10*time.Second, func() bool { return contains(l.terminal.written(), muted) })
	l.waitScreen(10*time.Second, "Preferences saved")
}
