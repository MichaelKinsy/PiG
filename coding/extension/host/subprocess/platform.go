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
