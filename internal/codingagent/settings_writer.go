package codingagent

// Ports the write queue of packages/coding-agent/src/core/settings-manager.ts (writeQueue, enqueueWrite, flush).

import (
	"fmt"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/goroutineid"
)

// settingsWrite is one queued write of a settings scope. done is closed once it has been performed, and err is then its failure.
type settingsWrite struct {
	run  func() error
	fail func(error)
	done chan struct{}
	err  error
}

// perform runs the write. A failure goes to fail, and so does a panic: Pi's queue catches whatever the task throws (settings-manager.ts:585-587).
func (write *settingsWrite) perform() {
	defer func() {
		if recovered := recover(); recovered != nil {
			err, ok := recovered.(error)
			if !ok {
				err = fmt.Errorf("%v", recovered)
			}
			write.err = err
		}
		if write.err != nil {
			write.fail(write.err)
		}
	}()
	write.err = write.run()
}

func (write *settingsWrite) performed() bool {
	select {
	case <-write.done:
		return true
	default:
		return false
	}
}

// settingsWriter performs a manager's writes one at a time, in the order they were enqueued (settings-manager.ts enqueueWrite chains every write on writeQueue), and a failing write does not stop the writes after it. The zero value is ready.
//
// Pi's queued write is a microtask that writes the file synchronously, so it has finished before the setter's caller resumes from its next await on I/O or a timer. Here the write is performed before the setter returns: by the calling goroutine, or by the goroutine that is performing writes when the setter queued its own, for which the caller waits. Only a setter called from inside one of the writer's writes returns first, and its write follows the one in progress, as Pi's follows the running microtask.
//
// What remains different from Pi is confined to one synchronous block of JavaScript: there, code that runs after a setter and before the block ends sees the file as it was, and what it changes (the project trust, a lock on the file, the file itself) is what the write finds.
type settingsWriter struct {
	mu    sync.Mutex
	queue []*settingsWrite
	// last is the write enqueued last while it has not been performed.
	last *settingsWrite
	// performer is the goroutine performing a write, 0 when none is; released is closed when it has performed it.
	performer uint64
	released  chan struct{}
}

// enqueue adds a write to the queue. The caller holds the manager's lock, so the writes are queued in the order of the changes they persist.
func (w *settingsWriter) enqueue(run func() error, fail func(error)) *settingsWrite {
	write := &settingsWrite{run: run, fail: fail, done: make(chan struct{})}
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.queue) == 0 && w.performer == 0 {
		activeSettingsWriters.add(w)
	}
	w.queue = append(w.queue, write)
	w.last = write
	return write
}

// settle returns once write has been performed, with its failure, performing queued writes on the calling goroutine while no other goroutine does. Called from inside one of the writer's writes it returns nil at once: the write is performed later and its failure is only recorded. A nil write is settled. The caller does not hold the manager's lock.
func (w *settingsWriter) settle(write *settingsWrite) error {
	if write == nil {
		return nil
	}
	self := goroutineid.Current()
	w.mu.Lock()
	// Called from inside one of this writer's writes the loop does not run: that write's goroutine performs this one after it.
	for w.performer != self {
		if w.performer != 0 {
			if write.performed() {
				break
			}
			released := w.released
			w.mu.Unlock()
			<-released
			w.mu.Lock()
			continue
		}
		if len(w.queue) == 0 {
			break
		}
		next := w.queue[0]
		w.queue[0] = nil
		w.queue = w.queue[1:]
		w.performer, w.released = self, make(chan struct{})
		w.mu.Unlock()
		w.perform(next)
		w.mu.Lock()
	}
	w.mu.Unlock()
	if write.performed() {
		return write.err
	}
	return nil
}

// perform runs one write as the performer. The release is deferred, so a write that ends its goroutine (runtime.Goexit) does not strand the writes behind it.
func (w *settingsWriter) perform(write *settingsWrite) {
	defer func() {
		close(write.done)
		w.mu.Lock()
		defer w.mu.Unlock()
		if w.last == write {
			w.last = nil
		}
		w.performer = 0
		close(w.released)
		if len(w.queue) == 0 {
			activeSettingsWriters.remove(w)
		}
	}()
	write.perform()
}

// wait returns once every write enqueued before the call has been performed (await this.writeQueue).
func (w *settingsWriter) wait() {
	w.mu.Lock()
	last := w.last
	w.mu.Unlock()
	_ = w.settle(last)
}

// activeSettingsWriters holds the writers of every manager that have a write queued or being performed, for FlushSettingsWrites. A writer adds and removes itself with its own lock held.
var activeSettingsWriters settingsWriterSet

type settingsWriterSet struct {
	mu      sync.Mutex
	writers map[*settingsWriter]struct{}
}

func (set *settingsWriterSet) add(w *settingsWriter) {
	set.mu.Lock()
	defer set.mu.Unlock()
	if set.writers == nil {
		set.writers = map[*settingsWriter]struct{}{}
	}
	set.writers[w] = struct{}{}
}

func (set *settingsWriterSet) remove(w *settingsWriter) {
	set.mu.Lock()
	defer set.mu.Unlock()
	delete(set.writers, w)
}

// FlushSettingsWrites waits until every settings write that any SettingsManager queued before the call has been performed. A process calls it before it exits, so that the exit does not cut a write another goroutine's setter is performing.
func FlushSettingsWrites() {
	activeSettingsWriters.mu.Lock()
	active := make([]*settingsWriter, 0, len(activeSettingsWriters.writers))
	for w := range activeSettingsWriters.writers {
		active = append(active, w)
	}
	activeSettingsWriters.mu.Unlock()
	for _, w := range active {
		w.wait()
	}
}
