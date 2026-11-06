//go:build !windows

package childwait

import "os"

// NewPipe is the pipe a child's output goes through: the read end for Start, and the write end for the child's stdout or stderr. Where poll(2) answers
// for a pipe it is an os.Pipe.
func NewPipe() (read, write *os.File, err error) { return os.Pipe() }
