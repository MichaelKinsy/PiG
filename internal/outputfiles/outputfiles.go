// Package outputfiles creates the files pi writes so the model can use output
// it was not shown in full: the full text of truncated tool output, binary MCP
// resources and images shown by codemode scripts. Every output file is created
// here, in the OS temp directory.
//
// Ports packages/coding-agent/src/utils/output-files.ts
package outputfiles

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"

	"github.com/MichaelKinsy/PiG/internal/nodeerrno"
)

// Mode is the permission of every output file. Output can carry private data,
// so only the user may read the files.
const Mode os.FileMode = 0o600

// newPath is a new, unused path: `<tmpdir>/<prefix>-<16 hex><extension>`, where tmpdir is Node's os.tmpdir().
// extension includes the dot.
func newPath(prefix, extension string) string {
	var id [8]byte
	// crypto/rand.Read never returns an error.
	_, _ = rand.Read(id[:])
	return filepath.Join(nodeTmpdir(runtime.GOOS, os.Getenv), prefix+"-"+hex.EncodeToString(id[:])+extension)
}

// create creates the file exclusively, so it never follows a link someone else
// placed at the path.
func create(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, Mode)
}

// WriteFile writes data to a new output file and returns its path. A failure
// returns an empty path and the error as Node reports it.
func WriteFile(prefix, extension string, data []byte) (string, error) {
	path := newPath(prefix, extension)
	f, err := create(path)
	if err != nil {
		return "", nodeerrno.FromPathError(err)
	}
	_, err = f.Write(data)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", nodeerrno.FromPathError(err)
	}
	return path, nil
}

// CreateStream opens a new output file for streamed output. Like Node's
// write stream, which reports an open failure asynchronously, it returns the
// path even when the file could not be created: the error accompanies a nil
// file and the caller keeps the path.
func CreateStream(prefix, extension string) (string, *os.File, error) {
	path := newPath(prefix, extension)
	f, err := create(path)
	return path, f, nodeerrno.FromPathError(err)
}
