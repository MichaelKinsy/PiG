//go:build !linux && !android && !darwin && !freebsd

package app

import (
	"os"
	"time"
)

// ctimeOf is the file's modification time where Go exposes no status-change time (Windows and the other systems): its mtime
// alone decides, a metadata-only change is not seen there.
func ctimeOf(info os.FileInfo) time.Time { return info.ModTime() }
