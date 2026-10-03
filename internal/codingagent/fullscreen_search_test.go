package codingagent

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// useTUIBindings installs user TUI keybindings for one test.
func useTUIBindings(t *testing.T, bindings map[string][]string) {
	t.Helper()
	previous := tui.GetKeybindings()
	tui.SetKeybindings(tui.NewTUIKeybindingsManager(bindings))
	t.Cleanup(func() { tui.SetKeybindings(previous) })
}

func newFullscreenProbe(t *testing.T) *InteractiveMode {
	t.Helper()
	m := newSwitchTuiProbe(t)
	if !m.switchTuiMode("fullscreen", false, true) || m.altScreen == nil {
		t.Fatal("probe did not switch to fullscreen")
	}
	t.Cleanup(func() {
		m.teardownCurrentTui()
		m.altScreen.StopWithOptions(tui.StopOptions{PreserveScreen: true})
	})
	return m
}

// TestFullscreenSearchTakesKeysFromEditor pins the driver routing that stands
// in for upstream's focused-component dispatch: while transcript search has
// focus, typed keys edit the query instead of the editor and the editor emits
// no cursor marker; closing search returns both to the editor.
func TestFullscreenSearchTakesKeysFromEditor(t *testing.T) {
	useTUIBindings(t, map[string][]string{
		tui.KBAltScreenSearch:      {"ctrl+shift+f"},
		tui.KBAltScreenSearchClose: {"escape"},
	})
	m := newFullscreenProbe(t)
	ctx := context.Background()
	if err := m.dispatchKey(ctx, "\x1b[102;6u"); err != nil {
		t.Fatal(err)
	}
	if !m.altScreen.IsSearchFocused() || m.editor.Focused {
		t.Fatalf("search focused=%v editor focused=%v", m.altScreen.IsSearchFocused(), m.editor.Focused)
	}
	for _, key := range []string{"n", "e"} {
		if err := m.dispatchKey(ctx, key); err != nil {
			t.Fatal(err)
		}
	}
	if m.editor.Text() != "" {
		t.Fatalf("editor received search keys: %q", m.editor.Text())
	}
	if err := m.dispatchKey(ctx, "\x1b"); err != nil {
		t.Fatal(err)
	}
	if m.altScreen.IsSearchFocused() || !m.editor.Focused {
		t.Fatal("escape closes search and refocuses the editor")
	}
	if err := m.dispatchKey(ctx, "x"); err != nil {
		t.Fatal(err)
	}
	if m.editor.Text() != "x" {
		t.Fatalf("editor text = %q, want x", m.editor.Text())
	}
}

// TestFullscreenTuiOptionsStyleIndicatorAndMatches pins the themed
// presentation upstream createInteractiveTui passes to TuiAltScreen.
func TestFullscreenTuiOptionsStyleIndicatorAndMatches(t *testing.T) {
	useTUIBindings(t, map[string][]string{tui.KBAltScreenBottom: {"end"}})
	opts := fullscreenTuiOptions()
	label := widthx.StripTerminalSequences(opts.ScrollToEndIndicator())
	if label != " ↓ Jump to latest message · End " {
		t.Fatalf("indicator label = %q", label)
	}
	useTUIBindings(t, map[string][]string{tui.KBAltScreenBottom: {}})
	if label := widthx.StripTerminalSequences(opts.ScrollToEndIndicator()); label != " ↓ Jump to latest message " {
		t.Fatalf("unbound indicator label = %q", label)
	}
	match := opts.SearchMatchStyle("hit")
	current := opts.SearchCurrentMatchStyle("hit")
	if !strings.HasPrefix(match, "\x1b[4m") || !strings.HasPrefix(current, "\x1b[1m") || !strings.Contains(current, "\x1b[7m") {
		t.Fatalf("match styles = %q / %q", match, current)
	}
	if bg := tui.ActiveTheme().Bg("searchMatchBg"); bg != "" && !strings.Contains(match, bg) {
		t.Fatalf("match style lacks the searchMatchBg background: %q", match)
	}
	if got := opts.SearchNavigationButtonStyle("↓", true); got != "\x1b[4m↓\x1b[24m" {
		t.Fatalf("hovered button = %q", got)
	}
	if got := opts.SearchNavigationButtonStyle("↓", false); got != "↓" {
		t.Fatalf("idle button = %q", got)
	}
}

// TestReadClipboardTextCommandOrder ports upstream readClipboardText's Linux
// command chain; macOS and Windows read only through the native helper.
func TestReadClipboardTextCommandOrder(t *testing.T) {
	previous, previousNative := clipboardGOOS, getNativeClipboard
	t.Cleanup(func() { clipboardGOOS, getNativeClipboard = previous, previousNative })
	for _, tc := range []struct {
		name  string
		goos  string
		env   map[string]string
		calls []fakeCall
		want  string
		log   []string
		// native is the getNativeClipboard text; nil models no helper.
		native *string
	}{
		{
			name: "linux falls through failures in order", goos: "linux",
			env:   map[string]string{"TERMUX_VERSION": "1", "WAYLAND_DISPLAY": "wayland-0", "DISPLAY": ":0"},
			calls: []fakeCall{{err: errors.New("no termux")}, {err: errors.New("no wl")}, {err: errors.New("no xclip")}, {out: []byte("text")}},
			want:  "text",
			log:   []string{"termux-clipboard-get ", "wl-paste --no-newline --type text", "xclip -selection clipboard -out", "xsel --clipboard --output"},
		},
		{
			name: "linux stops at an empty success", goos: "linux", env: map[string]string{"WAYLAND_DISPLAY": "w", "DISPLAY": ":0"},
			calls: []fakeCall{{out: []byte{}}}, want: "", log: []string{"wl-paste --no-newline --type text"},
		},
		{name: "headless linux", goos: "linux", env: map[string]string{}, want: ""},
		{name: "darwin", goos: "darwin", want: "mac", native: new("mac")},
		{name: "windows", goos: "windows", want: "win\r\n", native: new("win\r\n")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clipboardGOOS = tc.goos
			withEnv(t, tc.env)
			runner := withRunner(t, &fakeRunner{calls: tc.calls})
			getNativeClipboard = func() *tui.NativeClipboard {
				if tc.native == nil {
					return nil
				}
				return &tui.NativeClipboard{GetText: func(context.Context) (*string, bool, error) { return tc.native, true, nil }}
			}
			if got := readClipboardText(context.Background()); got != tc.want {
				t.Fatalf("text = %q, want %q", got, tc.want)
			}
			if !slices.Equal(runner.log, tc.log) {
				t.Fatalf("commands = %q, want %q", runner.log, tc.log)
			}
		})
	}
}

// TestRightClickPastesIntoFocusedComponent drives handleRightClickPaste: the
// clipboard text reaches the focused editor as a bracketed paste on the owner
// loop.
func TestRightClickPastesIntoFocusedComponent(t *testing.T) {
	useClipboardTextTestSeams(t, "darwin", nil,
		func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("unused") },
		func() *tui.NativeClipboard {
			return nativeTextHelper(func(context.Context) (*string, error) { return new("pasted"), nil })
		},
	)
	m := newFullscreenProbe(t)
	m.altScreen.SetFocus(m.editor)
	m.handleRightClickPaste()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	// Renderer callbacks can precede the clipboard callback in the owner queue.
	for m.editor.Text() == "" {
		select {
		case task := <-m.uiTaskCh:
			task()
		case <-deadline.C:
			t.Fatal("the paste was never posted to the owner loop")
		}
	}
	if m.editor.Text() != "pasted" {
		t.Fatalf("editor text = %q, want pasted", m.editor.Text())
	}
}

func TestRightClickPasteStopsWithRenderer(t *testing.T) {
	started, finished := make(chan struct{}), make(chan struct{})
	useBlockingClipboardBackend(t, "darwin", nil, false, func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		close(finished)
		return ctx.Err()
	})
	m := newFullscreenProbe(t)
	m.altScreen.SetFocus(m.editor)
	m.handleRightClickPaste()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("clipboard read did not start")
	}
	m.teardownCurrentTui()
	select {
	case <-finished:
	default:
		t.Fatal("renderer cleanup did not join clipboard read")
	}
	select {
	case <-m.uiTaskCh:
		t.Fatal("cancelled clipboard read posted a paste")
	default:
	}
}

func TestClipboardReadPropagatesCancellation(t *testing.T) {
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	useBlockingClipboardBackend(t, "darwin", nil, false, func(ctx context.Context) error {
		close(started)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-release:
			return errors.New("test cleanup")
		}
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { defer close(done); readClipboardText(ctx) }()
	t.Cleanup(func() { close(release); <-done })
	<-started
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("clipboard command ignored renderer cancellation")
	}
}

func TestRightClickPasteWaitsForOwnerQueueCapacity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		useClipboardTextTestSeams(t, "darwin", nil,
			func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("unused") },
			func() *tui.NativeClipboard {
				return nativeTextHelper(func(context.Context) (*string, error) { return new("queued paste"), nil })
			},
		)
		m := newFullscreenProbe(t)
		m.altScreen.SetFocus(m.editor)
	fillQueue:
		for {
			select {
			case m.uiTaskCh <- func() {}:
			default:
				break fillQueue
			}
		}
		if len(m.uiTaskCh) != cap(m.uiTaskCh) {
			t.Fatal("owner queue must be full before the clipboard read")
		}
		m.handleRightClickPaste()
		done := make(chan struct{})
		go func() { m.clipboardReads.Wait(); close(done) }()
		synctest.Wait()
		select {
		case <-done:
			t.Fatal("paste was dropped when the owner queue was full")
		default:
		}
		<-m.uiTaskCh
		synctest.Wait()
		<-done
		for len(m.uiTaskCh) > 0 {
			(<-m.uiTaskCh)()
		}
		if got := m.editor.Text(); got != "queued paste" {
			t.Fatalf("editor = %q, want queued paste", got)
		}
	})
}
