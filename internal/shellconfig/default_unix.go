//go:build !windows

package shellconfig

import (
	"os"
)

// Default resolves the shell on unix, mirroring upstream getShellConfig's unix
// branch (shell.ts). It never errors.
//
// Ports packages/coding-agent/src/utils/shell.ts (getShellConfig, unix branch).
func Default() (Config, error) {
	return unixDefault(fileExists, FindExecutableOnPath), nil
}

// unixDefault tries /bin/bash, then bash on PATH (findOnPath), then falls back to sh
// (resolved through PATH at spawn time, like upstream's bare "sh").
func unixDefault(exists func(string) bool, findOnPath func(string) string) Config {
	if exists("/bin/bash") {
		return ForBash("/bin/bash")
	}
	if bash := findOnPath("bash"); bash != "" {
		return ForBash(bash)
	}
	return Config{Path: "sh", Args: []string{"-c"}}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
