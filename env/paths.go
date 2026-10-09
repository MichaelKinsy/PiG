package env

import (
	"context"
	"regexp"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/nodepath"
	"github.com/MichaelKinsy/PiG/internal/nodeurl"
)

// remotePaths is Node's path rules of the remote system: path.win32 or path.posix.
type remotePaths struct{ windows bool }

func (p remotePaths) join(parts ...string) string {
	if p.windows {
		return nodepath.Win32Join(parts...)
	}
	return nodepath.PosixJoin(parts...)
}

func (p remotePaths) basename(path string) string {
	if p.windows {
		return nodepath.Win32Basename(path)
	}
	return nodepath.PosixBasename(path)
}

func (p remotePaths) isAbsolute(path string) bool {
	if p.windows {
		return nodepath.Win32IsAbsolute(path)
	}
	return nodepath.PosixIsAbsolute(path)
}

// resolve is path.resolve of the flavor; the client's own working directory is what a relative result falls back to.
func (p remotePaths) resolve(parts ...string) (string, error) {
	if p.windows {
		return nodepath.Win32Resolve(nodepath.Process(), parts...)
	}
	if slices.ContainsFunc(parts, nodepath.PosixIsAbsolute) {
		return nodepath.PosixResolve(nodepath.Env{}, parts...)
	}
	return nodepath.PosixResolve(nodepath.Process(), parts...)
}

// remotePath is Node's path rules of the remote system.
func remotePath(ctx context.Context, connection *Connection) (remotePaths, error) {
	info, err := connection.Info(ctx)
	if err != nil {
		return remotePaths{}, err
	}
	return remotePaths{windows: info.OS == "windows"}, nil
}

var driveRelative = regexp.MustCompile(`^([a-zA-Z]:)([^\\/]|$)`)

// resolvePath is Node's resolvePath on the remote system, with its home directory and working directories.
func (e *RemoteExecutionEnv) resolvePath(ctx context.Context, path string) (string, error) {
	info, err := e.Connection.Info(ctx)
	if err != nil {
		return "", err
	}
	windows := info.OS == "windows"
	paths := remotePaths{windows: windows}
	normalized := path
	switch {
	case normalized == "~":
		normalized = info.Home
	case strings.HasPrefix(normalized, "~/") || (windows && strings.HasPrefix(normalized, `~\`)):
		normalized = paths.join(info.Home, normalized[2:])
	case strings.HasPrefix(normalized, "file://"):
		// Malformed URLs stay ordinary paths, as in Node.
		if converted, convertErr := nodeurl.FileURLToPath(normalized, windows); convertErr == nil {
			normalized = converted
		}
	}
	cwd := e.Cwd()
	if paths.isAbsolute(normalized) {
		return paths.resolve(normalized)
	}
	if windows {
		// A drive-relative path on another drive: Node on Windows resolves it against that drive's working directory
		// (`=D:`), else its own working directory if on that drive, else the drive's root.
		if match := driveRelative.FindStringSubmatch(normalized); match != nil {
			drive := match[1]
			if !strings.EqualFold(drive, jsstring.Slice(cwd, 0, 2)) {
				base, ok := info.DriveCwds[strings.ToUpper(drive)]
				if !ok {
					base = info.Cwd
				}
				if !strings.EqualFold(jsstring.Slice(base, 0, 2), drive) && jsstring.Slice(base, 2, 3) == `\` {
					base = drive + `\`
				}
				return remotePaths{windows: true}.resolve(base, normalized)
			}
		}
	}
	return paths.resolve(cwd, normalized)
}
