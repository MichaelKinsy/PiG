package subprocess

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"
)

// processStderrLog owns the parent's diagnostic writer and file. The writer closes only after host output copies drain; retention and removal serialize so a late crash notice never advertises a deleted file.
type processStderrLog struct {
	mu       sync.Mutex
	path     string
	writer   *os.File
	retained bool
	removed  bool
	// head counts the bytes Write put in the file. After the file reached stderrHeadMax, overflow counts the bytes Write received and tail holds the newest of them.
	head     int
	overflow int
	tail     []byte
}

// An extension's stderr log keeps its first stderrHeadMax bytes, where a startup failure is, and its last stderrTailMax bytes, where a crash is. A flooding extension cannot fill the disk, and its pipe is always drained.
const (
	stderrHeadMax = 1 << 20
	stderrTailMax = 64 << 10
)

// Write records extension stderr within the head and tail bounds. It never fails, so the pipe it drains never stops.
func (l *processStderrLog) Write(p []byte) (int, error) {
	n := len(p)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.writer == nil {
		return n, nil
	}
	if room := stderrHeadMax - l.head; room > 0 {
		chunk := p[:min(len(p), room)]
		w, _ := l.writer.Write(chunk)
		l.head += w
		p = p[len(chunk):]
	}
	if len(p) == 0 {
		return n, nil
	}
	l.overflow += len(p)
	if len(p) >= stderrTailMax {
		l.tail = append(l.tail[:0], p[len(p)-stderrTailMax:]...)
		return n, nil
	}
	l.tail = append(l.tail, p...)
	if over := len(l.tail) - stderrTailMax; over > 0 {
		l.tail = append(l.tail[:0], l.tail[over:]...)
	}
	return n, nil
}

func (l *processStderrLog) closeWriter() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.writer != nil {
		if l.overflow > 0 {
			if dropped := l.overflow - len(l.tail); dropped > 0 {
				_, _ = fmt.Fprintf(l.writer, "\n[... %d bytes of extension stderr dropped ...]\n", dropped)
			}
			_, _ = l.writer.Write(l.tail)
			l.tail, l.overflow = nil, 0
		}
		_ = l.writer.Close()
		l.writer = nil
	}
}

// recordFailure preserves a structured Node factory failure without printing it a second time. Live output and this diagnostic use the same file offset; after output drains, a separate append handle is safe.
func (l *processStderrLog) recordFailure(failure *FactoryLoadError) error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	text := failure.Message
	if failure.Stack != "" {
		text += "\n" + failure.Stack
	}
	if l.writer != nil {
		_, err := fmt.Fprintln(l.writer, text)
		return err
	}
	file, err := os.OpenFile(l.path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(file, text)
	return errors.Join(err, file.Close())
}

func (me *managedExt) factoryLoadError(failure *FactoryLoadError) *LoadError {
	log := me.stderrLog
	if me.packedProcess != nil {
		log = me.packedProcess.stderrLog
	}
	loadErr := newLoadError(me.config.Name, "load", "load_failed", failure)
	if err := log.recordFailure(failure); err != nil {
		loadErr.Err = fmt.Errorf("%s; write extension diagnostic: %w", failure.Message, err)
	}
	loadErr.StderrLog = me.stderrLogPath
	return loadErr
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
