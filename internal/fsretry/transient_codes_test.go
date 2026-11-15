package fsretry

import (
	"errors"
	"os"
	"syscall"
	"testing"
)

func TestTransientWindowsCode(t *testing.T) {
	link := func(err error) error { return &os.LinkError{Op: "rename", Old: "a", New: "b", Err: err} }
	for name, err := range map[string]error{
		"access denied (5)":                         syscall.Errno(5),
		"sharing violation (32)":                    link(syscall.Errno(32)),
		"lock violation (33)":                       link(syscall.Errno(33)),
		"replaced file could not be removed (1175)": syscall.Errno(1175),
		"replacement could not be moved (1176)":     link(syscall.Errno(1176)),
		"wrapped":                                   errors.Join(errors.New("context"), link(syscall.Errno(1175))),
	} {
		if !transientWindowsCode(err) {
			t.Errorf("%s: transientWindowsCode = false, want a retry", name)
		}
	}
	for name, err := range map[string]error{
		"nil":                                   nil,
		"file not found (2)":                    link(syscall.Errno(2)),
		"disk full (112)":                       link(syscall.Errno(112)),
		"replacement moved, rest failed (1177)": syscall.Errno(1177),
		"plain":                                 errors.New("access is denied"),
	} {
		if transientWindowsCode(err) {
			t.Errorf("%s: transientWindowsCode = true, want the error reported unchanged", name)
		}
	}
}
