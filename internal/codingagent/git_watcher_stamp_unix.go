//go:build unix

package codingagent

import "syscall"

// gitStampHasChangeTime reports whether stamps carry the change time.
const gitStampHasChangeTime = true

// gitPathStamp stats path once; its only allocation is syscall.Stat's copy of the path.
func gitPathStamp(path string) gitStatStamp {
	var st syscall.Stat_t
	if syscall.Stat(path, &st) != nil {
		return gitStatStamp{}
	}
	modified, changed := gitStatTimes(&st)
	return gitStatStamp{modified: modified, changed: changed, size: int64(st.Size)}
}
