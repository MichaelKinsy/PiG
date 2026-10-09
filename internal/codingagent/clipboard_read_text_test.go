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
	native func() *tui.NativeClipboard,
) {
	t.Helper()
	oldGOOS, oldEnv, oldRun, oldNative := clipboardGOOS, clipboardEnv, clipboardRun, getNativeClipboard
	t.Cleanup(func() {
		clipboardGOOS, clipboardEnv, clipboardRun, getNativeClipboard = oldGOOS, oldEnv, oldRun, oldNative
	})
	clipboardGOOS = goos
	clipboardEnv = fakeEnvLookup(env)
	clipboardRun = run
	getNativeClipboard = native
}

// nativeTextHelper is the getNativeClipboard() result whose getText is read.
func nativeTextHelper(read func(context.Context) (*string, error)) *tui.NativeClipboard {
	return &tui.NativeClipboard{GetText: func(ctx context.Context) (*string, bool, error) {
		text, err := read(ctx)
		return text, true, err
	}}
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
		func() *tui.NativeClipboard {
			return nativeTextHelper(func(context.Context) (*string, error) {
				calls++
				if calls == 1 {
					return &want, nil
				}
				return nil, errors.New("clipboard unavailable")
			})
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
	}
	for _, tc := range cases {
		for _, text := range []string{"clipboard text", ""} {
			t.Run(fmt.Sprintf("%s result %q", tc.command, text), func(t *testing.T) {
				var calls []string
				var gotArgs []string
				var gotTimeout time.Duration
				nativeCalls := 0
				useClipboardTextTestSeams(t, "linux", map[string]string{"DISPLAY": ":0", tc.env: "1"},
					func(ctx context.Context, name string, args ...string) ([]byte, error) {
						calls = append(calls, name)
						if name == tc.command {
							gotArgs = slices.Clone(args)
							// Upstream asserts the last command ran with { timeoutMs: 5000 }.
							if deadline, ok := ctx.Deadline(); ok {
								gotTimeout = time.Until(deadline)
							}
							return []byte(text), nil
						}
						return nil, errors.New("unavailable")
					},
					func() *tui.NativeClipboard {
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
				if gotTimeout <= 4*time.Second || gotTimeout > clipboardTextTimeout {
					t.Fatalf("%s timeout remaining = %v, want within (4s, 5s]", tc.command, gotTimeout)
				}
				if nativeCalls != 0 {
					t.Fatal("native clipboard consulted after a successful command")
				}
			})
		}
	}
}

// Upstream: "Termux reads termux-clipboard-get result %j on Android"
// (clipboard.test.ts, #10391). Termux reports the platform "android", so the
// command runs outside the Linux-only branch and the native helper stays
// unconsulted.
func TestReadClipboardTextTermuxReadsOnAndroid(t *testing.T) {
	for _, text := range []string{"clipboard text", ""} {
		t.Run(fmt.Sprintf("result %q", text), func(t *testing.T) {
			var calls []string
			var gotArgs []string
			var gotTimeout time.Duration
			nativeCalls := 0
			useClipboardTextTestSeams(t, "android", map[string]string{"TERMUX_VERSION": "0.119"},
				func(ctx context.Context, name string, args ...string) ([]byte, error) {
					calls = append(calls, name)
					gotArgs = slices.Clone(args)
					if deadline, ok := ctx.Deadline(); ok {
						gotTimeout = time.Until(deadline)
					}
					return []byte(text), nil
				},
				func() *tui.NativeClipboard {
					nativeCalls++
					return nil
				},
			)
			if got := readClipboardText(t.Context()); got != text {
				t.Fatalf("read = %q, want %q", got, text)
			}
			if !slices.Equal(calls, []string{"termux-clipboard-get"}) || len(gotArgs) != 0 {
				t.Fatalf("calls = %v args = %v, want exactly termux-clipboard-get", calls, gotArgs)
			}
			if gotTimeout <= 4*time.Second || gotTimeout > clipboardTextTimeout {
				t.Fatalf("timeout remaining = %v, want within (4s, 5s]", gotTimeout)
			}
			if nativeCalls != 0 {
				t.Fatal("native clipboard consulted after a successful command")
			}
		})
	}
}

// Android without Termux has no command backend, and Android never reaches
// the Linux Wayland/X11 branch even when the variables are set (upstream
// readClipboardText: only the Termux command sits outside platform() === "linux").
func TestReadClipboardTextAndroidSkipsLinuxCommands(t *testing.T) {
	var calls []string
	useClipboardTextTestSeams(t, "android", map[string]string{"WAYLAND_DISPLAY": "wayland-0", "DISPLAY": ":0"},
		func(_ context.Context, name string, _ ...string) ([]byte, error) {
			calls = append(calls, name)
			return []byte("x"), nil
		},
		func() *tui.NativeClipboard {
			return nativeTextHelper(func(context.Context) (*string, error) { return new("native"), nil })
		},
	)
	if got := readClipboardText(t.Context()); got != "native" || len(calls) != 0 {
		t.Fatalf("read = %q, commands = %v, want native and no commands", got, calls)
	}
}

// Upstream: "uses native X11 after command failures: %j", with getNativeClipboard mocked as upstream does.
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
			nativeLookups := 0
			useClipboardTextTestSeams(t, "linux", map[string]string{"DISPLAY": ":0", "WAYLAND_DISPLAY": "wayland-0"},
				func(_ context.Context, name string, _ ...string) ([]byte, error) {
					calls = append(calls, name)
					return nil, errors.New("unavailable")
				},
				func() *tui.NativeClipboard {
					nativeLookups++
					return nativeTextHelper(func(context.Context) (*string, error) { return tc.text, nil })
				},
			)
			if got := readClipboardText(t.Context()); got != tc.want {
				t.Fatalf("read = %q, want %q", got, tc.want)
			}
			if !slices.Equal(calls, []string{"wl-paste", "xclip", "xsel"}) || nativeLookups != 1 {
				t.Fatalf("calls = %v native lookups = %d, want one", calls, nativeLookups)
			}
		})
	}
}

// Upstream: "falls back to X11 tools when wl-paste is unavailable".
func TestReadClipboardTextFallsBackToX11ToolsWhenWlPasteIsUnavailable(t *testing.T) {
	var calls []string
	nativeLookups := 0
	useClipboardTextTestSeams(t, "linux", map[string]string{"WAYLAND_DISPLAY": "wayland-0", "DISPLAY": ":0"},
		func(_ context.Context, name string, _ ...string) ([]byte, error) {
			calls = append(calls, name)
			if name == "wl-paste" {
				return nil, errors.New("unavailable")
			}
			return []byte("X11 text"), nil
		},
		func() *tui.NativeClipboard { nativeLookups++; return nil },
	)
	if got := readClipboardText(t.Context()); got != "X11 text" {
		t.Fatalf("read = %q, want X11 text", got)
	}
	if !slices.Equal(calls, []string{"wl-paste", "xclip"}) || nativeLookups != 0 {
		t.Fatalf("calls = %v native lookups = %d", calls, nativeLookups)
	}
}

// ctrlVImageBackends are the platform image readers Ctrl+V can block on. Each
// test runs against every one on any host, faking whichever read the
// backend runs first; DISPLAY gives the Linux reader an X11 backend.
var ctrlVImageBackends = []struct {
	name  string
	goos  string
	env   map[string]string
	image bool
}{
	{"darwin-image", "darwin", nil, true},
	{"darwin-text", "darwin", nil, false},
	{"linux-command", "linux", map[string]string{"DISPLAY": ":0"}, false},
}

// useBlockingClipboardBackend exercises blocking native image reads, native text
// fallback, and Linux command reads without reaching the host clipboard.
func useBlockingClipboardBackend(t *testing.T, goos string, env map[string]string, image bool, block func(context.Context) error) {
	t.Helper()
	oldGOOS, oldRun, oldNative := clipboardGOOS, clipboardRun, getNativeClipboard
	t.Cleanup(func() { clipboardGOOS, clipboardRun, getNativeClipboard = oldGOOS, oldRun, oldNative })
	clipboardGOOS = goos
	withEnv(t, env)
	clipboardRun = func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
		return nil, block(ctx)
	}
	getNativeClipboard = func() *tui.NativeClipboard {
		return &tui.NativeClipboard{
			GetImage: func(ctx context.Context) ([]byte, bool, error) {
				if image {
					return nil, true, block(ctx)
				}
				return nil, true, nil
			},
			GetText: func(ctx context.Context) (*string, bool, error) { return nil, true, block(ctx) },
		}
	}
}

// Ctrl+V falls back to clipboard text in the regular renderer as well as
// fullscreen. Upstream handleClipboardPaste is renderer-independent.
func TestCtrlVPasteDoesNotBlockOwnerLoop(t *testing.T) {
	for _, backend := range ctrlVImageBackends {
		t.Run(backend.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				started := make(chan struct{})
				startOnce := sync.OnceFunc(func() { close(started) })
				release := make(chan struct{})
				useBlockingClipboardBackend(t, backend.goos, backend.env, backend.image, func(ctx context.Context) error {
					startOnce()
					select {
					case <-release:
						return errors.New("no image")
					case <-ctx.Done():
						return ctx.Err()
					}
				})

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
		t.Run(backend.name, func(t *testing.T) {
			started := make(chan struct{})
			startOnce := sync.OnceFunc(func() { close(started) })
			cancelled := make(chan struct{})
			cancelOnce := sync.OnceFunc(func() { close(cancelled) })
			useBlockingClipboardBackend(t, backend.goos, backend.env, backend.image, func(ctx context.Context) error {
				startOnce()
				<-ctx.Done()
				cancelOnce()
				return ctx.Err()
			})

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
	useClipboardTextTestSeams(t, "darwin", nil,
		func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("no image") },
		func() *tui.NativeClipboard {
			return &tui.NativeClipboard{
				GetImage: func(context.Context) ([]byte, bool, error) { return nil, true, nil },
				GetText:  func(context.Context) (*string, bool, error) { return new("paste"), true, nil },
			}
		},
	)
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
		func() *tui.NativeClipboard {
			return nativeTextHelper(func(context.Context) (*string, error) {
				nativeCalls.Add(1)
				return new("paste"), nil
			})
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
		func() *tui.NativeClipboard {
			return nativeTextHelper(func(ctx context.Context) (*string, error) {
				close(started)
				select {
				case <-release:
					return new("stale"), nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			})
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

// Ports packages/coding-agent/test/clipboard.test.ts:93 at upstream's mock boundary: getNativeClipboard returns a helper whose getText supplies the text after every command fails, on every platform.
func TestReadClipboardTextUsesGetNativeClipboardText(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "windows"} {
		t.Run(goos, func(t *testing.T) {
			lookups := 0
			var commands []string
			old := getNativeClipboard
			t.Cleanup(func() { getNativeClipboard = old })
			getNativeClipboard = func() *tui.NativeClipboard {
				lookups++
				return &tui.NativeClipboard{GetText: func(context.Context) (*string, bool, error) { return new("native text"), true, nil }}
			}
			oldGOOS, oldEnv, oldRun := clipboardGOOS, clipboardEnv, clipboardRun
			t.Cleanup(func() { clipboardGOOS, clipboardEnv, clipboardRun = oldGOOS, oldEnv, oldRun })
			clipboardGOOS = goos
			clipboardEnv = fakeEnvLookup(map[string]string{"DISPLAY": ":0"})
			clipboardRun = func(_ context.Context, name string, _ ...string) ([]byte, error) {
				commands = append(commands, name)
				return nil, errors.New("unavailable")
			}
			if got := readClipboardText(t.Context()); got != "native text" || lookups != 1 {
				t.Fatalf("read=%q lookups=%d commands=%v", got, lookups, commands)
			}
		})
	}
}

func TestHostNativeClipboardTextWrapsGetNativeClipboard(t *testing.T) {
	old := getNativeClipboard
	t.Cleanup(func() { getNativeClipboard = old })
	getNativeClipboard = func() *tui.NativeClipboard { return nil }
	if hostNativeClipboardText() != nil {
		t.Fatal("missing helper produced a reader")
	}
	getNativeClipboard = func() *tui.NativeClipboard { return &tui.NativeClipboard{} }
	if hostNativeClipboardText() != nil {
		t.Fatal("helper without GetText produced a reader")
	}
	failure := errors.New("boom")
	for _, tc := range []struct {
		name      string
		value     *string
		available bool
		err       error
		want      *string
		wantErr   error
	}{
		{"available", new("text"), true, nil, new("text"), nil},
		{"empty", new(""), true, nil, new(""), nil},
		{"unavailable", new("stale"), false, nil, nil, nil},
		{"error", nil, true, failure, nil, failure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			getNativeClipboard = func() *tui.NativeClipboard {
				return &tui.NativeClipboard{GetText: func(context.Context) (*string, bool, error) { return tc.value, tc.available, tc.err }}
			}
			read := hostNativeClipboardText()
			if read == nil {
				t.Fatal("no reader")
			}
			got, err := read(t.Context())
			if !errors.Is(err, tc.wantErr) || (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
				t.Fatalf("got=%v err=%v", got, err)
			}
		})
	}
}

// Upstream readClipboardText decodes command stdout with Buffer.toString("utf8"): each maximal ill-formed subpart becomes one U+FFFD.
func TestReadClipboardTextDecodesCommandBytesLikeBufferToString(t *testing.T) {
	useClipboardTextTestSeams(t, "linux", map[string]string{"WAYLAND_DISPLAY": "1"},
		func(ctx context.Context, name string, args ...string) ([]byte, error) {
			if name == "wl-paste" {
				return []byte("a\xff\xfeb"), nil
			}
			return nil, errors.New("unavailable")
		},
		func() *tui.NativeClipboard { return nil },
	)
	if got, want := readClipboardText(t.Context()), "a��b"; got != want {
		t.Fatalf("read = %q, want %q", got, want)
	}
}
