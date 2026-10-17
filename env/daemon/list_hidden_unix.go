//go:build !windows

package daemon

// hiddenAttribute is whether the file has a hidden attribute, which only Windows files have.
func hiddenAttribute(string) bool { return false }
