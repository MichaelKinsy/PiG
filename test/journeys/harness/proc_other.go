//go:build !unix

package harness

import "errors"

func signalPID(pid, sig int) error { return errors.New("signals need a unix host") }
