package codingagent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSelectPreferredImageMIME(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{"prefers png over jpeg", []string{"image/jpeg", "image/png", "text/plain"}, "image/png"},
		{"falls through to jpeg", []string{"image/jpeg", "text/plain"}, "image/jpeg"},
		{"empty when no image", []string{"text/plain", "text/html"}, ""},
		{"trims whitespace", []string{"  image/png  "}, "image/png"},
		{"any image fallback", []string{"image/heic"}, "image/heic"},
		{"preserves case", []string{"IMAGE/PNG"}, "IMAGE/PNG"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := selectPreferredImageMIME(tc.in); got != tc.want {
				t.Errorf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestExtensionForImageMIME(t *testing.T) {
	cases := map[string]string{
		"image/png":              "png",
		"image/jpeg":             "jpg",
		"image/webp":             "webp",
		"image/gif":              "gif",
		"image/png; charset=foo": "png",
		"IMAGE/JPEG":             "jpg",
		"image/heic":             "",
		"":                       "",
	}
	for in, want := range cases {
		if got := ExtensionForImageMIME(in); got != want {
			t.Errorf("ExtensionForImageMIME(%q) = %q want %q", in, got, want)
		}
	}
}

func TestBaseMIME(t *testing.T) {
	cases := map[string]string{
		"image/png":            "image/png",
		"image/png; charset=x": "image/png",
		"  IMAGE/JPEG  ":       "image/jpeg",
		"":                     "",
	}
	for in, want := range cases {
		if got := baseMIME(in); got != want {
			t.Errorf("baseMIME(%q) = %q want %q", in, got, want)
		}
	}
}

func TestSaveClipboardImageToTempFile(t *testing.T) {
	body := []byte("\x89PNG\r\n\x1a\nfake-payload")
	path, err := SaveClipboardImageToTempFile(body, "image/png")
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	defer func() { _ = os.Remove(path) }()
	if !strings.HasSuffix(path, ".png") {
		t.Errorf("expected .png suffix; got %s", path)
	}
	if !strings.Contains(filepath.Base(path), "pig-clipboard-") {
		t.Errorf("expected pig-clipboard- prefix; got %s", path)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Errorf("file body mismatch")
	}
}

// fakeRunner produces canned outputs for a sequence of (cmd, args)
// invocations and records what was called. The next call returns the
// next entry; running past the end returns an error.
type fakeRunner struct {
	calls []fakeCall
	idx   int
	log   []string
}

type fakeCall struct {
	out []byte
	err error
}

func (r *fakeRunner) run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.log = append(r.log, name+" "+strings.Join(args, " "))
	if r.idx >= len(r.calls) {
		return nil, errors.New("fakeRunner: out of canned responses")
	}
	c := r.calls[r.idx]
	r.idx++
	return c.out, c.err
}

func withEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	prev := clipboardEnv
	clipboardEnv = func(k string) string {
		if v, ok := kv[k]; ok {
			return v
		}
		return ""
	}
	prevRead := clipboardReadFile
	clipboardReadFile = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	t.Cleanup(func() { clipboardEnv, clipboardReadFile = prev, prevRead })
}

func withRunner(t *testing.T, fr *fakeRunner) *fakeRunner {
	t.Helper()
	prev := clipboardRun
	clipboardRun = fr.run
	t.Cleanup(func() { clipboardRun = prev })
	return fr
}

func TestReadClipboardImageLinuxWaylandFirst(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("Linux behavior test")
	}
	withEnv(t, map[string]string{
		"WAYLAND_DISPLAY":  "wayland-0",
		"XDG_SESSION_TYPE": "wayland",
	})
	pngBody := []byte("\x89PNG\r\n\x1a\nbody")
	fr := withRunner(t, &fakeRunner{
		calls: []fakeCall{
			{out: []byte("image/png\nimage/jpeg\n"), err: nil}, // wl-paste --list-types
			{out: pngBody, err: nil},                           // wl-paste --type image/png --no-newline
		},
	})
	// Force the linux code path even on macOS test runs by calling
	// directly:
	data, mime, err := readClipboardImageLinux()
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if mime != "image/png" {
		t.Errorf("mime: %q want image/png", mime)
	}
	if string(data) != string(pngBody) {
		t.Errorf("body mismatch")
	}
	if len(fr.log) < 1 || !strings.HasPrefix(fr.log[0], "wl-paste --list-types") {
		t.Errorf("first call must be `wl-paste --list-types`; got %v", fr.log)
	}
	if len(fr.log) < 2 || !strings.Contains(fr.log[1], "wl-paste --type image/png --no-newline") {
		t.Errorf("second call must be `wl-paste --type image/png --no-newline`; got %v", fr.log)
	}
}

func TestReadClipboardImageLinuxFallsThroughToXclip(t *testing.T) {
	withEnv(t, map[string]string{"DISPLAY": ":0"})
	pngBody := []byte("\x89PNG\r\n\x1a\nbody")
	fr := withRunner(t, &fakeRunner{
		calls: []fakeCall{
			{out: []byte("TARGETS\nimage/png\n"), err: nil}, // xclip TARGETS probe
			{out: pngBody, err: nil},                        // xclip -t image/png -o
		},
	})
	data, mime, err := readClipboardImageLinux()
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if mime != "image/png" || string(data) != string(pngBody) {
		t.Errorf("got mime=%q data=%q", mime, data)
	}
	if len(fr.log) < 1 || !strings.Contains(fr.log[0], "xclip") {
		t.Errorf("expected xclip call first; got %v", fr.log)
	}
}

func TestReadClipboardImageNoImage(t *testing.T) {
	// On Linux with empty clipboard: list-types returns no image lines.
	withEnv(t, map[string]string{"WAYLAND_DISPLAY": "wayland-0"})
	withRunner(t, &fakeRunner{
		calls: []fakeCall{
			{out: []byte("text/plain\nUTF8_STRING\n"), err: nil},
			{out: []byte(""), err: errors.New("xclip not found")},
		},
	})
	data, mime, err := readClipboardImageLinux()
	if err != nil {
		t.Errorf("no-image case must NOT error; got %v", err)
	}
	if data != nil || mime != "" {
		t.Errorf("expected nil/empty; got data=%q mime=%q", data, mime)
	}
}

func TestReadClipboardImageHeadlessLinuxStillProbesXclip(t *testing.T) {
	// Pi clipboard-image.ts tries Xclip after an unavailable Wayland backend even without DISPLAY. When TARGETS fails it probes no image type (.upstream/v0.99.1/packages/coding-agent/src/utils/clipboard-image.ts:177) and falls to the native lookup.
	f := newClipboardImageFixture(t, "linux", map[string]string{})
	f.missing = true
	assertClipboardImage(t, nil)
	if len(f.commands) != 1 || f.helperCalls != 1 {
		t.Fatalf("commands=%v helper=%d; want the TARGETS query, then native lookup", f.commands, f.helperCalls)
	}
}

func TestReadClipboardImageMacOSNative(t *testing.T) {
	f := newClipboardImageFixture(t, "darwin", map[string]string{})
	pngBody := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\x0dIHDRfake-mac-png")
	f.getImage = func() ([]byte, bool, error) { return pngBody, true, nil }
	assertClipboardImage(t, pngBody)
	if f.imageCalls != 1 || len(f.commands) != 0 {
		t.Fatalf("native reads=%d commands=%v", f.imageCalls, f.commands)
	}
}
