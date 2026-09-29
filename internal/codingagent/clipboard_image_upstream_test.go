package codingagent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/tui"
)

type clipboardImageFixture struct {
	command                 func(string, []string) ([]byte, error)
	getImage                func() ([]byte, bool, error)
	imageContext            context.Context
	commands                []string
	helperCalls, imageCalls int
	missing                 bool
}

var upstreamClipboardPNG = []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 13, 0x49, 0x48, 0x44, 0x52}

func newClipboardImageFixture(t *testing.T, platform string, env map[string]string) *clipboardImageFixture {
	t.Helper()
	oldRun, oldEnv, oldPlatform, oldNative, oldRead := clipboardRun, clipboardEnv, clipboardGOOS, getNativeClipboard, clipboardReadFile
	t.Cleanup(func() {
		clipboardRun, clipboardEnv, clipboardGOOS, getNativeClipboard, clipboardReadFile = oldRun, oldEnv, oldPlatform, oldNative, oldRead
	})
	clipboardGOOS = platform
	if platform == "win32" {
		clipboardGOOS = "windows"
	}
	clipboardEnv = func(key string) string { return env[key] }
	clipboardReadFile = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	fixture := &clipboardImageFixture{command: func(string, []string) ([]byte, error) { return nil, errors.New("command failed") }, getImage: func() ([]byte, bool, error) { return upstreamClipboardPNG, true, nil }}
	clipboardRun = func(_ context.Context, name string, args ...string) ([]byte, error) {
		fixture.commands = append(fixture.commands, name)
		return fixture.command(name, args)
	}
	getNativeClipboard = func() *tui.NativeClipboard {
		fixture.helperCalls++
		if fixture.missing {
			return nil
		}
		return &tui.NativeClipboard{GetImage: func(ctx context.Context) ([]byte, bool, error) {
			fixture.imageCalls++
			fixture.imageContext = ctx
			return fixture.getImage()
		}}
	}
	return fixture
}

func assertClipboardImage(t *testing.T, want []byte) {
	t.Helper()
	data, mime, err := ReadClipboardImageContext(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if want == nil {
		if data != nil || mime != "" {
			t.Fatalf("image=%v mime=%q, want null", data, mime)
		}
	} else if !slices.Equal(data, want) || mime != "image/png" {
		t.Fatalf("image=%v mime=%q", data, mime)
	}
}

func TestUpstreamClipboardImage(t *testing.T) {
	for _, backend := range []struct {
		name, command string
		env           map[string]string
	}{{"wayland", "wl-paste", map[string]string{"WAYLAND_DISPLAY": "1", "DISPLAY": ":0"}}, {"x11", "xclip", map[string]string{"DISPLAY": ":0"}}} {
		for _, present := range []bool{true, false} {
			// .upstream/v0.87.1/packages/coding-agent/test/clipboard-image.test.ts:33.
			t.Run(fmt.Sprintf("%s: command image present=%t stops fallback", backend.name, present), func(t *testing.T) {
				f := newClipboardImageFixture(t, "linux", backend.env)
				f.command = func(name string, args []string) ([]byte, error) {
					if name != backend.command {
						t.Fatalf("command=%q", name)
					}
					if slices.Contains(args, "--list-types") || slices.Contains(args, "TARGETS") {
						if present {
							return []byte("text/plain\nimage/png\n"), nil
						}
						return []byte("text/plain\n"), nil
					}
					return upstreamClipboardPNG, nil
				}
				var want []byte
				if present {
					want = upstreamClipboardPNG
				}
				assertClipboardImage(t, want)
				calls := 1
				if present {
					calls = 2
				}
				if len(f.commands) != calls || f.helperCalls != 0 {
					t.Fatalf("commands=%v helper=%d", f.commands, f.helperCalls)
				}
			})
		}
	}
	for _, bytes := range [][]byte{upstreamClipboardPNG, nil, {}} {
		// .upstream/v0.87.1/packages/coding-agent/test/clipboard-image.test.ts:47.
		t.Run(fmt.Sprintf("native X11 result %v stops fallback", bytes), func(t *testing.T) {
			f := newClipboardImageFixture(t, "linux", map[string]string{"DISPLAY": ":0"})
			f.getImage = func() ([]byte, bool, error) { return bytes, true, nil }
			var want []byte
			if len(bytes) > 0 {
				want = bytes
			}
			assertClipboardImage(t, want)
			if f.helperCalls != 1 || f.imageCalls != 1 || !slices.Equal(f.commands, []string{"xclip", "xclip", "xclip", "xclip", "xclip"}) {
				t.Fatalf("calls=%v helper=%d image=%d", f.commands, f.helperCalls, f.imageCalls)
			}
		})
	}
	for _, failure := range []string{"missing module", "unavailable display"} {
		// .upstream/v0.87.1/packages/coding-agent/test/clipboard-image.test.ts:56.
		t.Run("Wayland: falls back to X11 after "+failure, func(t *testing.T) {
			f := newClipboardImageFixture(t, "linux", map[string]string{"WAYLAND_DISPLAY": "1"})
			if failure == "missing module" {
				f.missing = true
			} else {
				f.getImage = func() ([]byte, bool, error) { return nil, false, nil }
			}
			f.command = func(name string, args []string) ([]byte, error) {
				if name == "wl-paste" {
					return nil, errors.New("failed")
				}
				if slices.Contains(args, "TARGETS") {
					return []byte("image/png\n"), nil
				}
				return upstreamClipboardPNG, nil
			}
			assertClipboardImage(t, upstreamClipboardPNG)
			if f.helperCalls != 0 {
				t.Fatal("native helper preceded Xclip")
			}
		})
	}
	// .upstream/v0.87.1/packages/coding-agent/test/clipboard-image.test.ts:70.
	t.Run("WSL: tries PowerShell before a broken native X11 bridge", func(t *testing.T) {
		f := newClipboardImageFixture(t, "linux", map[string]string{"WSL_DISTRO_NAME": "Ubuntu"})
		f.getImage = func() ([]byte, bool, error) { return nil, false, errors.New("Broken X11 bridge") }
		var temporary string
		f.command = func(name string, args []string) ([]byte, error) {
			switch name {
			case "wl-paste", "xclip":
				return nil, errors.New("unavailable")
			case "wslpath":
				temporary = args[1]
				return []byte("C:\\Users\\O'Hare\\clip.png\n"), nil
			case "powershell.exe":
				// The Go runner has no environment-override argument (the counterpart of options.env being undefined). The path must be embedded in the quoted script, not read from a PI_* variable.
				if strings.Contains(args[2], "PI_WSL_CLIPBOARD_IMAGE_PATH") {
					t.Fatal("clipboard path passed through environment")
				}
				if !strings.Contains(args[2], "$path = 'C:\\Users\\O''Hare\\clip.png'") {
					t.Fatal(args)
				}
				if temporary == "" {
					t.Fatal("wslpath must precede PowerShell")
				}
				if err := os.WriteFile(temporary, upstreamClipboardPNG, 0o600); err != nil {
					t.Fatal(err)
				}
				return []byte("ok\n"), nil
			default:
				t.Fatalf("unexpected command %s", name)
				return nil, nil
			}
		}
		assertClipboardImage(t, upstreamClipboardPNG)
		if f.helperCalls != 0 {
			t.Fatal("native helper preceded WSL fallback")
		}
		if _, err := os.Stat(temporary); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("WSL temporary image retained: %v", err)
		}
	})
	for _, platform := range []string{"darwin", "win32"} {
		for _, value := range []struct {
			name      string
			bytes     []byte
			available bool
		}{{"PNG", upstreamClipboardPNG, true}, {"null", nil, true}, {"empty", []byte{}, true}, {"undefined", nil, false}} {
			// .upstream/v0.87.1/packages/coding-agent/test/clipboard-image.test.ts:97.
			t.Run(platform+": reads native image "+value.name+" once", func(t *testing.T) {
				f := newClipboardImageFixture(t, platform, map[string]string{})
				f.getImage = func() ([]byte, bool, error) { return value.bytes, value.available, nil }
				var want []byte
				if len(value.bytes) > 0 {
					want = value.bytes
				}
				assertClipboardImage(t, want)
				if f.imageCalls != 1 || len(f.commands) != 0 {
					t.Fatalf("image=%d commands=%v", f.imageCalls, f.commands)
				}
			})
		}
	}
	// .upstream/v0.87.1/packages/coding-agent/test/clipboard-image.test.ts:107.
	t.Run("returns null without a native helper", func(t *testing.T) {
		f := newClipboardImageFixture(t, "win32", map[string]string{})
		f.missing = true
		assertClipboardImage(t, nil)
		if f.imageCalls != 0 {
			t.Fatal("missing helper read")
		}
	})
	for _, platform := range []string{"linux", "win32"} {
		// .upstream/v0.87.1/packages/coding-agent/test/clipboard-image.test.ts:113.
		t.Run(platform+": propagates native transfer errors without fallback", func(t *testing.T) {
			f := newClipboardImageFixture(t, platform, map[string]string{"WAYLAND_DISPLAY": "1", "DISPLAY": ":0"})
			failure := errors.New("Native clipboard operation failed")
			f.getImage = func() ([]byte, bool, error) { return nil, true, failure }
			_, _, err := ReadClipboardImageContext(t.Context())
			if err != failure {
				t.Fatalf("error=%v, want original failure", err)
			}
			var want []string
			if platform == "linux" {
				want = []string{"wl-paste", "xclip", "xclip", "xclip", "xclip", "xclip"}
			}
			if !slices.Equal(f.commands, want) {
				t.Fatalf("commands=%v", f.commands)
			}
		})
	}
	// .upstream/v0.87.1/packages/coding-agent/test/clipboard-image.test.ts:129.
	t.Run("Termux does not read image clipboards", func(t *testing.T) {
		f := newClipboardImageFixture(t, "linux", map[string]string{"TERMUX_VERSION": "0.119"})
		assertClipboardImage(t, nil)
		if f.helperCalls != 0 || len(f.commands) != 0 {
			t.Fatalf("helper=%d commands=%v", f.helperCalls, f.commands)
		}
	})
}

func TestClipboardImageTermuxSkipsConfiguredBackends(t *testing.T) {
	f := newClipboardImageFixture(t, "linux", map[string]string{"TERMUX_VERSION": "0.119", "WAYLAND_DISPLAY": "1", "DISPLAY": ":0"})
	assertClipboardImage(t, nil)
	if f.helperCalls != 0 || len(f.commands) != 0 {
		t.Fatalf("helper=%d commands=%v", f.helperCalls, f.commands)
	}
}

func TestClipboardImageNativeTransferOwnsCallerLifetime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newClipboardImageFixture(t, "darwin", map[string]string{})
		parent, cancel := context.WithCancelCause(t.Context())
		defer cancel(nil)
		started := make(chan struct{})
		f.getImage = func() ([]byte, bool, error) {
			close(started)
			<-f.imageContext.Done()
			return nil, true, context.Cause(f.imageContext)
		}
		done := make(chan error, 1)
		go func() {
			_, _, err := ReadClipboardImageContext(parent)
			done <- err
		}()
		<-started
		synctest.Wait()
		select {
		case err := <-done:
			t.Fatalf("read returned before native transfer settled: %v", err)
		default:
		}
		failure := errors.New("clipboard caller stopped")
		cancel(failure)
		synctest.Wait()
		if err := <-done; err != failure {
			t.Fatalf("native read error=%v, want original caller cause", err)
		}
		if len(f.commands) != 0 || f.imageCalls != 1 {
			t.Fatalf("image calls=%d commands=%v", f.imageCalls, f.commands)
		}
	})
}
