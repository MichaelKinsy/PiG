//go:build !windows

package runtimecell

// windowsDeletion reports nothing: only Windows keeps a directory visible, and its open failing, while another process deletes it.
func windowsDeletion(error) (pending, denied bool) { return false, false }
