package codingagent

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
)

// snapshotSeq numbers the snapshot file names this process takes. A name is never derived from the clock: a Windows clock ticks every
// half to fifteen milliseconds, so two loads of small files in one tick took one name, and the second could make no snapshot.
var snapshotSeq atomic.Uint64

// createSnapshotFile creates a snapshot file in dir with create, which makes the file at the name it is given and reports fs.ErrExist when
// that name is taken (Windows CREATE_NEW, clonefile). The name is the process id and the next number of snapshotSeq, so no two names of this
// process are alike; a name taken by a file of another process (a leftover of one that had this id) is skipped. Each try takes a new number, so
// the loop ends at the first free name: it cannot spin on a name that stays taken. Any other error ends it.
func createSnapshotFile(dir string, create func(name string) (*os.File, error)) (*os.File, error) {
	for {
		name := filepath.Join(dir, ".pig-snapshot-"+strconv.Itoa(os.Getpid())+"-"+strconv.FormatUint(snapshotSeq.Add(1), 36))
		file, err := create(name)
		if !errors.Is(err, fs.ErrExist) {
			return file, err
		}
	}
}
