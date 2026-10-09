package nodeurl

import (
	"strings"

	"github.com/MichaelKinsy/PiG/internal/nodepath"
)

// PathToFileURL is Node 24.19.0's url.pathToFileURL(path, { windows }).href. It resolves path as path.posix or path.win32 does, keeps a
// trailing separator, and percent-encodes what Node's encodePathChars and
// the WHATWG path state encode: C0 controls, space, `"#%<>?[\]^` + "`{|}~",
// DEL and every non-ASCII byte. A Windows UNC path names the URL's host.
func PathToFileURL(path string, windows bool) string {
	resolved := path
	unc := windows && strings.HasPrefix(path, `\\`)
	if !unc {
		var err error
		if windows {
			resolved, err = nodepath.Win32Resolve(nodepath.Process(), path)
		} else {
			resolved, err = nodepath.PosixResolve(nodepath.Process(), path)
		}
		if err != nil {
			resolved = path
		}
	}
	host := ""
	if windows && strings.HasPrefix(resolved, `\\`) {
		rest := strings.TrimPrefix(resolved, `\\`)
		if after, ok := strings.CutPrefix(resolved, `\\?\UNC\`); ok {
			rest = after
		}
		if server, share, ok := strings.Cut(rest, `\`); ok && server != "" {
			host, resolved = server, `\`+share
			if ascii, err := FileHost(server); err == nil {
				host = ascii
			}
		}
	}
	// path.resolve strips a trailing separator, so it is added back.
	endsInSeparator := strings.HasSuffix(resolved, "/") || windows && strings.HasSuffix(resolved, `\`)
	if last := path[max(len(path)-1, 0):]; (last == "/" || windows && last == `\`) && !endsInSeparator {
		resolved += "/"
	}
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	b.WriteString("file://" + host)
	if windows && host == "" {
		b.WriteByte('/')
	}
	for i := range len(resolved) {
		c := resolved[i]
		switch {
		case windows && c == '\\':
			b.WriteByte('/')
		case c <= 0x20 || c >= 0x7f || strings.IndexByte("\"#%<>?[\\]^`{|}~", c) >= 0:
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}
