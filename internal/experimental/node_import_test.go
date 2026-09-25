package experimental

import (
	"net/url"
	"path/filepath"
	"strings"
)

// upstreamLoaderImport returns the `node --import` specifier for the upstream
// loader. --import takes a module URL, not a path: on Windows an absolute path
// parses as a URL with the scheme "c:".
func upstreamLoaderImport(root string) string {
	path := filepath.ToSlash(filepath.Join(root, "internal", "experimental", "testdata", "upstream-loader.mjs"))
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}
