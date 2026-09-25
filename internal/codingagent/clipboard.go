// System clipboard reader for image paste.
//
// Mirrors upstream packages/coding-agent/src/utils/clipboard-image.ts
// minus the native clipboard module (pig has no native bindings: pure
// shellouts only).
//
// macOS: osascript with the «class PNGf» query (one round trip; writes
// the PNG bytes to a tempfile because AppleScript's `do shell script
// echo` can't round-trip binary safely).
//
// Linux:
//
//   - Wayland (WAYLAND_DISPLAY or XDG_SESSION_TYPE=wayland) → wl-paste
//     `--list-types` then `--type image/<fmt> --no-newline` for the
//     first preferred MIME type the clipboard advertises.
//   - X11 (DISPLAY) or a failed wl-paste → xclip TARGETS probe then
//     `xclip -selection clipboard -t image/<fmt> -o`.
//   - WSL with no Linux image → the Windows clipboard through PowerShell.
//
// Returns (nil, "", nil) when no image is present (NOT an error). The
// caller (paste handler) silently ignores in that case.

package codingagent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// SupportedImageMIMEs lists the formats we accept from the clipboard.
// Order = preference (matches upstream SUPPORTED_IMAGE_MIME_TYPES).
var SupportedImageMIMEs = []string{
	"image/png",
	"image/jpeg",
	"image/webp",
	"image/gif",
}

// clipboardRunner is the seam unit tests use to inject fake exec results.
// Production callers use the package-level default.
type clipboardRunner func(ctx context.Context, name string, args ...string) ([]byte, error)

func defaultClipboardRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return out, nil
}

// envLookup is the seam tests use to override env-var lookups.
type envLookup func(string) string

// clipboardEnv is the package-level env reader (overridden in tests).
var clipboardEnv envLookup = os.Getenv

// clipboardRun is the package-level command runner (overridden in tests).
var clipboardRun clipboardRunner = defaultClipboardRunner

// ReadClipboardImage reads a PNG/JPEG/WebP/GIF from the system
// clipboard. Returns (nil, "", nil) when the clipboard holds no image
// : this is not an error case, just "nothing to paste".
func ReadClipboardImage() ([]byte, string, error) {
	return ReadClipboardImageContext(context.Background())
}

// ReadClipboardImageContext reads a clipboard image while parent remains
// active. Ctrl+V passes its renderer lifetime so teardown cancels command
// probes and joins the operation instead of leaving terminal I/O behind.
func ReadClipboardImageContext(parent context.Context) ([]byte, string, error) {
	switch clipboardGOOS {
	case "darwin":
		return readClipboardImageMacOSContext(parent)
	case "linux":
		return readClipboardImageLinuxContext(parent)
	default:
		return nil, "", nil
	}
}

// ─── macOS ────────────────────────────────────────────────────────────────────

// readClipboardImageMacOS spawns `osascript` with a small AppleScript
// program that writes the clipboard's PNG bytes to a tempfile (or
// returns "no" if the clipboard doesn't hold image data).
//
// AppleScript can't round-trip binary safely through stdout: the
// tempfile dance is the parity-faithful approach (matches the upstream
// PowerShell-on-WSL pattern, just on macOS).
func readClipboardImageMacOS() ([]byte, string, error) {
	return readClipboardImageMacOSContext(context.Background())
}

func readClipboardImageMacOSContext(parent context.Context) ([]byte, string, error) {
	tmpFile, err := os.CreateTemp("", "pig-clip-*.png")
	if err != nil {
		return nil, "", fmt.Errorf("clipboard: tempfile: %w", err)
	}
	tmpPath := tmpFile.Name()
	_ = tmpFile.Close()
	defer func() { _ = os.Remove(tmpPath) }()

	script := strings.Join([]string{
		"try",
		"  set thePng to (the clipboard as «class PNGf»)",
		"  set fp to open for access POSIX file \"" + tmpPath + "\" with write permission",
		"  set eof fp to 0",
		"  write thePng to fp",
		"  close access fp",
		"  return \"ok\"",
		"on error errMsg",
		"  try",
		"    close access POSIX file \"" + tmpPath + "\"",
		"  end try",
		"  return \"no\"",
		"end try",
	}, "\n")

	// osascript stands in for upstream's native clipboard read and takes
	// runClipboardCommand's default timeout.
	out, err := runClipboardImageCommandContext(parent, clipboardCommandTimeout, "osascript", "-e", script)
	if err != nil {
		// Most osascript failures = "no image" rather than a hard
		// error. Mirror upstream and return (nil, "", nil) silently.
		return nil, "", nil
	}
	if strings.TrimSpace(string(out)) != "ok" {
		return nil, "", nil
	}
	bytes, err := os.ReadFile(tmpPath)
	if err != nil {
		return nil, "", nil
	}
	if len(bytes) == 0 {
		return nil, "", nil
	}
	return bytes, "image/png", nil
}

// ─── Linux ────────────────────────────────────────────────────────────────────

// isWaylandSession mirrors upstream isWaylandSession.
func isWaylandSession() bool {
	if clipboardEnv("WAYLAND_DISPLAY") != "" {
		return true
	}
	return clipboardEnv("XDG_SESSION_TYPE") == "wayland"
}

// clipboardImageResult distinguishes a failed backend from one that reports
// no image, as upstream's undefined and null do: an empty Wayland clipboard
// must not fall through to stale X11 clipboard contents.
type clipboardImageResult int

const (
	clipboardImageFailed clipboardImageResult = iota
	clipboardImageNone
	clipboardImageFound
)

// Upstream clipboard-image.ts timeouts: DEFAULT_LIST_TIMEOUT_MS for type
// listings and wslpath, DEFAULT_POWERSHELL_TIMEOUT_MS for the WSL PowerShell
// read, and runClipboardCommand's default for every other read.
const (
	clipboardListTimeout       = time.Second
	clipboardPowerShellTimeout = 5 * time.Second
	// upstream: packages/coding-agent/src/utils/clipboard-command.ts:runClipboardCommand
	clipboardCommandTimeout = 3 * time.Second
)

// clipboardReadFile reads /proc/version for WSL detection (overridden in
// tests).
var clipboardReadFile = os.ReadFile

func runClipboardImageCommandContext(parent context.Context, timeout time.Duration, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	return clipboardRun(ctx, name, args...)
}

// readClipboardImageLinux mirrors upstream readClipboardImage's linux branch:
// wl-paste under Wayland or WSL, xclip when that backend failed, then the
// Windows clipboard through PowerShell under WSL when Linux had no image.
func readClipboardImageLinux() ([]byte, string, error) {
	return readClipboardImageLinuxContext(context.Background())
}

func readClipboardImageLinuxContext(parent context.Context) ([]byte, string, error) {
	wayland := isWaylandSession()
	hasX11 := clipboardEnv("DISPLAY") != ""
	wsl := IsWSL(clipboardEnv, clipboardReadFile)

	var data []byte
	var mime string
	result := clipboardImageFailed
	if wayland || wsl {
		data, mime, result = tryWlPasteContext(parent)
	}
	if result == clipboardImageFailed && (hasX11 || wayland || wsl) {
		data, mime, result = tryXclipContext(parent)
	}
	if result == clipboardImageFound {
		return data, mime, nil
	}
	if wsl {
		if data, ok := readClipboardImageViaPowerShellContext(parent); ok {
			return data, "image/png", nil
		}
	}
	return nil, "", nil
}

func tryWlPaste() ([]byte, string, clipboardImageResult) {
	return tryWlPasteContext(context.Background())
}

func tryWlPasteContext(parent context.Context) ([]byte, string, clipboardImageResult) {
	listOut, err := runClipboardImageCommandContext(parent, clipboardListTimeout, "wl-paste", "--list-types")
	if err != nil {
		return nil, "", clipboardImageFailed
	}
	preferred := selectPreferredImageMIME(strings.Split(string(listOut), "\n"))
	if preferred == "" {
		return nil, "", clipboardImageNone
	}
	data, err := runClipboardImageCommandContext(parent, clipboardCommandTimeout, "wl-paste", "--type", preferred, "--no-newline")
	if err != nil {
		return nil, "", clipboardImageFailed
	}
	if len(data) == 0 {
		return nil, "", clipboardImageNone
	}
	return data, baseMIME(preferred), clipboardImageFound
}

func tryXclip() ([]byte, string, clipboardImageResult) {
	return tryXclipContext(context.Background())
}

func tryXclipContext(parent context.Context) ([]byte, string, clipboardImageResult) {
	// First probe TARGETS to learn what the clipboard advertises.
	targetsOut, targetsErr := runClipboardImageCommandContext(parent, clipboardListTimeout, "xclip", "-selection", "clipboard", "-t", "TARGETS", "-o")

	var candidates []string
	if targetsErr == nil {
		candidates = strings.Split(string(targetsOut), "\n")
	}
	preferred := selectPreferredImageMIME(candidates)
	if targetsErr == nil && preferred == "" {
		return nil, "", clipboardImageNone
	}

	// The preferred type keeps its advertised spelling; like upstream's Set,
	// only exact duplicates are dropped.
	tryOrder := slices.Clone(SupportedImageMIMEs)
	if preferred != "" {
		tryOrder = slices.DeleteFunc(tryOrder, func(m string) bool { return m == preferred })
		tryOrder = slices.Insert(tryOrder, 0, preferred)
	}

	for _, mime := range tryOrder {
		data, err := runClipboardImageCommandContext(parent, clipboardCommandTimeout, "xclip", "-selection", "clipboard", "-t", mime, "-o")
		if err != nil || len(data) == 0 {
			continue
		}
		return data, baseMIME(mime), clipboardImageFound
	}
	return nil, "", clipboardImageFailed
}

// readClipboardImageViaPowerShell reads the Windows clipboard from WSL, where
// the Linux clipboard does not receive Windows screenshots. Mirrors upstream
// readClipboardImageViaPowerShell.
func readClipboardImageViaPowerShellContext(parent context.Context) ([]byte, bool) {
	suffix := make([]byte, 16)
	_, _ = rand.Read(suffix)
	tmpFile := filepath.Join(os.TempDir(), "pi-wsl-clip-"+hex.EncodeToString(suffix)+".png")
	defer func() { _ = os.Remove(tmpFile) }()

	out, err := runClipboardImageCommandContext(parent, clipboardListTimeout, "wslpath", "-w", tmpFile)
	if err != nil {
		return nil, false
	}
	winPath := strings.TrimSpace(string(out))
	if winPath == "" {
		return nil, false
	}
	script := strings.Join([]string{
		"Add-Type -AssemblyName System.Windows.Forms",
		"Add-Type -AssemblyName System.Drawing",
		"$path = '" + strings.ReplaceAll(winPath, "'", "''") + "'",
		"$img = [System.Windows.Forms.Clipboard]::GetImage()",
		"if ($img) { $img.Save($path, [System.Drawing.Imaging.ImageFormat]::Png); Write-Output 'ok' } else { Write-Output 'empty' }",
	}, "; ")
	out, err = runClipboardImageCommandContext(parent, clipboardPowerShellTimeout, "powershell.exe", "-NoProfile", "-Command", script)
	if err != nil || strings.TrimSpace(string(out)) != "ok" {
		return nil, false
	}
	data, err := os.ReadFile(tmpFile)
	if err != nil || len(data) == 0 {
		return nil, false
	}
	return data, true
}

// selectPreferredImageMIME picks the most-preferred image MIME from a
// list of advertised types. Returns the original (case-preserved) type
// : `wl-paste --type` is case-sensitive on some compositors.
func selectPreferredImageMIME(types []string) string {
	type entry struct{ raw, base string }
	var es []entry
	for _, t := range types {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		es = append(es, entry{raw: t, base: baseMIME(t)})
	}
	for _, want := range SupportedImageMIMEs {
		for _, e := range es {
			if e.base == want {
				return e.raw
			}
		}
	}
	// Any image/* as a fallback (mirrors upstream's `anyImage`).
	for _, e := range es {
		if strings.HasPrefix(e.base, "image/") {
			return e.raw
		}
	}
	return ""
}

// baseMIME strips parameters and lowercases ("image/png; charset=x" → "image/png").
func baseMIME(mime string) string {
	if i := strings.Index(mime, ";"); i >= 0 {
		mime = mime[:i]
	}
	return strings.ToLower(strings.TrimSpace(mime))
}

// ExtensionForImageMIME returns the canonical file extension for a
// supported image MIME, or "" if unknown. Matches upstream's
// extensionForImageMimeType.
func ExtensionForImageMIME(mime string) string {
	switch baseMIME(mime) {
	case "image/png":
		return "png"
	case "image/jpeg":
		return "jpg"
	case "image/webp":
		return "webp"
	case "image/gif":
		return "gif"
	default:
		return ""
	}
}

// SaveClipboardImageToTempFile writes the bytes to
// $TMPDIR/pig-clipboard-<nanos>.<ext> and returns the absolute path.
// The caller is responsible for cleanup; for a paste-into-editor flow
// we leave the file around so the model can read it back.
func SaveClipboardImageToTempFile(bytes []byte, mime string) (string, error) {
	ext := ExtensionForImageMIME(mime)
	if ext == "" {
		ext = "png"
	}
	name := fmt.Sprintf("pig-clipboard-%d.%s", time.Now().UnixNano(), ext)
	path := filepath.Join(os.TempDir(), name)
	if err := os.WriteFile(path, bytes, 0o600); err != nil {
		return "", err
	}
	return path, nil
}
