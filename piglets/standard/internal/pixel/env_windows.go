package pixel

import (
	"syscall"
	"unicode"
)

// os.Getenv on Windows converts the name to UTF-16, allocates the value
// buffer, and converts the result back: three allocations per call. These
// lookups read the same environment through GetEnvironmentVariableW into a
// stack buffer, so a per-frame call allocates nothing.
var (
	colorTermName = syscall.StringToUTF16Ptr("COLORTERM")
	wtSessionName = syscall.StringToUTF16Ptr("WT_SESSION")
)

// envBuffer holds the longest value that can match: "truecolor" and its NUL.
type envBuffer [10]uint16

// lookup returns the value's length in UTF-16 units and whether the variable
// exists. A length beyond the buffer means the value did not fit.
func lookup(name *uint16, buf *envBuffer) (n int, exists bool) {
	size, err := syscall.GetEnvironmentVariable(name, &buf[0], uint32(len(buf)))
	if size == 0 {
		// syscall.Getenv: only ERROR_ENVVAR_NOT_FOUND means the variable is unset.
		return 0, err != syscall.ERROR_ENVVAR_NOT_FOUND
	}
	return int(size), true
}

func colorTermIsTrueColor() bool {
	var buf envBuffer
	n, _ := lookup(colorTermName, &buf)
	if n >= len(buf) {
		return false
	}
	value := buf[:n]
	return foldEqual(value, "truecolor") || foldEqual(value, "24bit")
}

func inWindowsTerminal() bool {
	var buf envBuffer
	n, _ := lookup(wtSessionName, &buf)
	return n != 0
}

// foldEqual reports whether strings.ToLower of the UTF-16 value equals lower.
// A surrogate unit never lowercases to ASCII, so it never matches.
func foldEqual(value []uint16, lower string) bool {
	if len(value) != len(lower) {
		return false
	}
	for i, unit := range value {
		if unicode.ToLower(rune(unit)) != rune(lower[i]) {
			return false
		}
	}
	return true
}
