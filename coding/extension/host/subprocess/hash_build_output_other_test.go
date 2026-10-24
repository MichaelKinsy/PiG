//go:build !windows

package subprocess

// churnRemovalBusy reports nothing: only Windows refuses to delete an entry another handle holds open.
func churnRemovalBusy(error) bool { return false }
