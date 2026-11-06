// Package noderealpath is Node's fs.realpathSync: the path Node's JS realpath walk reports, which Go's filepath.EvalSymlinks differs from. Node resolves the path before it walks it, so a ".." step cancels a component that need not exist, where EvalSymlinks looks the component up first and fails. On Windows EvalSymlinks also expands 8.3 short names and the case of non-link components and follows a rooted link target on the process drive.
package noderealpath

import "github.com/MichaelKinsy/PiG/internal/nodepath"

// Sync is fs.realpathSync(path): path.resolve makes the path absolute and collapses its dot segments, then every link is followed. It fails where Node throws, with the Go error of the failing lstat or stat.
func Sync(path string) (string, error) {
	absolute, err := nodepath.Resolve(path)
	if err != nil {
		return "", err
	}
	return Eval(absolute)
}
