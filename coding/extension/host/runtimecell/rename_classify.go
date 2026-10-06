package runtimecell

import "github.com/MichaelKinsy/PiG/internal/fsretry"

// renameRetryable reports whether a rename failed on a transient Windows lock.
func renameRetryable(err error) bool { return fsretry.TransientRename(err) }
