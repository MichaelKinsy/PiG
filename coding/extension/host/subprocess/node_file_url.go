package subprocess

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/nodeurl"
)

// nodeFileURL returns the href Node's url.pathToFileURL returns for path on
// this platform. Node resolves an --import value as a module specifier, so a
// bare Windows path such as C:\x\loader.mjs fails there with
// ERR_UNSUPPORTED_ESM_URL_SCHEME.
func nodeFileURL(path string) (string, error) {
	windows := runtime.GOOS == "windows"
	resolved, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	// Node's path.resolve drops a trailing separator; pathToFileURL restores it.
	if strings.HasSuffix(path, "/") || (windows && strings.HasSuffix(path, `\`)) {
		if !strings.HasSuffix(resolved, string(filepath.Separator)) {
			resolved += "/"
		}
	}
	return fileURLFromAbs(resolved, windows)
}

// fileURLFromAbs formats an absolute path as Node's pathToFileURL does after
// resolution. On Windows a UNC share becomes the URL host, an extended-length
// drive prefix is dropped, and backslashes become slashes.
func fileURLFromAbs(resolved string, windows bool) (string, error) {
	host := ""
	urlPath := resolved
	if windows {
		switch {
		case strings.HasPrefix(resolved, `\\?\UNC\`):
			var err error
			if host, urlPath, err = splitUNC(resolved, len(`\\?\UNC\`)); err != nil {
				return "", err
			}
		case strings.HasPrefix(resolved, `\\?\`) && isDriveLetterPath(resolved[len(`\\?\`):]):
			urlPath = resolved[len(`\\?\`):]
		case strings.HasPrefix(resolved, `\\?\`), strings.HasPrefix(resolved, `\\.\`):
			return "", fmt.Errorf("file URL for device path %q is not supported", resolved)
		case strings.HasPrefix(resolved, `\\`):
			var err error
			if host, urlPath, err = splitUNC(resolved, len(`\\`)); err != nil {
				return "", err
			}
		}
		urlPath = strings.ReplaceAll(urlPath, `\`, "/")
		if !strings.HasPrefix(urlPath, "/") {
			urlPath = "/" + urlPath
		}
	}
	return "file://" + host + encodeFileURLPath(urlPath), nil
}

// splitUNC returns the URL host and the \share\resource path of a UNC path
// whose server starts at offset, rejecting the forms Node rejects. The host is
// the server name as the WHATWG host parser gives it: IDNA-encoded and
// lowercase, localhost empty.
func splitUNC(resolved string, offset int) (host, rest string, err error) {
	end := strings.IndexByte(resolved[offset:], '\\')
	if end < 0 {
		return "", "", fmt.Errorf("invalid UNC path %q: missing UNC resource path", resolved)
	}
	if end == 0 {
		return "", "", fmt.Errorf("invalid UNC path %q: empty UNC servername", resolved)
	}
	host, err = fileURLHost(resolved[offset : offset+end])
	if err != nil {
		return "", "", err
	}
	return host, resolved[offset+end:], nil
}

func isDriveLetterPath(p string) bool {
	return len(p) >= 2 && p[1] == ':' && ('A' <= p[0] && p[0] <= 'Z' || 'a' <= p[0] && p[0] <= 'z')
}

// encodeFileURLPath percent-encodes the bytes Node 24's pathToFileURL encodes
// in a path: C0 controls, space, DEL, non-ASCII UTF-8 bytes, and
// "#%<>?[\]^`{|}~.
func encodeFileURLPath(p string) string {
	const upperhex = "0123456789ABCDEF"
	var b strings.Builder
	b.Grow(len(p))
	for i := range len(p) {
		c := p[i]
		if c <= ' ' || c >= 0x7f || strings.IndexByte("\"#%<>?[\\]^`{|}~", c) >= 0 {
			b.WriteByte('%')
			b.WriteByte(upperhex[c>>4])
			b.WriteByte(upperhex[c&0x0f])
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// fileURLHost returns the host Node's pathToFileURL gives a UNC server name:
// the WHATWG host parser's result for a file URL, where localhost is empty.
// Node assigns the name through URL's hostname setter, which stops at the
// first # or ?, so the rest of the name is dropped.
func fileURLHost(server string) (string, error) {
	if end := strings.IndexAny(server, "#?"); end >= 0 {
		server = server[:end]
	}
	return nodeurl.FileHost(server)
}
