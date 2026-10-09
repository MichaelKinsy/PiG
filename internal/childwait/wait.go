// Waiting for a child's output pipes after it exits.
//
// Ports packages/coding-agent/src/utils/child-process.ts waitForChildProcess.
package childwait

import (
	"os"
	"sync"
	"time"
)

// Grace mirrors upstream EXIT_STDIO_GRACE_MS.
const Grace = 100 * time.Millisecond

// Hooks order the reader, the child's exit and the grace in tests without
// sleeps. The zero value does nothing.
type Hooks struct {
	// BeforeRead runs in each reader before its first read.
	BeforeRead func()
	// GraceExpired runs when the idle grace elapses, before MayHoldOutput.
	GraceExpired func()
}

// Pipes reads a child's output pipes. Create it with Start before the child
// runs and call Wait once the child has exited.
type Pipes struct {
	files    []*os.File
	onData   func(index int, chunk []byte)
	hooks    Hooks
	activity chan struct{}
	done     chan struct{}

	mu        sync.Mutex
	accepting bool
}

// Start reads each file until it fails or ends, calling onData with the index
// of the file and the bytes read. Calls to onData never overlap, and none
// starts after Wait has abandoned the pipes.
func Start(files []*os.File, onData func(index int, chunk []byte), hooks Hooks) *Pipes {
	p := &Pipes{
		files:     files,
		onData:    onData,
		hooks:     hooks,
		activity:  make(chan struct{}, 1),
		done:      make(chan struct{}),
		accepting: true,
	}
	var readers sync.WaitGroup
	for index, file := range files {
		readers.Go(func() { p.read(index, file) })
	}
	go func() {
		readers.Wait()
		close(p.done)
	}()
	return p
}

func (p *Pipes) read(index int, file *os.File) {
	if p.hooks.BeforeRead != nil {
		p.hooks.BeforeRead()
	}
	for {
		buf := make([]byte, 64*1024)
		n, err := file.Read(buf)
		if n > 0 {
			p.mu.Lock()
			if !p.accepting {
				p.mu.Unlock()
				return
			}
			if p.onData != nil {
				p.onData(index, buf[:n])
			}
			p.mu.Unlock()
			select {
			case p.activity <- struct{}{}:
			default:
			}
		}
		if err != nil {
			return
		}
	}
}

// Wait mirrors the post-exit half of upstream waitForChildProcess: once the
// child has exited, keep reading until every pipe ends, or until no data has
// arrived on any pipe for Grace (re-armed on every chunk). A descendant that
// inherited a pipe therefore neither blocks the call nor truncates output it
// is still writing. Wait then closes the pipes, discards what a reader had not
// yet delivered when the grace released the wait, and returns after every
// reader has stopped. It reports whether every pipe ended.
//
// The grace exists only for a descendant that still holds a pipe. When it
// expires, mayHoldOutput reports whether any such process can still be alive.
// If none can, every write end is closed, so EOF follows once the readers have
// drained what the child's tree already wrote, and Wait reads to EOF.
// Otherwise a reader that fell behind (a loaded Windows runner) would drop
// output the exited command had already written: the grace measures the
// reader's own latency, not the writer's silence.
func (p *Pipes) Wait(mayHoldOutput func() bool) bool {
	// Upstream re-arms the timer only for data that arrives after exit.
	select {
	case <-p.activity:
	default:
	}
	closed := waitIdle(p.done, p.activity, mayHoldOutput, p.hooks.GraceExpired)
	if !closed {
		p.mu.Lock()
		p.accepting = false
		p.mu.Unlock()
	}
	for _, file := range p.files {
		_ = file.Close()
	}
	<-p.done
	return closed
}

func waitIdle(done, activity <-chan struct{}, mayHoldOutput func() bool, graceExpired func()) bool {
	timer := time.NewTimer(Grace)
	defer timer.Stop()
	for {
		select {
		case <-done:
			return true
		case <-activity:
			timer.Reset(Grace)
		case <-timer.C:
			if graceExpired != nil {
				graceExpired()
			}
			if mayHoldOutput() {
				return false
			}
			<-done
			return true
		}
	}
}
