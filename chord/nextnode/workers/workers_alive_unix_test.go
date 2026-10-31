//go:build unix

package workers

import (
	"os"
	"strconv"
	"strings"
	"syscall"
)

// alive reports whether process pid lives: a zombie its parent never reaped (a container's init that does not wait) has
// ended, as `kill(pid, 0)` cannot tell.
func alive(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return true
	}
	// The state follows the command in parentheses.
	end := strings.LastIndexByte(string(stat), ')')
	return end < 0 || end+2 >= len(stat) || stat[end+2] != 'Z'
}
