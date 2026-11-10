package ai

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// createNewFollowingLinks creates the file at path with create (an exclusive create) and, when path is a symlink whose target does not exist yet, creates the target. Exclusive creation reports "exists" for a dangling link and never creates its target, but Pi's ensureFileExists is writeFileSync(path, "{}"), which follows the link and creates it (auth-storage.ts:63-67). The agent directory of #11655 is such a link to a shared file that is not there yet. Other errors, and a file that exists, are create's own.
func createNewFollowingLinks(path string, create func(string) (*os.File, error)) (*os.File, error) {
	file, err := create(path)
	if !errors.Is(err, fs.ErrExist) || pathExists(path) {
		return file, err
	}
	target := path
	// A link cycle stops at its first repeat, where the exclusive create reports "exists" as Node's writeFileSync reports ELOOP: the walk is bounded by the chain itself, with no depth constant.
	seen := map[string]bool{}
	for !seen[target] {
		seen[target] = true
		//perf:ok cold path: runs only when the exclusive create reported a dangling link; each hop is a link of the chain being resolved
		info, statErr := os.Lstat(target)
		if statErr != nil || info.Mode()&fs.ModeSymlink == 0 {
			break
		}
		next, readErr := os.Readlink(target)
		if readErr != nil {
			return nil, err
		}
		if !filepath.IsAbs(next) {
			next = filepath.Join(filepath.Dir(target), next)
		}
		target = next
	}
	if target == path {
		return nil, err
	}
	return create(target)
}
