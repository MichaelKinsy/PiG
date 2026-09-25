package codingagent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// scriptedClipboard answers clipboard commands by their full command line and
// records every call, so a test asserts both the result and which backends ran.
type scriptedClipboard struct {
	responses map[string]fakeCall
	log       []string
}

func (s *scriptedClipboard) run(_ context.Context, name string, args ...string) ([]byte, error) {
	line := strings.Join(append([]string{name}, args...), " ")
	s.log = append(s.log, line)
	if r, ok := s.responses[line]; ok {
		return r.out, r.err
	}
	return nil, errors.New("scriptedClipboard: command failed: " + line)
}

func withScriptedClipboard(t *testing.T, responses map[string]fakeCall) *scriptedClipboard {
	t.Helper()
	s := &scriptedClipboard{responses: responses}
	prev := clipboardRun
	clipboardRun = s.run
	t.Cleanup(func() { clipboardRun = prev })
	return s
}

func (s *scriptedClipboard) ran(prefix string) bool {
	return slices.ContainsFunc(s.log, func(line string) bool { return strings.HasPrefix(line, prefix) })
}

var linuxClipboardPNG = []byte("\x89PNG\r\n\x1a\nbody")

// Upstream readClipboardImageViaWlPaste returns null when the listing has no
// image type, and readClipboardImage only falls through to xclip on undefined:
// an empty Wayland clipboard must not surface stale X11 clipboard contents.
func TestReadClipboardImageEmptyWaylandDoesNotFallThroughToXclip(t *testing.T) {
	withEnv(t, map[string]string{"WAYLAND_DISPLAY": "wayland-0", "DISPLAY": ":0"})
	s := withScriptedClipboard(t, map[string]fakeCall{
		"wl-paste --list-types":                      {out: []byte("text/plain\nUTF8_STRING\n")},
		"xclip -selection clipboard -t TARGETS -o":   {out: []byte("TARGETS\nimage/png\n")},
		"xclip -selection clipboard -t image/png -o": {out: linuxClipboardPNG},
	})
	data, mime, err := readClipboardImageLinux()
	if err != nil || data != nil || mime != "" {
		t.Fatalf("read = %q, %q, %v; want no image", data, mime, err)
	}
	if s.ran("xclip") {
		t.Fatalf("empty Wayland clipboard fell through to xclip: %v", s.log)
	}
}

// Upstream returns null when wl-paste reads zero bytes for the listed type.
func TestReadClipboardImageEmptyWaylandDataDoesNotFallThroughToXclip(t *testing.T) {
	withEnv(t, map[string]string{"WAYLAND_DISPLAY": "wayland-0", "DISPLAY": ":0"})
	s := withScriptedClipboard(t, map[string]fakeCall{
		"wl-paste --list-types":                      {out: []byte("image/png\n")},
		"wl-paste --type image/png --no-newline":     {out: []byte{}},
		"xclip -selection clipboard -t TARGETS -o":   {out: []byte("image/png\n")},
		"xclip -selection clipboard -t image/png -o": {out: linuxClipboardPNG},
	})
	if data, _, _ := readClipboardImageLinux(); data != nil {
		t.Fatalf("read = %q; want no image", data)
	}
	if s.ran("xclip") {
		t.Fatalf("empty wl-paste read fell through to xclip: %v", s.log)
	}
}

// A failed wl-paste (undefined upstream) still falls through to xclip.
func TestReadClipboardImageFailedWaylandFallsThroughToXclip(t *testing.T) {
	withEnv(t, map[string]string{"WAYLAND_DISPLAY": "wayland-0"})
	withScriptedClipboard(t, map[string]fakeCall{
		"xclip -selection clipboard -t TARGETS -o":   {out: []byte("image/png\n")},
		"xclip -selection clipboard -t image/png -o": {out: linuxClipboardPNG},
	})
	data, mime, err := readClipboardImageLinux()
	if err != nil || mime != "image/png" || string(data) != string(linuxClipboardPNG) {
		t.Fatalf("read = %q, %q, %v; want xclip PNG", data, mime, err)
	}
}

// Upstream readClipboardImageViaXclip returns null when TARGETS succeeds but
// names no image type, instead of probing each supported type.
func TestReadClipboardImageXclipTargetsWithoutImageIsEmpty(t *testing.T) {
	withEnv(t, map[string]string{"DISPLAY": ":0"})
	s := withScriptedClipboard(t, map[string]fakeCall{
		"xclip -selection clipboard -t TARGETS -o":   {out: []byte("TARGETS\nUTF8_STRING\n")},
		"xclip -selection clipboard -t image/png -o": {out: linuxClipboardPNG},
	})
	if data, _, _ := readClipboardImageLinux(); data != nil {
		t.Fatalf("read = %q; want no image", data)
	}
	if want := []string{"xclip -selection clipboard -t TARGETS -o"}; !slices.Equal(s.log, want) {
		t.Fatalf("calls = %v, want %v", s.log, want)
	}
}

// When TARGETS fails, upstream probes every supported type in preference
// order, after the preferred raw type, deduplicated by exact string.
func TestReadClipboardImageXclipTargetsFailureProbesSupportedTypes(t *testing.T) {
	withEnv(t, map[string]string{"DISPLAY": ":0"})
	s := withScriptedClipboard(t, map[string]fakeCall{
		"xclip -selection clipboard -t image/gif -o": {out: []byte("GIF89a")},
	})
	data, mime, _ := readClipboardImageLinux()
	if mime != "image/gif" || string(data) != "GIF89a" {
		t.Fatalf("read = %q, %q; want gif", data, mime)
	}
	want := []string{
		"xclip -selection clipboard -t TARGETS -o",
		"xclip -selection clipboard -t image/png -o",
		"xclip -selection clipboard -t image/jpeg -o",
		"xclip -selection clipboard -t image/webp -o",
		"xclip -selection clipboard -t image/gif -o",
	}
	if !slices.Equal(s.log, want) {
		t.Fatalf("calls = %v, want %v", s.log, want)
	}
}

// windowsClipboard fakes wslpath and powershell.exe for the WSL fallback. The
// PowerShell call writes image to the Linux path that wslpath translated and
// prints output, as upstream readClipboardImageViaPowerShell expects.
type windowsClipboard struct {
	scriptedClipboard
	winPath string
	image   []byte
	output  string
	tmpFile string
	script  string
}

func (w *windowsClipboard) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	switch name {
	case "wslpath":
		w.log = append(w.log, "wslpath "+strings.Join(args, " "))
		w.tmpFile = args[len(args)-1]
		return []byte(w.winPath + "\n"), nil
	case "powershell.exe":
		w.log = append(w.log, "powershell.exe")
		w.script = args[len(args)-1]
		if w.image != nil {
			if err := os.WriteFile(w.tmpFile, w.image, 0o600); err != nil {
				return nil, err
			}
		}
		return []byte(w.output + "\r\n"), nil
	}
	return w.scriptedClipboard.run(ctx, name, args...)
}

func withWindowsClipboard(t *testing.T, w *windowsClipboard) *windowsClipboard {
	t.Helper()
	t.Setenv("TMPDIR", t.TempDir())
	prev := clipboardRun
	clipboardRun = w.run
	t.Cleanup(func() { clipboardRun = prev })
	return w
}

// Upstream readClipboardImage tries wl-paste on WSL even without a Wayland
// session variable.
func TestReadClipboardImageWSLTriesWlPasteWithoutWaylandDisplay(t *testing.T) {
	withEnv(t, map[string]string{"WSL_DISTRO_NAME": "Ubuntu"})
	s := withScriptedClipboard(t, map[string]fakeCall{
		"wl-paste --list-types":                  {out: []byte("image/png\n")},
		"wl-paste --type image/png --no-newline": {out: linuxClipboardPNG},
	})
	data, mime, err := readClipboardImageLinux()
	if err != nil || mime != "image/png" || string(data) != string(linuxClipboardPNG) {
		t.Fatalf("read = %q, %q, %v; want wl-paste PNG (calls %v)", data, mime, err, s.log)
	}
}

// On WSL a Windows screenshot (Win+Shift+S) reaches only the Windows
// clipboard. Upstream readClipboardImageViaPowerShell saves it as PNG through
// the wslpath-translated temp file when the Linux clipboard has no image.
func TestReadClipboardImageWSLFallsBackToWindowsClipboard(t *testing.T) {
	withEnv(t, map[string]string{"WSL_DISTRO_NAME": "Ubuntu", "WAYLAND_DISPLAY": "wayland-0", "DISPLAY": ":0"})
	w := withWindowsClipboard(t, &windowsClipboard{
		scriptedClipboard: scriptedClipboard{responses: map[string]fakeCall{
			"wl-paste --list-types": {out: []byte("text/plain\n")},
		}},
		winPath: `C:\Users\o'brien\AppData\Local\Temp\clip.png`,
		image:   linuxClipboardPNG,
		output:  "ok",
	})
	data, mime, err := readClipboardImageLinux()
	if err != nil || mime != "image/png" || string(data) != string(linuxClipboardPNG) {
		t.Fatalf("read = %q, %q, %v; want Windows clipboard PNG (calls %v)", data, mime, err, w.log)
	}
	if w.ran("xclip") {
		t.Fatalf("empty Wayland clipboard fell through to xclip: %v", w.log)
	}
	if dir, base := filepath.Split(w.tmpFile); filepath.Clean(dir) != os.TempDir() || !strings.HasPrefix(base, "pi-wsl-clip-") || !strings.HasSuffix(base, ".png") {
		t.Fatalf("temp file = %q, want %s/pi-wsl-clip-*.png", w.tmpFile, os.TempDir())
	}
	wantScript := "Add-Type -AssemblyName System.Windows.Forms; Add-Type -AssemblyName System.Drawing; " +
		`$path = 'C:\Users\o''brien\AppData\Local\Temp\clip.png'; ` +
		"$img = [System.Windows.Forms.Clipboard]::GetImage(); " +
		"if ($img) { $img.Save($path, [System.Drawing.Imaging.ImageFormat]::Png); Write-Output 'ok' } else { Write-Output 'empty' }"
	if w.script != wantScript {
		t.Fatalf("PowerShell script:\n got %s\nwant %s", w.script, wantScript)
	}
	if _, err := os.Stat(w.tmpFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temp file %s was not removed: %v", w.tmpFile, err)
	}
}

// Without WSL environment variables, upstream isWSL reads /proc/version. A
// headless WSL session (no WSLg) still reaches the Windows clipboard.
func TestReadClipboardImageWSLDetectedFromProcVersion(t *testing.T) {
	withEnv(t, map[string]string{})
	clipboardReadFile = func(name string) ([]byte, error) {
		if name != "/proc/version" {
			return nil, os.ErrNotExist
		}
		return []byte("Linux version 6.6.87.2-microsoft-standard-WSL2"), nil
	}
	w := withWindowsClipboard(t, &windowsClipboard{winPath: `C:\tmp\clip.png`, image: linuxClipboardPNG, output: "ok"})
	data, mime, _ := readClipboardImageLinux()
	if mime != "image/png" || string(data) != string(linuxClipboardPNG) {
		t.Fatalf("read = %q, %q; want Windows clipboard PNG (calls %v)", data, mime, w.log)
	}
}

// PowerShell prints "empty" when the Windows clipboard holds no image.
func TestReadClipboardImageWSLWindowsClipboardWithoutImage(t *testing.T) {
	withEnv(t, map[string]string{"WSL_DISTRO_NAME": "Ubuntu"})
	w := withWindowsClipboard(t, &windowsClipboard{winPath: `C:\tmp\clip.png`, output: "empty"})
	if data, _, err := readClipboardImageLinux(); err != nil || data != nil {
		t.Fatalf("read = %q, %v; want no image", data, err)
	}
	if !w.ran("powershell.exe") {
		t.Fatalf("PowerShell fallback did not run: %v", w.log)
	}
	if _, err := os.Stat(w.tmpFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temp file %s was not removed: %v", w.tmpFile, err)
	}
}

// Outside WSL there is no Windows clipboard to fall back to.
func TestReadClipboardImageNonWSLSkipsWindowsClipboard(t *testing.T) {
	withEnv(t, map[string]string{"WAYLAND_DISPLAY": "wayland-0"})
	w := withWindowsClipboard(t, &windowsClipboard{winPath: `C:\tmp\clip.png`, image: linuxClipboardPNG, output: "ok"})
	if data, _, _ := readClipboardImageLinux(); data != nil {
		t.Fatalf("read = %q; want no image", data)
	}
	if w.ran("wslpath") || w.ran("powershell.exe") {
		t.Fatalf("non-WSL read used the Windows clipboard: %v", w.log)
	}
}

// The preferred raw type keeps its advertised spelling and upstream's Set
// deduplicates only exact strings, so "image/PNG" is followed by "image/png".
func TestReadClipboardImageXclipPreferredRawTypeThenSupportedTypes(t *testing.T) {
	withEnv(t, map[string]string{"DISPLAY": ":0"})
	s := withScriptedClipboard(t, map[string]fakeCall{
		"xclip -selection clipboard -t TARGETS -o":   {out: []byte("image/PNG\n")},
		"xclip -selection clipboard -t image/png -o": {out: linuxClipboardPNG},
	})
	data, mime, _ := readClipboardImageLinux()
	if mime != "image/png" || string(data) != string(linuxClipboardPNG) {
		t.Fatalf("read = %q, %q; want png", data, mime)
	}
	want := []string{
		"xclip -selection clipboard -t TARGETS -o",
		"xclip -selection clipboard -t image/PNG -o",
		"xclip -selection clipboard -t image/png -o",
	}
	if !slices.Equal(s.log, want) {
		t.Fatalf("calls = %v, want %v", s.log, want)
	}
}
