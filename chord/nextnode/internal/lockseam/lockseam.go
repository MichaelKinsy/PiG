// Package lockseam is the test seam for process locks: on Linux and Android, process locks use abstract sockets, so a
// Linux test cannot exercise the socket-file lock path (macOS and the BSDs) without forcing it. It is unimportable
// outside chord/nextnode; the workers package assigns the hook in a non-test file; no production code calls it.
package lockseam

// ForceSocketFile, when set by the workers package, makes Unix process locks use a socket file under dir instead of an
// abstract socket on Linux and Android. Passing an empty dir restores the platform default. It returns a restore function
// that resets the previous directory.
var ForceSocketFile func(dir string) func()
