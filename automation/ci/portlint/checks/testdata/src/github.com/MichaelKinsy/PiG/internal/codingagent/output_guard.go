package codingagent

import (
	"io"
	"os"
)

// RawStdoutWriter stands in for the process's raw stdout writer.
func RawStdoutWriter() io.Writer { return os.Stdout }
