package tools

import "path/filepath"

// virtualPath spells an absolute slash path that names nothing on disk (a remote or virtual tree behind configured operations) the way the tools
// resolve it. Pi resolves paths with node:path, so on Windows "/virtual" becomes "<process drive>:\virtual" before an operation sees it (path.resolve),
// and the operations must be keyed by that form; elsewhere the slash path is already absolute and comes back unchanged.
func virtualPath(slash string) string {
	abs, err := filepath.Abs(filepath.FromSlash(slash))
	if err != nil {
		panic(err)
	}
	return abs
}
