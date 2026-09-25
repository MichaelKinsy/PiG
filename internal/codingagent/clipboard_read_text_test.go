package codingagent

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

func useClipboardTextTestSeams(
	t *testing.T,
	goos string,
	env map[string]string,
	run clipboardRunner,
	native func() nativeClipboardText,
) {
	t.Helper()
	oldGOOS, oldEnv, oldRun, oldNative := clipboardGOOS, clipboardEnv, clipboardRun, getNativeClipboardText
	t.Cleanup(func() {
		clipboardGOOS, clipboardEnv, clipboardRun, getNativeClipboardText = oldGOOS, oldEnv, oldRun, oldNative
	})
	clipboardGOOS = goos
	clipboardEnv = fakeEnvLookup(env)
	clipboardRun = run
	getNativeClipboardText = native
}

// Upstream: "awaits native clipboard text and catches rejected reads".
func TestReadClipboardTextAwaitsNativeTextAndCatchesRejectedReads(t *testing.T) {
	calls := 0
	want := "clipboard text"
	useClipboardTextTestSeams(t, "darwin", nil,
		func(context.Context, string, ...string) ([]byte, error) {
			t.Fatal("native test unexpectedly ran a platform command")
			return nil, nil
		},
		func() nativeClipboardText {
			return func(context.Context) (*string, error) {
				calls++
				if calls == 1 {
					return &want, nil
				}
				return nil, errors.New("clipboard unavailable")
			}
		},
	)
	if got := readClipboardText(t.Context()); got != want {
		t.Fatalf("read = %q, want %q", got, want)
	}
	if got := readClipboardText(t.Context()); got != "" {
		t.Fatalf("rejected native read = %q, want empty", got)
	}
}

// Upstream: "<command> result %j stops fallback". A successful empty
// Wayland result must not fall through to stale X11 content.
func TestReadClipboardTextCommandResultStopsFallback(t *testing.T) {
	cases := []struct {
		env     string
		command string
		args    []string
		calls   []string
	}{
		{"WAYLAND_DISPLAY", "wl-paste", []string{"--no-newline", "--type", "text"}, []string{"wl-paste"}},
		{"DISPLAY", "xclip", []string{"-selection", "clipboard", "-out"}, []string{"xclip"}},
		{"DISPLAY", "xsel", []string{"--clipboard", "--output"}, []string{"xclip", "xsel"}},
		{"TERMUX_VERSION", "termux-clipboard-get", nil, []string{"termux-clipboard-get"}},
	}
	for _, tc := range cases {
		for _, text := range []string{"clipboard text", ""} {
			t.Run(fmt.Sprintf("%s result %q", tc.command, text), func(t *testing.T) {
				var calls []string
				var gotArgs []string
				nativeCalls := 0
				useClipboardTextTestSeams(t, "linux", map[string]string{"DISPLAY": ":0", tc.env: "1"},
					func(_ context.Context, name string, args ...string) ([]byte, error) {
						calls = append(calls, name)
						if name == tc.command {
							gotArgs = slices.Clone(args)
							return []byte(text), nil
						}
						return nil, errors.New("unavailable")
					},
					func() nativeClipboardText {
						nativeCalls++
						return nil
					},
				)
				if got := readClipboardText(t.Context()); got != text {
					t.Fatalf("read = %q, want %q", got, text)
				}
				if !slices.Equal(calls, tc.calls) || !slices.Equal(gotArgs, tc.args) {
					t.Fatalf("calls = %v args = %v, want %v %v", calls, gotArgs, tc.calls, tc.args)
				}
				if nativeCalls != 0 {
					t.Fatal("native clipboard consulted after a successful command")
				}
			})
		}
	}
}

// Upstream: "uses native X11 after command failures: %j". This exercises the
// injectable native adapter contract; hostNativeClipboardText has no Linux
// implementation, so PORT_MAP records the production residual separately.
func TestReadClipboardTextUsesInjectedNativeX11AfterCommandFailures(t *testing.T) {
	for _, tc := range []struct {
		name string
		text *string
		want string
	}{
		{"native text", new("native text"), "native text"},
		{"empty", new(""), ""},
		{"null", nil, ""},
		{"undefined", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			useClipboardTextTestSeams(t, "linux", map[string]string{"DISPLAY": ":0", "WAYLAND_DISPLAY": "wayland-0"},
				func(_ context.Context, name string, _ ...string) ([]byte, error) {
					calls = append(calls, name)
					return nil, errors.New("unavailable")
				},
				func() nativeClipboardText {
					return func(context.Context) (*string, error) { return tc.text, nil }
				},
			)
			if got := readClipboardText(t.Context()); got != tc.want {
				t.Fatalf("read = %q, want %q", got, tc.want)
			}
			if !slices.Equal(calls, []string{"wl-paste", "xclip", "xsel"}) {
				t.Fatalf("calls = %v", calls)
			}
		})
	}
}

func TestHostNativeClipboardTextReturnsNilOnLinux(t *testing.T) {
	oldGOOS := clipboardGOOS
	t.Cleanup(func() { clipboardGOOS = oldGOOS })
	clipboardGOOS = "linux"
	if native := hostNativeClipboardText(); native != nil {
		t.Fatal("Linux unexpectedly exposed a production native clipboard adapter")
	}
}

// Upstream: "falls back to X11 tools when wl-paste is unavailable".
func TestReadClipboardTextFallsBackToX11ToolsWhenWlPasteIsUnavailable(t *testing.T) {
	var calls []string
	useClipboardTextTestSeams(t, "linux", map[string]string{"WAYLAND_DISPLAY": "wayland-0", "DISPLAY": ":0"},
		func(_ context.Context, name string, _ ...string) ([]byte, error) {
			calls = append(calls, name)
			if name == "wl-paste" {
				return nil, errors.New("unavailable")
			}
			return []byte("X11 text"), nil
		},
		func() nativeClipboardText { return nil },
	)
	if got := readClipboardText(t.Context()); got != "X11 text" {
		t.Fatalf("read = %q, want X11 text", got)
	}
	if !slices.Equal(calls, []string{"wl-paste", "xclip"}) {
		t.Fatalf("calls = %v", calls)
	}
}

// ctrlVImageBackends are the platform image readers Ctrl+V can block on. Each
// test runs against every one on any host, faking whichever command the
// backend runs first; DISPLAY gives the Linux reader an X11 backend.
var ctrlVImageBackends = []struct {
	goos string
	env  map[string]string
}{
	{"darwin", nil},
	{"linux", map[string]string{"DISPLAY": ":0"}},
}

// Ctrl+V falls back to clipboard text in the regular renderer as well as
// fullscreen. Upstream handleClipboardPaste is renderer-independent.
func TestCtrlVPasteDoesNotBlockOwnerLoop(t *testing.T) {
	for _, backend := range ctrlVImageBackends {
		t.Run(backend.goos, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				oldGOOS, oldRun := clipboardGOOS, clipboardRun
				t.Cleanup(func() { clipboardGOOS, clipboardRun = oldGOOS, oldRun })
				clipboardGOOS = backend.goos
				withEnv(t, backend.env)

				started := make(chan struct{})
				startOnce := sync.OnceFunc(func() { close(started) })
				release := make(chan struct{})
				clipboardRun = func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
					startOnce()
					select {
					case <-release:
						return nil, errors.New("no image")
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				}

				m := newSwitchTuiProbe(t)
				m.tuiInst.CancelPendingRender()
				t.Cleanup(m.teardownCurrentTui)
				done := make(chan struct{})
				go func() {
					m.handleClipboardImagePaste()
					close(done)
				}()
				<-started
				synctest.Wait()
				returned := false
				select {
				case <-done:
					returned = true
				default:
					t.Error("Ctrl+V blocked the owner loop on clipboard image I/O")
				}
				close(release)
				if !returned {
					<-done
				}
			})
		})
	}
}

func TestCtrlVPasteTeardownCancelsAndJoinsImageRead(t *testing.T) {
	for _, backend := range ctrlVImageBackends {
		t.Run(backend.goos, func(t *testing.T) {
			oldGOOS, oldRun := clipboardGOOS, clipboardRun
			t.Cleanup(func() { clipboardGOOS, clipboardRun = oldGOOS, oldRun })
			clipboardGOOS = backend.goos
			withEnv(t, backend.env)

			started := make(chan struct{})
			startOnce := sync.OnceFunc(func() { close(started) })
			cancelled := make(chan struct{})
			cancelOnce := sync.OnceFunc(func() { close(cancelled) })
			clipboardRun = func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
				startOnce()
				<-ctx.Done()
				cancelOnce()
				return nil, ctx.Err()
			}

			m := newSwitchTuiProbe(t)
			m.tuiInst.CancelPendingRender()
			m.handleClipboardImagePaste()
			<-started
			m.teardownCurrentTui()
			select {
			case <-cancelled:
			default:
				t.Fatal("renderer teardown returned before the clipboard image read was cancelled")
			}
		})
	}
}

func TestCtrlVTextFallbackRegularMode(t *testing.T) {
	oldGOOS, oldRun := clipboardGOOS, clipboardRun
	t.Cleanup(func() { clipboardGOOS, clipboardRun = oldGOOS, oldRun })
	clipboardGOOS = "darwin"
	clipboardRun = func(_ context.Context, name string, _ ...string) ([]byte, error) {
		if name == "pbpaste" {
			return []byte("paste"), nil
		}
		return nil, errors.New("no image")
	}
	m := newSwitchTuiProbe(t)
	m.tuiInst.CancelPendingRender()
	t.Cleanup(m.teardownCurrentTui)
	m.editor.SetText("before ")
	m.handleClipboardImagePaste()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for m.editor.Text() != "before paste" {
		select {
		case task := <-m.uiTaskCh:
			task()
		case <-deadline.C:
			t.Fatalf("editor = %q, want Ctrl+V text fallback in regular mode", m.editor.Text())
		}
	}
}

// Ctrl+V and right-click share the context-owned reader. Both reads are joined
// by renderer teardown; right-click retains its focus check and bracketed paste.
func TestClipboardTextCtrlVAndRightClickShareOwnedReader(t *testing.T) {
	var nativeCalls atomic.Int32
	useClipboardTextTestSeams(t, "darwin", nil,
		func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("no image") },
		func() nativeClipboardText {
			return func(context.Context) (*string, error) {
				nativeCalls.Add(1)
				return new("paste"), nil
			}
		},
	)
	m := newFullscreenProbe(t)
	m.altScreen.SetFocus(m.editor)
	m.editor.SetText("before ")
	m.handleClipboardImagePaste()
	m.handleRightClickPaste()

	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for m.editor.Text() != "before pastepaste" {
		select {
		case task := <-m.uiTaskCh:
			task()
		case <-deadline.C:
			t.Fatalf("editor = %q calls = %d, want both paste paths", m.editor.Text(), nativeCalls.Load())
		}
	}
	m.clipboardReads.Wait()
	if nativeCalls.Load() != 2 {
		t.Fatalf("native reads = %d, want 2", nativeCalls.Load())
	}
}

func TestRightClickPasteDropsResultAfterFocusChange(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	useClipboardTextTestSeams(t, "darwin", nil,
		func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("unused") },
		func() nativeClipboardText {
			return func(ctx context.Context) (*string, error) {
				close(started)
				select {
				case <-release:
					return new("stale"), nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
		},
	)
	m := newFullscreenProbe(t)
	m.altScreen.SetFocus(m.editor)
	m.handleRightClickPaste()
	<-started
	m.altScreen.SetFocus(tui.NewEditor())
	close(release)

	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case task := <-m.uiTaskCh:
			task()
			m.clipboardReads.Wait()
			if m.editor.Text() != "" {
				t.Fatalf("stale paste reached old focus: %q", m.editor.Text())
			}
			return
		case <-deadline.C:
			t.Fatal("clipboard result was not posted")
		}
	}
}
