//go:build unix && !darwin && !freebsd && !netbsd

package codingagent

import "syscall"

// gitStatTimes returns the modification and change times of st in nanoseconds (these systems name them Mtim and Ctim).
func gitStatTimes(st *syscall.Stat_t) (modified, changed int64) {
	return st.Mtim.Nano(), st.Ctim.Nano()
}
