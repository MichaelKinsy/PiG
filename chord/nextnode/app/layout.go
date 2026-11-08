package app

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// Ports packages/chord/src/node/app/layout.ts
//
// The app kit's data directory (slice 13 §4, decision "The app kit, point 4: storage"): one fixed layout, made private at
// every start. `server/` holds the server's store and files, `workers/<kind>/<address>/` each worker's store and files,
// `plugins/` the user's plugins root, `logs/` the logs, `owner-token` the owner's token for command-line clients.
// Directories are 0700 and stores 0600, also when they existed looser before (other local users must not read the
// sessions in a store, nor replace it).
//
// Go forced differences: the asynchronous functions block and return `(value, error)`; `process.getuid` is os.Getuid
// (not on Windows, where the owner check is skipped as Pi's `process.getuid === undefined` skips it).

// AppLayout is the paths of a data directory.
type AppLayout struct {
	DataDir      string
	Server       string
	ServerStore  string
	Workers      string
	Plugins      string
	Logs         string
	OwnerToken   string
	CookieSecret string
}

// DefaultDataDir is the data directory of app name unless the start script names one: `~/.<name>`.
func DefaultDataDir(name string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "."+name)
}

// OpenDataDir is the real path of the data directory path, made when missing (mode 0700), refused when the kit must not
// make it private: the home directory, a file system's root, or a directory another user owns (a clear error, instead of
// tightening a directory others use or failing on `EPERM`). Its mode is set on the real path, never through a link.
func OpenDataDir(path string) (string, error) {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return "", err
	}
	dataDir, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	home, _ := os.UserHomeDir()
	if realHome, err := filepath.EvalSymlinks(home); err == nil {
		home = realHome
	}
	if dataDir == home || dataDir == filepath.VolumeName(dataDir)+string(filepath.Separator) {
		what := "a file system's root"
		if dataDir == home {
			what = "the home directory"
		}
		return "", fmt.Errorf("startServer: the data directory %s is %s; name a directory of the app's own", dataDir, what)
	}
	info, err := os.Stat(dataDir)
	if err != nil {
		return "", err
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && int(stat.Uid) != os.Getuid() {
		return "", fmt.Errorf("startServer: the data directory %s belongs to another user (uid %d)", dataDir, stat.Uid)
	}
	return dataDir, nil
}

// CreateLayout is the layout of dataDir (a real path, OpenDataDir's), its directories made private (PrivateDirectory).
func CreateLayout(dataDir string) (AppLayout, error) {
	if err := PrivateDirectory(dataDir); err != nil {
		return AppLayout{}, err
	}
	layout := AppLayout{
		DataDir:      dataDir,
		Server:       filepath.Join(dataDir, "server"),
		ServerStore:  filepath.Join(dataDir, "server", "store.sqlite"),
		Workers:      filepath.Join(dataDir, "workers"),
		Plugins:      filepath.Join(dataDir, "plugins"),
		Logs:         filepath.Join(dataDir, "logs"),
		OwnerToken:   filepath.Join(dataDir, "owner-token"),
		CookieSecret: filepath.Join(dataDir, "server", "cookie-secret"),
	}
	for _, dir := range []string{layout.Server, layout.Workers, layout.Plugins, layout.Logs} {
		if err := PrivateDirectory(dir); err != nil {
			return AppLayout{}, err
		}
	}
	return layout, nil
}

// PrivateDirectory makes dir (and missing parents) with mode 0700, and sets 0700 on it when it existed.
func PrivateDirectory(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.Chmod(dir, 0o700)
}

// PrivateStore makes the store file path private before SQLite opens it: created empty with mode 0600 when missing (SQLite
// gives its journal files the database's mode), else set to 0600 with the journal files it has.
func PrivateStore(path string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	for _, name := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Chmod(name, 0o600); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// WorkerDirectory is the directory of the worker at address: `workers/<kind>/<its key, encoded>`.
func WorkerDirectory(dataDir string, address contracts.Address) string {
	return filepath.Join(dataDir, "workers", address.Kind, encodedKey(address.Key))
}

// readableKey is the longest key kept readable as a directory name; a longer one is named by its hash.
const readableKey = 160

// encodedKey is an address key as one directory name: percent-encoded (`/`, `\`, `.` and every other character a file
// system might treat specially included), or its SHA-256 when that is longer than readableKey. Capital letters are encoded
// too: on a case-insensitive file system (macOS) `p|A` and `p|a` would otherwise share a directory.
func encodedKey(key string) string {
	var out strings.Builder
	text := encodeURIComponent(key)
	for index := 0; index < len(text); index++ {
		b := text[index]
		switch {
		case b == '%':
			// An escape stays as it is (its hex digits are upper case already).
			out.WriteString(text[index : index+3])
			index += 2
		case b == '.' || b == '!' || b == '\'' || b == '(' || b == ')' || b == '*' || b == '~' || (b >= 'A' && b <= 'Z'):
			fmt.Fprintf(&out, "%%%02X", b)
		default:
			out.WriteByte(b)
		}
	}
	if encoded := out.String(); len(encoded) <= readableKey {
		return encoded
	}
	sum := sha256.Sum256([]byte(key))
	return "sha256-" + hex.EncodeToString(sum[:])
}

// encodeURIComponent is JavaScript's: everything but A-Z a-z 0-9 - _ . ! ~ * ' ( ) is percent-encoded as UTF-8 (a lone
// surrogate, which JavaScript refuses, is a replacement character here).
func encodeURIComponent(s string) string {
	escaped := url.QueryEscape(s)
	// QueryEscape writes a space as `+` and leaves `~` alone; JavaScript writes %20 and also leaves `!'()*` alone.
	escaped = strings.ReplaceAll(escaped, "+", "%20")
	for _, kept := range []string{"!", "'", "(", ")", "*"} {
		escaped = strings.ReplaceAll(escaped, url.QueryEscape(kept), kept)
	}
	return escaped
}
