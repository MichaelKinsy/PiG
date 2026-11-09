//go:build !(linux || darwin || freebsd)

package node

import (
	"errors"
	"os"
)

// openWalIndex is unavailable here: Windows locks byte ranges of the wal-index and has not been measured, and a system
// without one page cache for mapped and read files may show a read a store that SQLite made through its mapping late.
// A database on such a system has no commit token, and its readers read the database.
func openWalIndex(string) (*os.File, error) {
	return nil, errors.New("the wal-index is not read on this system")
}

func walIndexKey(string) ([2]uint64, bool) { return [2]uint64{}, false }
