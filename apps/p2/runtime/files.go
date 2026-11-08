package runtime

import (
	"regexp"
	"strings"
	"unicode/utf16"

	"golang.org/x/text/unicode/norm"
)

// Ports apps/p2/src/runtime/files.ts: file names and previews every process shares.

var unsafeNameRun = regexp.MustCompile(`[^\p{L}\p{N}._-]+`)

// SafeFileName returns a display filename, never a path or a raw HTTP header value: the last path component, NFKC,
// runs of other characters replaced by `_`, leading dots dropped, at most the last 120 UTF-16 units; "file" if nothing
// is left.
func SafeFileName(name string) string {
	parts := splitKeepingEmpty(name)
	base := parts[len(parts)-1]
	safe := unsafeNameRun.ReplaceAllString(norm.NFKC.String(base), "_")
	safe = strings.TrimLeft(safe, ".")
	if units := utf16.Encode([]rune(safe)); len(units) > 120 {
		safe = string(utf16.Decode(units[len(units)-120:]))
	}
	if safe == "" {
		return "file"
	}
	return safe
}

// splitKeepingEmpty is `name.split(/[/\\]/)`: the pieces between slashes and backslashes, empty ones kept.
func splitKeepingEmpty(name string) []string {
	return regexp.MustCompile(`[/\\]`).Split(name, -1)
}

// DownloadInfo is a download of the session (`DownloadInfo`).
type DownloadInfo struct {
	ID   string
	Name string
	Size float64
}

// PreviewKind is how a file previews.
type PreviewKind string

// The kinds of preview.
const (
	PreviewText  PreviewKind = "text"
	PreviewImage PreviewKind = "image"
	PreviewAudio PreviewKind = "audio"
	PreviewVideo PreviewKind = "video"
)

// FilePreview is how a file previews: its kind and its served MIME type.
type FilePreview struct {
	Kind     PreviewKind
	MimeType string
}

const plainText = "text/plain; charset=utf-8"

var previewTypes = map[string]FilePreview{}

func init() {
	for _, extension := range strings.Fields("txt md markdown js jsx ts tsx json css html htm xml yaml yml sh py rs go java c h cpp sql log") {
		previewTypes[extension] = FilePreview{PreviewText, plainText}
	}
	for extension, mime := range map[string]string{"png": "image/png", "jpg": "image/jpeg", "jpeg": "image/jpeg", "gif": "image/gif", "webp": "image/webp", "avif": "image/avif"} {
		previewTypes[extension] = FilePreview{PreviewImage, mime}
	}
	for extension, mime := range map[string]string{"mp3": "audio/mpeg", "m4a": "audio/mp4", "aac": "audio/aac", "wav": "audio/wav", "oga": "audio/ogg", "ogg": "audio/ogg", "flac": "audio/flac"} {
		previewTypes[extension] = FilePreview{PreviewAudio, mime}
	}
	for extension, mime := range map[string]string{"mp4": "video/mp4", "m4v": "video/mp4", "webm": "video/webm", "ogv": "video/ogg", "mov": "video/quicktime"} {
		previewTypes[extension] = FilePreview{PreviewVideo, mime}
	}
}

// PreviewOf is upstream's `filePreview`: a conservative extension allowlist; active formats such as SVG never execute.
// The extension is the text after the last dot of the lowercased name, or the whole name when it has no dot, as
// `name.toLowerCase().split(".").pop()` is.
func PreviewOf(name string) (FilePreview, bool) {
	lower := strings.ToLower(name)
	extension := lower[strings.LastIndexByte(lower, '.')+1:]
	if extension == "" {
		return FilePreview{}, false
	}
	preview, ok := previewTypes[extension]
	return preview, ok
}
