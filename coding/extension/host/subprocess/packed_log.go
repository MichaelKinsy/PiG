package subprocess

import (
	"errors"
	"io/fs"
	"os"
	"sync"
)

// processStderrLog owns a process's diagnostic file after the parent's write handle closes. Retention and removal serialize so a late crash notice never advertises a deleted file.
type processStderrLog struct {
	mu       sync.Mutex
	path     string
	retained bool
	removed  bool
}

func (l *processStderrLog) retain() string {
	if l == nil {
		return ""
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.removed {
		return ""
	}
	l.retained = true
	return l.path
}

// remove runs only after the process tree and the parent's write handle close. Files named in failure diagnostics remain available after shutdown and reload.
func (l *processStderrLog) remove() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.retained || l.removed {
		return
	}
	if err := os.Remove(l.path); err == nil || errors.Is(err, fs.ErrNotExist) {
		l.removed = true
	}
}

func (me *managedExt) retainStderrLog() string {
	if me.packedProcess != nil && me.packedProcess.stderrLog != nil {
		return me.packedProcess.stderrLog.retain()
	}
	if me.stderrLog != nil {
		return me.stderrLog.retain()
	}
	return me.stderrLogPath
}
