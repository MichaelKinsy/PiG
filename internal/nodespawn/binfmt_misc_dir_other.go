//go:build !linux

package nodespawn

// UseBinfmtMiscDir does nothing: only Linux reads binfmt_misc handlers when it spawns (see the Linux file).
func UseBinfmtMiscDir(string) (restore func()) { return func() {} }
