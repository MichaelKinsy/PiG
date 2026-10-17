//go:build !unix

package codingagent

import "os"

// gitStampHasChangeTime reports whether stamps carry the change time.
const gitStampHasChangeTime = false

// gitPathStamp reads the modification time and size; Windows reports no change time.
func gitPathStamp(path string) gitStatStamp {
	info, err := os.Stat(path)
	if err != nil {
		return gitStatStamp{}
	}
	return gitStatStamp{modified: info.ModTime().UnixNano(), size: info.Size()}
}
