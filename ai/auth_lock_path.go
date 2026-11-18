package ai

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// maxAuthLinkHops bounds the by-hand walk over a chain of links, as the kernel bounds it (ELOOP).
const maxAuthLinkHops = 40

// resolveAuthPath returns the path the store's lock is taken for: the path made absolute, with the store file's own chain of links followed. A link is followed by hand, a relative target joined to the folder of the link as written; no folder in the path is resolved (Pi locks with lockfile.lockSync(path, { realpath: false }), beside the path as given). A dangling link gives the missing target; a path that is no link gives filepath.Abs(path). A cycle is an error that names ELOOP.
//
// pig divergence (proposed upstream): Pi takes the lock beside the link, so two links to one store (one per process) hold two locks and two sessions that refresh a token at once each write over the other's record. The write itself (internal/atomicfile) already follows the file's links, so the lock follows the same chain.
func resolveAuthPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	cur := abs
	for range maxAuthLinkHops + 1 {
		info, err := os.Lstat(cur)
		if err != nil {
			// The end of a dangling chain is the missing target; any other failure (a folder without search permission, say) is an error, not a path.
			if errors.Is(err, fs.ErrNotExist) {
				return cur, nil
			}
			return "", err
		}
		if info.Mode()&fs.ModeSymlink == 0 {
			return cur, nil
		}
		target, err := os.Readlink(cur)
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(cur), target)
		}
		cur = filepath.Clean(target)
	}
	return "", fmt.Errorf("auth: resolve %s: too many levels of symbolic links: %w", path, syscall.ELOOP)
}
