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
		"access denied (5)":      syscall.Errno(5),
		"sharing violation (32)": link(syscall.Errno(32)),
		"lock violation (33)":    link(syscall.Errno(33)),
		"wrapped":                errors.Join(errors.New("context"), link(syscall.Errno(32))),
	} {
		if !transientWindowsCode(err) {
			t.Errorf("%s: transientWindowsCode = false, want a retry", name)
		}
	}
	for name, err := range map[string]error{
		"nil":                nil,
		"file not found (2)": link(syscall.Errno(2)),
		"disk full (112)":    link(syscall.Errno(112)),
		"plain":              errors.New("access is denied"),
	} {
		if transientWindowsCode(err) {
			t.Errorf("%s: transientWindowsCode = true, want the error reported unchanged", name)
		}
	}
}
