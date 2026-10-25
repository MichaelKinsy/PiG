package toolchain

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// pig additive (D195): a packager supplies the toolchains PiG builds and launches extensions with in one directory, which PATH cannot override.

// installedToolchainDir is the directory of executables a packager installed for PiG's extension builds and launches: go, cargo, rustc,
// python3, node, and the C compiler and linker they run. A packager sets it at link time:
//
//	-ldflags "-X github.com/MichaelKinsy/PiG/internal/toolchain.installedToolchainDir=<dir>"
//
// Empty, the default, finds each toolchain on PATH (and Go also in the PiG-managed toolchain). Set, [LookPath] reads only this directory, so
// a conflicting tool on PATH, a version manager's shim, or a PiG-managed Go never replaces the installation's toolchain. Processes other
// than toolchain commands, such as the commands the bash tool runs, keep the user's PATH.
var installedToolchainDir string

// InstalledToolchainDir returns the directory of the toolchains this installation supplies, or "" when toolchains come from PATH.
func InstalledToolchainDir() string { return installedToolchainDir }

// SetInstalledToolchainDirForTest sets the installed toolchain directory and returns the function that restores the previous one. Only
// tests call it: a program sets the directory at link time.
func SetInstalledToolchainDirForTest(dir string) (restore func()) {
	previous := installedToolchainDir
	installedToolchainDir = dir
	resolveMu.Lock()
	clear(resolveMemo)
	resolveMu.Unlock()
	return func() {
		installedToolchainDir = previous
		resolveMu.Lock()
		clear(resolveMemo)
		resolveMu.Unlock()
	}
}

// LookPath finds the toolchain executable name: in the installed toolchain directory when one is configured, and only there, else on PATH
// as exec.LookPath does. A miss wraps exec.ErrNotFound.
func LookPath(name string) (string, error) {
	dir := installedToolchainDir
	if dir == "" {
		return exec.LookPath(name)
	}
	path := filepath.Join(dir, exeName(name))
	if info, err := os.Stat(path); err == nil && !info.IsDir() && (exeName(name) != name || info.Mode()&0o111 != 0) {
		return path, nil
	}
	return "", &exec.Error{Name: name, Err: fmt.Errorf("not in this installation's toolchain directory %s: %w", dir, exec.ErrNotFound)}
}

// Command returns the command to run for the toolchain executable name: name itself when no toolchain directory is configured, so the
// start finds it on PATH as before, else its path in the installed toolchain directory, whose start reports a tool the installation lacks.
func Command(name string) string {
	if installedToolchainDir == "" {
		return name
	}
	return filepath.Join(installedToolchainDir, exeName(name))
}

// Environ returns environ with the installed toolchain directory first on PATH, so a toolchain command finds the tools it runs by name
// (cargo runs rustc and the linker, cgo runs the C compiler) in the same installation. It returns environ unchanged when no toolchain
// directory is configured.
func Environ(environ []string) []string {
	dir := installedToolchainDir
	if dir == "" {
		return environ
	}
	out := slices.Clone(environ)
	for i, kv := range out {
		if envKey(kv, "PATH") {
			name, value, _ := strings.Cut(kv, "=")
			out[i] = name + "=" + dir + string(os.PathListSeparator) + value
			return out
		}
	}
	return append(out, "PATH="+dir)
}
