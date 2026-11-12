//go:build linux

package codingagent

import (
	"os"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// kernelFrameTimer is the frameTimer that keeps the owner loop's frame deadline on a timerfd. The Go runtime sleeps in epoll_wait, whose timeout is whole milliseconds, so a runtime timer
// that comes due between two wake-ups fires up to a millisecond late (0.68 ms at the median of a streaming reply's 16 ms frames); a timerfd is an event
// the sleeping epoll_wait returns for when the kernel's high-resolution timer expires. One goroutine reads the descriptor: it is parked in the network
// poller while no deadline is armed or due and takes no timer wake of its own.
type kernelFrameTimer struct {
	// c holds the tick of the latest expiry the owner loop has not received yet.
	c chan time.Time

	file *os.File
	done chan struct{}

	mu     sync.Mutex // orders Reset and Stop with Close: the descriptor's number may be another file's once Close returned
	fd     int
	closed bool
}

// newKernelFrameTimer returns a disarmed timer, or nil when the kernel gives no timerfd; the caller then keeps the runtime's timer.
func newKernelFrameTimer() frameTimer {
	fd, err := unix.TimerfdCreate(unix.CLOCK_MONOTONIC, unix.TFD_NONBLOCK|unix.TFD_CLOEXEC)
	if err != nil {
		return nil
	}
	d := &kernelFrameTimer{c: make(chan time.Time, 1), file: os.NewFile(uintptr(fd), "frame-deadline"), done: make(chan struct{}), fd: fd}
	go d.read()
	return d
}

func (d *kernelFrameTimer) C() <-chan time.Time { return d.c }

// read turns each expiry into a tick until Close closes the descriptor.
func (d *kernelFrameTimer) read() {
	defer close(d.done)
	var expirations [8]byte
	for {
		if _, err := d.file.Read(expirations[:]); err != nil {
			return
		}
		select {
		case <-d.c:
		default:
		}
		select {
		case d.c <- time.Now():
		default:
		}
	}
}

// Reset arms the deadline to expire after the given time, replacing the one armed. A deadline that has passed is not handed to the kernel: the tick
// is sent from here, as the runtime timer delivers one at the next scheduling point. Through the descriptor it would reach the owner loop only
// once the netpoller is polled, which a P with runnable goroutines does after they have run: the first frame of a run, due at once while the
// process is still starting, was drawn 1.4 ms late that way.
func (d *kernelFrameTimer) Reset(after time.Duration) {
	if after <= 0 {
		d.set(0, true)
		return
	}
	d.set(after, false)
}

// Stop disarms the deadline and drops a tick not yet received.
func (d *kernelFrameTimer) Stop() {
	d.set(0, false)
	select {
	case <-d.c:
	default:
	}
}

// set arms the descriptor to expire after the given time, or disarms it for zero; tick also leaves a tick for the owner loop.
func (d *kernelFrameTimer) set(after time.Duration, tick bool) {
	spec := unix.ItimerSpec{Value: unix.NsecToTimespec(int64(after))}
	d.mu.Lock()
	if !d.closed {
		_ = unix.TimerfdSettime(d.fd, 0, &spec, nil)
		if tick {
			// The latest tick replaces one the owner loop has not received: a tick is a wake, and the renderer checks the due time itself.
			select {
			case <-d.c:
			default:
			}
			select {
			case d.c <- time.Now():
			default:
			}
		}
	}
	d.mu.Unlock()
}

// Close releases the descriptor and returns once the reading goroutine has ended. Reset and Stop do nothing afterwards.
func (d *kernelFrameTimer) Close() {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return
	}
	d.closed = true
	d.mu.Unlock()
	_ = d.file.Close()
	<-d.done
}
