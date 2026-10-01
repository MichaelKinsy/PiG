//go:build !windows

package nodespawn

// programEnv is pairs: libuv writes the entries of Node's env option into the
// child's environment in order outside Windows.
func programEnv(pairs []string) []string { return pairs }
