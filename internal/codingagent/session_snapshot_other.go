//go:build !linux && !darwin && !windows

package codingagent

import "os"

// anonymousFile creates a file in dir and removes its name at once.
func anonymousFile(dir string) (*os.File, error) { return unlinkedTemp(dir) }

// cloneFile reports that this platform has no file clone: the snapshot is a copy.
func cloneFile(*os.File, string) (*os.File, bool) { return nil, false }
