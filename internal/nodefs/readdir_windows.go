package nodefs

import "os"

// readDir keeps the order FindNextFile returns, the order libuv's Windows scandir passes to Node unsorted.
func readDir(name string) ([]os.DirEntry, error) {
	f, err := os.Open(name) //nolint:gosec // G304: the path names a file that the user, the project config, or PiG's own state directory designates; the CLI runs as the user with no sandbox, as Pi does, so reading it grants nothing the user lacks
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return f.ReadDir(-1)
}
