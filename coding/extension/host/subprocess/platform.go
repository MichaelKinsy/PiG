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

func pythonExecutableCandidates(goos string) []string {
	if goos == "windows" {
		return []string{"python", "python3"}
	}
	return []string{"python3"}
}

func findPythonExecutable(goos string, lookPath func(string) (string, error)) string {
	for _, candidate := range pythonExecutableCandidates(goos) {
		if path, err := lookPath(candidate); err == nil {
			return path
		}
	}
	return pythonExecutableCandidates(goos)[0]
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

// worstRuntimeSocketSuffix is the longest path below the socket directory: os.MkdirTemp's host-<uint32> directory and
// an e-<counter>.sock leaf of up to four digits.
const worstRuntimeSocketSuffix = len("/host-4294967295/e-9999.sock")

// socketRuntimeBase returns the directory below which a Host creates its private runtime directory. It is socketDir
// unless a socket below it could exceed the platform's Unix socket path limit, as with a deep $TMPDIR, and then the
// per-user directory in /tmp. Pi's in-process extensions have no such limit, so a deep $TMPDIR must not stop PiG
// from starting an extension. Windows keeps its per-user temp directory.
func socketRuntimeBase(goos, socketDir string, uid int) string {
	if goos == "windows" || len(socketDir)+worstRuntimeSocketSuffix <= unixSocketPathLimit(goos) {
		return socketDir
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
