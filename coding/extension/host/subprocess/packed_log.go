package subprocess

import (
	"os"
	"sync"
)

// packedStderrLog owns a process's diagnostic file after the parent's write handle closes. Retention and removal serialize so a late crash notice never advertises a deleted file.
type packedStderrLog struct {
	mu       sync.Mutex
	path     string
	retained bool
	removed  bool
}

func (l *packedStderrLog) retain() string {
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
func (l *packedStderrLog) remove() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.retained && !l.removed {
		_ = os.Remove(l.path)
		l.removed = true
	}
}

func (me *managedExt) retainStderrLog() string {
	if me.packedProcess != nil && me.packedProcess.stderrLog != nil {
		return me.packedProcess.stderrLog.retain()
	}
	return me.stderrLogPath
}
