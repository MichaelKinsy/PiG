package nodefs

import "os"

// readDir keeps the order FindNextFile returns, the order libuv's Windows scandir passes to Node unsorted.
func readDir(name string) ([]os.DirEntry, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return f.ReadDir(-1)
}
