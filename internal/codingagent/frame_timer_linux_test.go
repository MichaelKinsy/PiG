//go:build linux

package codingagent

import (
	"errors"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// The reason for the kernel timer: a deadline that is not a whole number of milliseconds away ticks within a small fraction of a millisecond. The
// runtime's timer, whose sleep is epoll_wait's whole-millisecond timeout, ticks about half a millisecond late for the same deadlines.
func TestKernelFrameTimerTicksWithinAFractionOfAMillisecond(t *testing.T) {
	d, _ := newKernelFrameTimer().(*kernelFrameTimer)
	if d == nil {
		t.Skip("no timerfd on this kernel")
	}
	defer d.Close()
	var late []time.Duration
	for range 25 {
		const after = 2500 * time.Microsecond
		armed := time.Now()
		d.Reset(after)
		select {
		case <-d.c:
		case <-time.After(5 * time.Second):
			t.Fatal("no tick 5 s after a 2.5 ms deadline")
		}
		elapsed := time.Since(armed)
		if elapsed < after {
			t.Fatalf("ticked after %v, before its %v deadline", elapsed, after)
		}
		late = append(late, elapsed-after)
	}
	slices.Sort(late)
	// The quarter best: a loaded machine delays some wake-ups, the kernel timer delays none by itself.
	if got := late[len(late)/4]; got > 250*time.Microsecond {
		t.Fatalf("lower quartile of the lateness of 25 ticks = %v, want at most 250µs (all: %v)", got, late)
	}
}

func TestKernelFrameTimerStopDropsTheTickAndResetReplacesTheDeadline(t *testing.T) {
	d, _ := newKernelFrameTimer().(*kernelFrameTimer)
	if d == nil {
		t.Skip("no timerfd on this kernel")
	}
	defer d.Close()
	d.Reset(time.Millisecond)
	d.Stop()
	select {
	case at := <-d.c:
		t.Fatalf("a stopped deadline ticked at %v", at)
	case <-time.After(20 * time.Millisecond):
	}
	// An expired, unreceived tick is dropped by Stop as well.
	d.Reset(time.Millisecond)
	time.Sleep(10 * time.Millisecond)
	d.Stop()
	select {
	case at := <-d.c:
		t.Fatalf("Stop left the tick of %v", at)
	default:
	}
	// A later Reset replaces an earlier one: one tick, at the later deadline.
	armed := time.Now()
	d.Reset(time.Hour)
	d.Reset(3 * time.Millisecond)
	select {
	case <-d.c:
		if elapsed := time.Since(armed); elapsed < 3*time.Millisecond {
			t.Fatalf("ticked after %v, before the 3 ms deadline", elapsed)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no tick for the replaced deadline")
	}
	// A deadline already passed ticks at once instead of disarming the timer.
	d.Reset(0)
	select {
	case <-d.c:
	case <-time.After(5 * time.Second):
		t.Fatal("no tick for a deadline that has passed")
	}
}

// A deadline that has passed ticks before Reset returns, without the kernel: the reader goroutine of the descriptor runs only once the netpoller is
// polled, which a P with runnable goroutines does after they have run (the first frame of a run was 1.4 ms late that way while the process started).
// With one P the reader cannot run between Reset and the receive, so a tick that comes through the descriptor is not there yet.
func TestKernelFrameTimerTicksAtOnceForADeadlineThatHasPassed(t *testing.T) {
	d, _ := newKernelFrameTimer().(*kernelFrameTimer)
	if d == nil {
		t.Skip("no timerfd on this kernel")
	}
	defer d.Close()
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	for _, after := range []time.Duration{0, -time.Second} {
		// The deadline it replaces is gone with it.
		d.Reset(time.Hour)
		d.Reset(after)
		select {
		case <-d.c:
		default:
			t.Fatalf("no tick when Reset(%v) returned", after)
		}
		if kernelTimerArmed(t, d) {
			t.Fatalf("Reset(%v) left a deadline armed in the kernel", after)
		}
	}
}

// kernelTimerArmed reports whether the descriptor holds a deadline that has not expired.
func kernelTimerArmed(t *testing.T, d *kernelFrameTimer) bool {
	t.Helper()
	var spec unix.ItimerSpec
	if err := unix.TimerfdGettime(d.fd, &spec); err != nil {
		t.Fatal(err)
	}
	return spec.Value.Sec != 0 || spec.Value.Nsec != 0
}

// Close releases the descriptor and ends the goroutine that reads it; a late Reset or Stop touches nothing.
func TestKernelFrameTimerCloseReleasesTheDescriptorAndTheReader(t *testing.T) {
	d, _ := newKernelFrameTimer().(*kernelFrameTimer)
	if d == nil {
		t.Skip("no timerfd on this kernel")
	}
	d.Reset(time.Hour)
	d.Close()
	select {
	case <-d.done:
	default:
		t.Fatal("Close returned while the reader still runs")
	}
	var one [8]byte
	if _, err := d.file.Read(one[:]); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("reading the descriptor after Close: %v, want %v", err, os.ErrClosed)
	}
	d.Reset(time.Millisecond)
	d.Stop()
	d.Close()
	select {
	case at := <-d.c:
		t.Fatalf("a closed deadline ticked at %v", at)
	case <-time.After(10 * time.Millisecond):
	}
}

// kernelTimerOf returns the kernel timer that keeps m's frame deadline, or nil while the runtime's timer does.
func kernelTimerOf(m *InteractiveMode) *kernelFrameTimer {
	timer, _ := (*m.frameTimer.Load()).(*kernelFrameTimer)
	return timer
}

// frameOutputIs makes standard output count as a terminal, or not, for the test.
func frameOutputIs(t *testing.T, terminal bool) {
	onTerminal := frameOutputIsTerminal
	t.Cleanup(func() { frameOutputIsTerminal = onTerminal })
	frameOutputIsTerminal = func() bool { return terminal }
}

// The owner loop keeps a frame's deadline on the kernel timer only for a run that asked for it and draws on the process terminal; Stop releases
// the kernel timer and gives the deadline back to the runtime's.
func TestOwnerLoopKeepsTheFrameDeadlineOnAKernelTimerOnlyWhenAsked(t *testing.T) {
	frameOutputIs(t, true)
	plain := newPendingDisplayHarness(t)
	plain.installRenderDispatcher()
	if kernelTimerOf(plain) != nil {
		t.Fatal("a run that did not ask for it keeps its frame deadline on a kernel timer")
	}

	m := newPendingDisplayHarness(t)
	m.opts.KernelFrameDeadline = true
	frameOutputIsTerminal = func() bool { return false }
	m.installRenderDispatcher()
	if kernelTimerOf(m) != nil {
		t.Fatal("a run whose standard output is not a terminal keeps its frame deadline on a kernel timer")
	}
	frameOutputIsTerminal = func() bool { return true }
	m.installRenderDispatcher()
	timer := kernelTimerOf(m)
	if timer == nil {
		t.Skip("no timerfd on this kernel")
	}
	m.installRenderDispatcher()
	if kernelTimerOf(m) != timer {
		t.Fatal("a second renderer replaced the kernel timer")
	}
	m.armFrameTimer(time.Now().Add(1500 * time.Microsecond))
	select {
	case <-m.renderWakeCh:
	case <-time.After(5 * time.Second):
		t.Fatal("the owner loop's frame channel did not tick for an armed deadline")
	}
	m.armFrameTimer(time.Now().Add(time.Millisecond))
	m.armFrameTimer(time.Time{})
	select {
	case at := <-m.renderWakeCh:
		t.Fatalf("a disarmed deadline ticked at %v", at)
	case <-time.After(20 * time.Millisecond):
	}
	if err := m.Stop(); err != nil {
		t.Fatal(err)
	}
	if kernelTimerOf(m) != nil {
		t.Fatal("Stop left the kernel timer")
	}
	select {
	case <-timer.done:
	default:
		t.Fatal("Stop left the kernel timer's reader running")
	}
	var one [8]byte
	if _, err := timer.file.Read(one[:]); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("reading the kernel timer's descriptor after Stop: %v, want %v", err, os.ErrClosed)
	}
	m.armFrameTimer(time.Now().Add(time.Millisecond))
	select {
	case <-m.renderWakeCh:
	case <-time.After(5 * time.Second):
		t.Fatal("after Stop the runtime timer does not keep the deadline")
	}
	// A run started again takes a kernel timer again.
	m.installRenderDispatcher()
	if again := kernelTimerOf(m); again == nil || again == timer {
		t.Fatalf("the renderer of a run started again keeps its deadline on %v, want a new kernel timer", again)
	}
	if err := m.Stop(); err != nil {
		t.Fatal(err)
	}
}

// A renderer that stops (ctrl-z, an external editor, the end of the run) leaves no deadline armed in the kernel, so a suspended or idle process is
// not woken for a frame; the first frame asked for once the renderer runs again ticks.
func TestStoppedRendererLeavesTheKernelFrameTimerDisarmed(t *testing.T) {
	frameOutputIs(t, true)
	m := newPendingDisplayHarness(t)
	m.opts.KernelFrameDeadline = true
	m.installRenderDispatcher()
	timer := kernelTimerOf(m)
	if timer == nil {
		t.Skip("no timerfd on this kernel")
	}
	t.Cleanup(func() { _ = m.Stop() })
	armed := func() bool {
		var spec unix.ItimerSpec
		if err := unix.TimerfdGettime(timer.fd, &spec); err != nil {
			t.Fatal(err)
		}
		return spec.Value.Sec != 0 || spec.Value.Nsec != 0
	}
	frame := func() {
		t.Helper()
		m.tuiInst.RequestRender()
		select {
		case <-m.renderWakeCh:
		case <-time.After(5 * time.Second):
			t.Fatal("no tick for a frame that was asked for")
		}
		m.tuiInst.RunDueRender()
	}
	m.tuiInst.Start()
	frame()
	// The next frame is throttled: its deadline is most of a frame interval ahead.
	m.tuiInst.RequestRender()
	if !armed() {
		t.Fatal("a throttled frame armed no deadline in the kernel")
	}
	m.tuiInst.Stop()
	if armed() {
		t.Fatal("the stopped renderer left a deadline armed in the kernel")
	}
	select {
	case at := <-m.renderWakeCh:
		t.Fatalf("the stopped renderer's deadline ticked at %v", at)
	case <-time.After(40 * time.Millisecond):
	}
	m.tuiInst.Start()
	frame()
}

// The first frame of a run, asked for when nothing has been drawn before, is due at once: its tick is on the owner loop channel when RequestRender
// returns and the kernel holds no deadline for it (with one P, so that the descriptor reader could not have delivered it yet).
func TestFirstFrameOfARunTicksAtOnceWithoutArmingTheKernelTimer(t *testing.T) {
	frameOutputIs(t, true)
	m := newPendingDisplayHarness(t)
	m.opts.KernelFrameDeadline = true
	m.installRenderDispatcher()
	timer := kernelTimerOf(m)
	if timer == nil {
		t.Skip("no timerfd on this kernel")
	}
	t.Cleanup(func() { _ = m.Stop() })
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	m.tuiInst.Start()
	m.tuiInst.RequestRender()
	select {
	case <-m.renderWakeCh:
	default:
		t.Fatal("the first frame of a run had no tick when RequestRender returned")
	}
	if kernelTimerArmed(t, timer) {
		t.Fatal("the first frame of a run armed a deadline in the kernel")
	}
	m.tuiInst.RunDueRender()
}

// kernelTimerReaders counts the goroutines that read a kernel timer's descriptor.
func kernelTimerReaders() int {
	stacks := make([]byte, 1<<20)
	for {
		n := runtime.Stack(stacks, true)
		if n < len(stacks) {
			return strings.Count(string(stacks[:n]), "(*kernelFrameTimer).read(")
		}
		stacks = make([]byte, 2*len(stacks))
	}
}

// timerDescriptors counts the timerfd descriptors the process holds.
func timerDescriptors(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skipf("the open descriptors cannot be listed: %v", err)
	}
	count := 0
	for _, entry := range entries {
		if target, err := os.Readlink("/proc/self/fd/" + entry.Name()); err == nil && strings.Contains(target, "timerfd") {
			count++
		}
	}
	return count
}

// Every kernel timer has one reading goroutine and one descriptor, and Close leaves neither: whether the deadline is armed, expired, stopped or
// was never set, and when several goroutines close the timer at once.
func TestKernelFrameTimerCloseLeavesNoGoroutineAndNoDescriptor(t *testing.T) {
	readers, descriptors := kernelTimerReaders(), timerDescriptors(t)
	const timers = 16
	var opened []*kernelFrameTimer
	defer func() {
		for _, d := range opened {
			d.Close()
		}
	}()
	for i := range timers {
		d, _ := newKernelFrameTimer().(*kernelFrameTimer)
		if d == nil {
			t.Skip("no timerfd on this kernel")
		}
		opened = append(opened, d)
		switch i % 4 {
		case 1:
			d.Reset(time.Hour)
		case 2:
			d.Reset(time.Nanosecond)
		case 3:
			d.Reset(time.Millisecond)
			d.Stop()
		}
	}
	// A reader counts once it has started.
	for started := time.Now(); kernelTimerReaders() != readers+timers && time.Since(started) < 5*time.Second; {
		time.Sleep(time.Millisecond)
	}
	if got, fds := kernelTimerReaders(), timerDescriptors(t); got != readers+timers || fds != descriptors+timers {
		t.Fatalf("%d open timers have %d reading goroutines and %d descriptors, want %d of each", timers, got-readers, fds-descriptors, timers)
	}
	for i, d := range opened {
		if i%2 == 0 {
			d.Close()
		} else {
			var closers sync.WaitGroup
			for range 4 {
				closers.Go(d.Close)
			}
			closers.Wait()
		}
		select {
		case <-d.done:
		default:
			t.Fatalf("Close of timer %d returned while its reader still runs", i)
		}
	}
	if got, fds := kernelTimerReaders(), timerDescriptors(t); got != readers || fds != descriptors {
		t.Fatalf("the closed timers left %d reading goroutines and %d descriptors", got-readers, fds-descriptors)
	}
}

// The end of a run (Stop) leaves neither the reading goroutine nor the descriptor of the kernel timer its owner loop took.
func TestStopOfARunLeavesNoKernelTimerGoroutineAndNoDescriptor(t *testing.T) {
	frameOutputIs(t, true)
	readers, descriptors := kernelTimerReaders(), timerDescriptors(t)
	m := newPendingDisplayHarness(t)
	m.opts.KernelFrameDeadline = true
	m.installRenderDispatcher()
	if kernelTimerOf(m) == nil {
		t.Skip("no timerfd on this kernel")
	}
	m.tuiInst.Start()
	m.tuiInst.RequestRender()
	if fds := timerDescriptors(t); fds != descriptors+1 {
		t.Fatalf("the run holds %d timer descriptors, want 1", fds-descriptors)
	}
	if err := m.Stop(); err != nil {
		t.Fatal(err)
	}
	if got, fds := kernelTimerReaders(), timerDescriptors(t); got != readers || fds != descriptors {
		t.Fatalf("the stopped run left %d reading goroutines and %d timer descriptors", got-readers, fds-descriptors)
	}
}

// A tick of the kernel timer is the zero time on both of its ways to the owner loop, the expiry the reader takes from the descriptor and the
// deadline that had passed when Reset was called: the loop is told to wake and reads no clock value from the tick.
func TestKernelFrameTimerTickCarriesNoTime(t *testing.T) {
	d, _ := newKernelFrameTimer().(*kernelFrameTimer)
	if d == nil {
		t.Skip("no timerfd on this kernel")
	}
	defer d.Close()
	for _, after := range []time.Duration{time.Millisecond, 0} {
		d.Reset(after)
		select {
		case at := <-d.C():
			if !at.IsZero() {
				t.Fatalf("the tick of Reset(%v) carries the time %v, want the zero time", after, at)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("no tick for Reset(%v)", after)
		}
	}
}

// BenchmarkKernelFrameTimer measures the timer's three paths: a deadline armed and replaced before it expires (a throttled frame that is
// stopped), a deadline that had passed (the tick comes from Reset), and an expiry (the tick comes from the reading goroutine).
func BenchmarkKernelFrameTimer(b *testing.B) {
	bench := func(name string, step func(d *kernelFrameTimer)) {
		b.Run(name, func(b *testing.B) {
			d, _ := newKernelFrameTimer().(*kernelFrameTimer)
			if d == nil {
				b.Skip("no timerfd on this kernel")
			}
			defer d.Close()
			b.ReportAllocs()
			for b.Loop() {
				step(d)
			}
		})
	}
	bench("ArmAndStop", func(d *kernelFrameTimer) {
		d.Reset(16 * time.Millisecond)
		d.Stop()
	})
	bench("DueAtOnce", func(d *kernelFrameTimer) {
		d.Reset(0)
		<-d.c
	})
	bench("Expiry", func(d *kernelFrameTimer) {
		d.Reset(time.Nanosecond)
		<-d.c
	})
}
