package prompt

import (
	"slices"

	"github.com/MichaelKinsy/PiG/env"
	"github.com/MichaelKinsy/PiG/internal/nodepath"
)

// PathRules are Node's path rules of the system a session works on: this machine's `path`, or path.posix / path.win32
// for a remote environment (`remotePaths` of apps/p2/src/runtime/environments.ts).
type PathRules struct {
	remote  bool
	windows bool
}

// LocalPaths are this machine's path rules.
var LocalPaths = PathRules{}

// RemotePaths are the rules of a remote system: path.win32 when it is Windows, else path.posix.
func RemotePaths(info env.RemoteInfo) PathRules {
	return PathRules{remote: true, windows: env.NodePlatform(info).Platform == "win32"}
}

// Join is path.join.
func (p PathRules) Join(parts ...string) string {
	switch {
	case !p.remote:
		return nodepath.Join(parts...)
	case p.windows:
		return nodepath.Win32Join(parts...)
	}
	return nodepath.PosixJoin(parts...)
}

// Dirname is path.dirname.
func (p PathRules) Dirname(path string) string {
	switch {
	case !p.remote:
		return nodepath.Dirname(path)
	case p.windows:
		return nodepath.Win32Dirname(path)
	}
	return nodepath.PosixDirname(path)
}

// Basename is path.basename.
func (p PathRules) Basename(path string) string {
	switch {
	case !p.remote:
		return nodepath.Basename(path)
	case p.windows:
		return nodepath.Win32Basename(path)
	}
	return nodepath.PosixBasename(path)
}

// Resolve is path.resolve; this process's working directory is what a relative result falls back to.
func (p PathRules) Resolve(parts ...string) (string, error) {
	switch {
	case !p.remote:
		return nodepath.Resolve(parts...)
	case p.windows:
		return nodepath.Win32Resolve(nodepath.Process(), parts...)
	case slices.ContainsFunc(parts, nodepath.PosixIsAbsolute):
		return nodepath.PosixResolve(nodepath.Env{}, parts...)
	}
	return nodepath.PosixResolve(nodepath.Process(), parts...)
}
