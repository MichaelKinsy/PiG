package nodespawn

// Stdio is one entry of the stdio option of Node's spawn, as libuv's uv_spawn
// sees it.
type Stdio uint8

const (
	// Ignore is "ignore": the child gets the NUL device.
	Ignore Stdio = iota
	// Pipe is "pipe": the child gets one end of a new pipe.
	Pipe
	// Inherit is "inherit" or a file descriptor: the child gets the
	// parent's handle (UV_INHERIT_FD).
	Inherit
)
