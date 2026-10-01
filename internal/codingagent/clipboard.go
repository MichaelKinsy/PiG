// Ports packages/coding-agent/src/utils/clipboard-image.ts.

package codingagent

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/internal/imageprocessing"
	"github.com/MichaelKinsy/PiG/tui"
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
	return runClipboardCommandContext(ctx, name, args, clipboardCommandOptions{})
}

// envLookup is the seam tests use to override env-var lookups.
type envLookup func(string) string

// clipboardEnv is the package-level env reader (overridden in tests).
var clipboardEnv envLookup = os.Getenv

// clipboardRun is the package-level command runner (overridden in tests).
var clipboardRun clipboardRunner = defaultClipboardRunner

var getNativeClipboard = tui.GetNativeClipboard

// ReadClipboardImage reads a PNG/JPEG/WebP/GIF from the system
// clipboard. Returns (nil, "", nil) when the clipboard holds no image
// : this is not an error case, just "nothing to paste".
func ReadClipboardImage() ([]byte, string, error) {
	return ReadClipboardImageContext(context.Background())
}

// ReadClipboardImageContext reads command backends before the native helper on Linux and the native helper directly elsewhere. Termux reads no image clipboard. Native transfer errors propagate unchanged, and unsupported formats are converted to PNG. The caller owns cancellation and awaits the transfer.
func ReadClipboardImageContext(parent context.Context) ([]byte, string, error) {
	if clipboardEnv("TERMUX_VERSION") != "" {
		return nil, "", nil
	}
	var data []byte
	var mime string
	var err error
	if clipboardGOOS == "linux" {
		data, mime, err = readClipboardImageLinuxContext(parent)
	} else {
		data, mime, err = readClipboardImageViaNativeClipboard(parent)
	}
	if err != nil || len(data) == 0 {
		return nil, "", err
	}
	if !slices.Contains(SupportedImageMIMEs, baseMIME(mime)) {
		// Clipboard conversion decodes raw pixels without the terminal image converter's EXIF orientation step.
		// upstream: packages/coding-agent/src/utils/clipboard-image.ts:convertToPng
		decoded, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			return nil, "", nil
		}
		var out bytes.Buffer
		if err := png.Encode(&out, decoded); err != nil {
			return nil, "", nil
		}
		data, mime = out.Bytes(), "image/png"
	}
	return data, mime, nil
}

func readClipboardImageViaNativeClipboard(ctx context.Context) ([]byte, string, error) {
	helper := getNativeClipboard()
	if helper == nil || helper.GetImage == nil {
		return nil, "", nil
	}
	data, _, err := helper.GetImage(ctx)
	if err != nil || len(data) == 0 {
		return nil, "", err
	}
	mime := imageprocessing.DetectSupportedImageMimeType(data)
	if mime == "" {
		mime = "application/octet-stream"
	}
	return data, mime, nil
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

// readClipboardImageLinux tries Wayland when selected, Xclip when the command backend is unavailable, PowerShell under WSL when Linux has no image, then the native helper only if the Linux backend remains unavailable.
func readClipboardImageLinux() ([]byte, string, error) {
	return readClipboardImageLinuxContext(context.Background())
}

func readClipboardImageLinuxContext(parent context.Context) ([]byte, string, error) {
	wayland := isWaylandSession()
	wsl := IsWSL(clipboardEnv, clipboardReadFile)

	var data []byte
	var mime string
	result := clipboardImageFailed
	if wayland || wsl {
		data, mime, result = tryWlPasteContext(parent)
	}
	if result == clipboardImageFailed {
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
	if result == clipboardImageFailed {
		return readClipboardImageViaNativeClipboard(parent)
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
	// Probe TARGETS to learn what the clipboard advertises; without it no image type is probed (upstream clipboard-image.ts readClipboardImageViaXclip).
	targetsOut, err := runClipboardImageCommandContext(parent, clipboardListTimeout, "xclip", "-selection", "clipboard", "-t", "TARGETS", "-o")
	if err != nil {
		return nil, "", clipboardImageFailed
	}
	// The preferred type keeps its advertised spelling.
	preferred := selectPreferredImageMIME(strings.Split(string(targetsOut), "\n"))
	if preferred == "" {
		return nil, "", clipboardImageNone
	}

	data, err := runClipboardImageCommandContext(parent, clipboardCommandTimeout, "xclip", "-selection", "clipboard", "-t", preferred, "-o")
	if err != nil {
		return nil, "", clipboardImageFailed
	}
	if len(data) == 0 {
		return nil, "", clipboardImageNone
	}
	return data, baseMIME(preferred), clipboardImageFound
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
