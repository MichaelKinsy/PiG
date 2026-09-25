package codingagent

import (
	"os"
	"regexp"
)

var wslReleasePattern = regexp.MustCompile(`(?i)microsoft|wsl`)

// IsWSL reports whether the process runs under Windows Subsystem for Linux,
// where Windows executables are reachable through interop. Ports
// utils/wsl.ts isWSL: WSL_DISTRO_NAME or WSLENV, else a /proc/version that
// names Microsoft or WSL. getenv and readFile are injected so tests never
// read the host.
func IsWSL(getenv func(string) string, readFile func(string) ([]byte, error)) bool {
	if getenv("WSL_DISTRO_NAME") != "" || getenv("WSLENV") != "" {
		return true
	}
	release, err := readFile("/proc/version")
	if err != nil {
		return false
	}
	return wslReleasePattern.Match(release)
}

// isHostWSL applies IsWSL to this process.
func isHostWSL() bool {
	return IsWSL(os.Getenv, os.ReadFile)
}
