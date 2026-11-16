package subprocess

import (
	"fmt"
	"path/filepath"
	"strconv"
)

func extensionArtifactName(goos, buildType string) string {
	if goos == "windows" && (buildType == "go" || buildType == "rust") {
		return "bin.exe"
	}
	return "bin"
}

func resolveSocketDirForGOOS(goos, xdgRuntimeDir, tmpDir, systemTempDir string, uid int) string {
	if goos == "windows" {
		return filepath.Join(systemTempDir, "pig")
	}
	if xdgRuntimeDir != "" {
		return filepath.Join(xdgRuntimeDir, "pig")
	}
	if tmpDir == "" {
		tmpDir = systemTempDir
	}
	return filepath.Join(tmpDir, fmt.Sprintf("pig-%d", uid))
}

// socketRuntimeDirPrefix is the os.MkdirTemp prefix of a Host's private runtime directory.
const socketRuntimeDirPrefix = "h"

// socketLeafExtension ends every socket file name newSocketPath makes.
const socketLeafExtension = ".s"

// socketLeaf is the file name of the count'th socket path of a Host: the count in base 36, then socketLeafExtension.
func socketLeaf(count uint32) string {
	return strconv.FormatUint(uint64(count), 36) + socketLeafExtension
}

// worstRuntimeSocketLeaf is the longest socket file name newSocketPath makes: the count of a uint32 at its largest.
const worstRuntimeSocketLeaf = "1z141z3" + socketLeafExtension

// lastSocketCount is the largest count a Host makes a socket path for; the next call fails rather than make a path again.
const lastSocketCount = ^uint32(0)

// worstRuntimeSocketSuffix is the longest path below the socket directory: os.MkdirTemp's <prefix><uint32> directory
// and the longest socket file name.
const worstRuntimeSocketSuffix = len("/" + socketRuntimeDirPrefix + "4294967295" + "/" + worstRuntimeSocketLeaf)

// socketRuntimeBase returns the directory below which a Host creates its private runtime directory. It is socketDir
// unless a socket below it could exceed the platform's Unix socket path limit, as with a deep $TMPDIR or %TEMP%, and
// then a short per-user directory: /tmp/pig-<uid>, or on Windows <localAppData>\pig\s. Pi's in-process extensions have
// no such limit, so a deep temp directory must not stop PiG from starting an extension. When Windows has no local
// application data directory, socketDir stays and newSocketPath reports the over-long path.
func socketRuntimeBase(goos, socketDir string, uid int, localAppData string) string {
	if len(socketDir)+worstRuntimeSocketSuffix <= unixSocketPathLimit(goos) {
		return socketDir
	}
	if goos == "windows" {
		if localAppData == "" {
			return socketDir
		}
		return filepath.Join(localAppData, "pig", "s")
	}
	return fmt.Sprintf("/tmp/pig-%d", uid)
}

func unixSocketPathLimit(goos string) int {
	if goos == "darwin" {
		return 103
	}
	return 107
}

func validateUnixSocketPath(goos, path string) error {
	limit := unixSocketPathLimit(goos)
	if len([]byte(path)) > limit {
		return fmt.Errorf("Unix socket path is %d bytes; %s supports at most %d: %s", len([]byte(path)), goos, limit, path)
	}
	return nil
}
