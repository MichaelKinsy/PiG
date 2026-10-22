package host

import (
	"testing"
	"time"
)

// A callback that fired runs once and a later stop changes nothing; a stop before it fires keeps it from running (the
// slot loop.after hands its timer is emptied by whichever of the two comes first).
func TestAfterRunsACallbackOnceAndNotAfterStop(t *testing.T) {
	var l loop
	fired := make(chan struct{}, 2)
	stop := l.after(time.Millisecond, func() { fired <- struct{}{} })
	<-fired
	stop()
	stopped := l.after(time.Hour, func() { fired <- struct{}{} })
	stopped()
	select {
	case <-fired:
		t.Fatal("a stopped callback ran")
	case <-time.After(20 * time.Millisecond):
	}
	select {
	case <-fired:
		t.Fatal("the fired callback ran twice")
	default:
	}
}
