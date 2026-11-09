//go:build !linux

package boltruntime

// inUse reports whether a running process executes a file inside dir. Without /proc the store cannot tell, so it relies on the recency of use.
func inUse(string) bool { return false }
