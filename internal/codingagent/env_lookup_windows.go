package codingagent

import (
	"os"
	"syscall"
)

// envVar is an environment variable read without allocating when it is unset, empty, or compared with "1". os.Getenv on Windows converts the name to UTF-16 and
// allocates the value buffer on every call, which the getters read per frame (settings_getters_alloc_test.go) must not do.
type envVar struct {
	name string
	utf  *uint16
}

func newEnvVar(name string) envVar { return envVar{name: name, utf: syscall.StringToUTF16Ptr(name)} }

// get is os.Getenv(name). A set, nonempty variable allocates its value as os.Getenv does; an unset or empty one reads through a one-unit stack buffer and returns "".
func (v envVar) get() string {
	var buf [1]uint16
	// GetEnvironmentVariableW returns 0 for an unset or empty variable, and otherwise the length of the value, or the length it needs when the buffer is too small.
	if size, _ := syscall.GetEnvironmentVariable(v.utf, &buf[0], uint32(len(buf))); size == 0 {
		return ""
	}
	return os.Getenv(v.name)
}

// isOne reports whether the variable's value is exactly "1".
func (v envVar) isOne() bool {
	var buf [2]uint16
	size, _ := syscall.GetEnvironmentVariable(v.utf, &buf[0], uint32(len(buf)))
	return size == 1 && buf[0] == '1'
}
