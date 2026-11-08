package workers

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
)

// Ports packages/chord/src/node/workers/base.ts `nodeBaseInputs`. The files an entry's code is made of, each with the hash of its
// bytes as read: what an edit of the server's own code outdates its bases by (the app hands them to `Plugins.WatchBase`).
//
// Forced Go differences (D600). Pi scans the import graph of a Node entry with esbuild: the entry and every file it imports under the
// given resolution `conditions`, installed packages and an import that does not resolve left out. A Go program's code is its
// executable, one file with no module graph, so the entry is that file and `conditions` (Node's `--conditions`) resolve nothing:
// they are accepted as Pi's signature has them and read by nothing. What Pi's scan guards holds here as it does there: the entry's own
// code is an input (its hash is recorded), and what cannot be read (gone, a directory, no access) is an error, which the app reports
// as `server_scan` and does not compare (Pi: "a scan that fails stops nothing; that code is then not compared").

// NodeBaseInputs is the record of the code of entry: its absolute path with the hash of its bytes. It fails when entry is not a
// regular file that can be read.
func NodeBaseInputs(entry string, conditions []string) (plugins.BaseInputs, error) {
	_ = conditions
	path, err := filepath.Abs(entry)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	hash, ok := plugins.HashFile(path)
	if !ok {
		return nil, fmt.Errorf("%s cannot be read", path)
	}
	return plugins.BaseInputs{path: hash}, nil
}
