package host

import "github.com/MichaelKinsy/PiG/chord/next/internal/loopseam"

func init() {
	loopseam.Quiesce = func(loop any) {
		if l, ok := loop.(*Loop); ok {
			l.quiesce()
		}
	}
}

// joined is Go for a hop that is a link of a chain Pi runs as microtasks, never a wait for something outside the host (a
// plugin's gate, a timer, a link, a held commit): the task a test's `delivered()` joins (quiesce). hops is guarded by mu.
func (l *Loop) joined(fn func()) {
	l.mu.Lock()
	if l.hops == 0 {
		l.quiet = make(chan struct{})
	}
	l.hops++
	l.mu.Unlock()
	l.Go(func() {
		defer l.hopDone()
		fn()
	})
}

func (l *Loop) hopDone() {
	l.mu.Lock()
	l.hops--
	if l.hops == 0 {
		close(l.quiet)
	}
	l.mu.Unlock()
}

// quiesce blocks until no joined hop is in flight. A hop that ended may have started another before it ended (a section it ran
// queued one): the count never falls to zero in between.
func (l *Loop) quiesce() {
	for {
		l.mu.Lock()
		quiet, busy := l.quiet, l.hops > 0
		l.mu.Unlock()
		if !busy {
			return
		}
		<-quiet
	}
}
