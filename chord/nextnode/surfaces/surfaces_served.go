package surfaces

import (
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/nextnode/http"
)

// Ports packages/chord/src/node/surfaces/served.ts. The files the surfaces serve (slice 11 §1, §4): base bundles, plugin
// scripts, stylesheets and assets, each named by its content and so cached by clients forever; their types.

// ServedFile is a file the server serves: its type and bytes.
type ServedFile struct {
	Mime  string
	Bytes []byte
}

// Served is a served file's response, cached by the browser forever (its path names its content); 404 for none (nil).
func Served(found *ServedFile) http.HttpResponse {
	if found == nil {
		return http.HttpResponse{Kind: http.KindError, Status: 404}
	}
	bytes := found.Bytes
	size := int64(len(bytes))
	return http.HttpResponse{
		Kind: http.KindBytes, Mime: found.Mime, Size: &size,
		Read: func(offset int64, length int, _ host.Ctx) ([]byte, error) {
			// `Uint8Array.subarray` clamps: a range past the end is empty.
			start := min(max(offset, 0), size)
			return bytes[start:min(start+int64(length), size)], nil
		},
		// Overrides the baseline's `no-store` for these files only.
		Headers: []http.ResponseHeader{http.HeaderOf("Cache-Control", "private, max-age=31536000, immutable")},
	}
}

// JavaScript is the type of a script.
const JavaScript = "text/javascript; charset=utf-8"

// CSS is the type of a stylesheet.
const CSS = "text/css; charset=utf-8"

// MimeOf is the type of a built asset, by extension.
func MimeOf(path string) string {
	if found, ok := assetTypes[strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))]; ok {
		return found
	}
	return "application/octet-stream"
}

// assetTypes are the types of the assets builds collect, by extension.
var assetTypes = map[string]string{
	"svg": "image/svg+xml", "png": "image/png", "jpg": "image/jpeg", "jpeg": "image/jpeg", "gif": "image/gif", "webp": "image/webp",
	"woff": "font/woff", "woff2": "font/woff2", "ttf": "font/ttf", "wasm": "application/wasm", "json": "application/json",
	"css": CSS, "js": JavaScript,
}
