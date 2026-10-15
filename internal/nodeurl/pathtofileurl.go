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
			resolved, err = nodepath.Win32Resolve(resolveEnv(path, windows), path)
		} else {
			resolved, err = nodepath.PosixResolve(resolveEnv(path, windows), path)
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

// processEnv is nodepath.Process; a test counts the working-directory reads through it.
var processEnv = nodepath.Process

// resolveEnv is the process state path.resolve(path) reads. It reads process.cwd() only when path is not absolute, so an absolute path (a
// drive-absolute one on Windows; `\x` still takes the drive from the working directory) skips the getcwd syscall.
func resolveEnv(path string, windows bool) nodepath.Env {
	if windows {
		if len(path) > 2 && path[0]|0x20 >= 'a' && path[0]|0x20 <= 'z' && path[1] == ':' && (path[2] == '\\' || path[2] == '/') {
			return nodepath.Env{}
		}
	} else if nodepath.PosixIsAbsolute(path) {
		return nodepath.Env{}
	}
	return processEnv()
}
