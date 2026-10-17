//go:build darwin || freebsd || netbsd

package codingagent

import "syscall"

// gitStatTimes returns the modification and change times of st in nanoseconds (these systems name them Mtimespec and Ctimespec).
func gitStatTimes(st *syscall.Stat_t) (modified, changed int64) {
	return st.Mtimespec.Nano(), st.Ctimespec.Nano()
}
