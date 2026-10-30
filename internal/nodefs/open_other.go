//go:build !darwin

package nodefs

import "os"

// namesFIFO is false off macOS: no other kernel loses a FIFO's open to a signal, so OpenFile is os.OpenFile.
func namesFIFO(string) bool { return false }

// quietOpen is never reached off macOS.
func quietOpen(name string, flag int, perm os.FileMode) (*os.File, error) {
	return os.OpenFile(name, flag, perm)
}
