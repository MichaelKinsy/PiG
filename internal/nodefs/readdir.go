// Package nodefs ports the fs contracts Pi relies on: the directory order of fs.readdirSync and fs.existsSync.
package nodefs

import (
	"os"
	"path/filepath"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// ReadDir returns the entries of the directory named by name in the order Node's fs.readdirSync returns them. libuv's scandir sorts them
// by name (strcmp) on Unix-like systems and returns them in the file system's own order on Windows, which on NTFS ignores case. On an
// error it returns the entries read before it, as os.ReadDir does. Like Node it hands out each name decoded as UTF-8, U+FFFD for each ill-formed
// subpart, so a path joined from such a name no longer names the file, as in Pi.
func ReadDir(name string) ([]os.DirEntry, error) {
	entries, err := readDir(name)
	for i, entry := range entries {
		if raw := entry.Name(); !utf8.ValidString(raw) {
			entries[i] = decodedEntry{DirEntry: entry, name: jsstring.FromUTF8([]byte(raw))}
		}
	}
	return entries, err
}

// decodedEntry is a directory entry whose name Node decoded.
type decodedEntry struct {
	os.DirEntry
	name string
}

func (e decodedEntry) Name() string { return e.name }

// EntryKind classifies a directory entry as Pi's directory collectors do: from the entry's own type, following the link with a stat only for a symlink. ok is false for a symlink whose target
// cannot be read, which those collectors skip. It does not stat a plain file or directory, so an entry whose name Node decoded still counts as the file or directory it was listed as.
func EntryKind(dir string, entry os.DirEntry) (isDir, isFile, ok bool) {
	if entry.Type()&os.ModeSymlink == 0 {
		return entry.IsDir(), entry.Type().IsRegular(), true
	}
	info, err := os.Stat(filepath.Join(dir, entry.Name()))
	if err != nil {
		return false, false, false
	}
	return info.IsDir(), info.Mode().IsRegular(), true
}
