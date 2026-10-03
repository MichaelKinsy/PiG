package subprocess

import (
	"fmt"
	"path/filepath"
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

// worstRuntimeSocketSuffix is the longest path below the socket directory: os.MkdirTemp's <prefix><uint32> directory
// and an e-<counter>.sock leaf of up to four digits.
const worstRuntimeSocketSuffix = len("/" + socketRuntimeDirPrefix + "4294967295" + "/e-9999.sock")

// socketRuntimeBase returns the directory below which a Host creates its private runtime directory. It is socketDir
// unless a socket below it could exceed the platform's Unix socket path limit, as with a deep $TMPDIR or %TEMP%, and
// then a short per-user directory: /tmp/pig-<uid>, or on Windows <localAppData>\pig\s. Pi's in-process extensions have
// no such limit, so a deep temp directory must not stop PiG from starting an extension. When Windows has no local
// application data directory, socketDir stays and sockPathFor reports the over-long path.
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
