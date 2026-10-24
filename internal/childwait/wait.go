// Waiting for a child's output pipes after it exits.
//
// Ports packages/coding-agent/src/utils/child-process.ts waitForChildProcess.
package childwait

import (
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// Grace mirrors upstream EXIT_STDIO_GRACE_MS.
const Grace = 100 * time.Millisecond

// Hooks order the reader, the child's exit and the grace in tests without
// sleeps. The zero value does nothing.
type Hooks struct {
	// BeforeRead runs in each reader before its first read.
	BeforeRead func()
	// AfterData runs in a reader once onData has taken a chunk, with the chunk
	// onData was lent. A test learns from it that the reader has consumed what
	// a writer wrote.
	AfterData func(index int, chunk []byte)
	// GraceTimer replaces the wall-clock timer that measures the grace, so a
	// test decides when the grace elapses instead of racing a writer against
	// host load.
	GraceTimer func() IdleTimer
	// GraceExpired runs each time the idle grace elapses, before Wait asks
	// whether the pipes can still produce output.
	GraceExpired func()
}

// IdleTimer is the clock of the grace that Wait measures. Every field is
// required.
type IdleTimer struct {
	// Expired delivers once Grace has elapsed with no chunk.
	Expired <-chan time.Time
	// Rearm restarts the grace in full. Wait calls it for each chunk that
	// arrives after the child exited, and when the grace elapsed while a pipe
	// held output that no reader had taken.
	Rearm func()
	// Stop releases the timer when Wait no longer uses it.
	Stop func()
}

// Pipes reads a child's output pipes. Create it with Start before the child
// runs and call Wait once the child has exited.
type Pipes struct {
	files    []*os.File
	onData   func(index int, chunk []byte)
	hooks    Hooks
	activity chan struct{}
	done     chan struct{}
	// stopped[i] is set once the reader of files[i] has returned: nothing more
	// is taken from that pipe, so it holds no output a reader will deliver.
	stopped []atomic.Bool

	mu        sync.Mutex
	accepting bool
}

// Start reads each file until it fails or ends, calling onData with the index
// of the file and the bytes read. Calls to onData never overlap, and none
// starts after Wait has abandoned the pipes. The chunk is lent for the call:
// each pipe reads into one buffer of its own, so onData copies what it keeps.
func Start(files []*os.File, onData func(index int, chunk []byte), hooks Hooks) *Pipes {
	p := &Pipes{
		files:     files,
		onData:    onData,
		hooks:     hooks,
		activity:  make(chan struct{}, 1),
		done:      make(chan struct{}),
		stopped:   make([]atomic.Bool, len(files)),
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

//perf:hot
func (p *Pipes) read(index int, file *os.File) {
	defer p.stopped[index].Store(true)
	if p.hooks.BeforeRead != nil {
		p.hooks.BeforeRead()
	}
	// Node reads up to 64 KiB per chunk. One buffer serves every read: a
	// line-buffered command delivers a few hundred bytes per read, and a
	// buffer per read cost a zeroed 64 KiB allocation each time.
	buf := make([]byte, 64*1024)
	for {
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
			if p.hooks.AfterData != nil {
				p.hooks.AfterData(index, buf[:n])
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

// graceVerdict is what Wait learns when the grace elapses.
type graceVerdict int

const (
	// pipesQuiet: a process may still hold a write end, and the readers have taken everything the pipes hold. The grace measured the writers' silence,
	// which is what upstream's idle timer measures: release the pipes.
	pipesQuiet graceVerdict = iota
	// pipesBehind: a pipe holds output that no reader has taken yet. The grace measured the readers' latency, not the writers' silence: keep waiting.
	pipesBehind
	// pipesEnded: no process holds a write end, so EOF follows the output the pipes hold: read to EOF.
	pipesEnded
)

// Wait mirrors the post-exit half of upstream waitForChildProcess: once the
// child has exited, keep reading until every pipe ends, or until no data has
// arrived on any pipe for Grace (re-armed on every chunk). A descendant that
// inherited a pipe therefore neither blocks the call nor truncates output it
// is still writing. Wait then closes the pipes, discards what a reader had not
// yet delivered when the grace released the wait, and returns after every
// reader has stopped. It reports whether every pipe ended.
//
// The grace exists only for a descendant that still holds a pipe, and it
// measures that holder's silence, not a reader's latency. A reader that a busy
// machine has not scheduled for a grace has taken nothing yet from a command
// that exited long before, and the grace must not release the pipe on it: what
// the exited command wrote is output, as it is for upstream, which keeps
// reading while a pipe delivers (Pi's regression test for
// earendil-works/pi#5303 drives the grace with virtual time because host load
// reorders a real subprocess's writes and the grace timer). So when the grace
// expires:
//
//   - mayHoldOutput reports whether a process can still hold a pipe that the
//     kernel cannot tell Wait about (a Windows job that is empty holds none).
//     If none can, every write end is closed, EOF follows once the readers have
//     drained what the child's tree already wrote, and Wait reads to EOF.
//   - On Unix the kernel answers for each pipe (pollPipe). A pipe with no write
//     end left ends the same way. A pipe that holds bytes no reader has taken
//     is a reader that fell behind: the grace is armed again, and the readers
//     deliver what the exited command had already written.
//   - Only a pipe that a process still holds and that holds no unread byte is
//     the quiet holder the grace is for: Wait releases it.
func (p *Pipes) Wait(mayHoldOutput func() bool) bool {
	// Upstream re-arms the timer only for data that arrives after exit.
	select {
	case <-p.activity:
	default:
	}
	closed := waitIdle(p.done, p.activity, func() graceVerdict { return p.verdict(mayHoldOutput) }, p.hooks)
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

// verdict is what the grace that just elapsed measured: see Wait.
func (p *Pipes) verdict(mayHoldOutput func() bool) graceVerdict {
	if !mayHoldOutput() {
		return pipesEnded
	}
	ended, behind := true, false
	for index, file := range p.files {
		if p.stopped[index].Load() {
			continue
		}
		hungUp, unread, known := pollPipe(file)
		switch {
		case !known:
			// The kernel cannot say: upstream's idle timer decides.
			return pipesQuiet
		case hungUp:
			// Every write end is closed; EOF follows what the pipe still holds.
		case unread:
			ended, behind = false, true
		default:
			ended = false
		}
	}
	switch {
	case ended:
		return pipesEnded
	case behind:
		return pipesBehind
	}
	return pipesQuiet
}

func waitIdle(done, activity <-chan struct{}, verdict func() graceVerdict, hooks Hooks) bool {
	var (
		wall    *time.Timer
		fake    IdleTimer
		expired <-chan time.Time
	)
	if hooks.GraceTimer != nil {
		fake = hooks.GraceTimer()
		expired = fake.Expired
		defer fake.Stop()
	} else {
		wall = time.NewTimer(Grace)
		expired = wall.C
		defer wall.Stop()
	}
	rearm := func() {
		if wall != nil {
			wall.Reset(Grace)
		} else {
			fake.Rearm()
		}
	}
	for {
		select {
		case <-done:
			return true
		case <-activity:
			rearm()
		case <-expired:
			if hooks.GraceExpired != nil {
				hooks.GraceExpired()
			}
			switch verdict() {
			case pipesEnded:
				<-done
				return true
			case pipesBehind:
				rearm()
			default:
				return false
			}
		}
	}
}
