//go:build windows

package pigdocs

import "os"

// canCreateIn reports whether this process may create entries in the existing directory dir. Windows has no access(2) that reflects ACLs and
// read-only volumes, so it creates and removes a probe file.
func canCreateIn(dir string) bool {
	probe, err := os.CreateTemp(dir, stagingPrefix+"probe-")
	if err != nil {
		return false
	}
	name := probe.Name()
	_ = probe.Close()
	_ = os.Remove(name)
	return true
}
