package surfaces

import (
	"fmt"
	"os"
	"runtime/pprof"
	"sync/atomic"
	"testing"
	"time"
)

// pi: packages/chord/test/surfaces/active-language.test.ts and active-theme.test.ts (`choice.pick.watch(ctx).then(start, () => {})`:
// a follower stopped before its hold binds ends quietly, and its changed callback runs with no lock held).

// spin busy-waits for d: it varies where in the follower's pick-watch goroutine Stop lands, and orders nothing.
func spin(d time.Duration) {
	for end := time.Now().Add(d); time.Now().Before(end); {
	}
}

// stopWithin runs stop and ends the test binary, with every goroutine's stack, when it has not returned in a minute. A Stop that
// waits for a callback that holds the host's loop is a deadlock of the loop, so the test's own cleanup (it closes the host through
// the same loop) would hang behind it: t.Fatal would only move the hang to the go test timeout.
func stopWithin(what string, stop func()) {
	done := make(chan struct{})
	go func() { defer close(done); stop() }()
	select {
	case <-done:
	case <-time.After(time.Minute):
		_ = pprof.Lookup("goroutine").WriteTo(os.Stderr, 1)
		fmt.Fprintf(os.Stderr, "FAIL: %s did not return: a callback of the follower's pick watch blocks the host's loop\n", what)
		os.Exit(1)
	}
}

// A follower stopped at once, before the server answers its pick watch, ends quietly. The pick watch's start is a microtask of the
// host's loop (WatchThen) that Stop races with at every moment of the bind: each Stop is bounded (stopWithin), and the test waits
// for the surfaces goroutines to end, so a panic there (the review probe's "Watch is stopped") takes the test binary down inside
// the test instead of after it.
func TestStoppingAFollowerBeforeItsPickWatchBindsEndsQuietly(t *testing.T) {
	notes := notesEnDe(t)
	client, closeAll := languageSetup(t, notes)
	defer closeAll()
	for i := range 300 {
		follower, err := FollowActiveLanguage(client, []string{"de"}, func(*Language) {}, func(MessagesReport) {}, bg)
		if err != nil {
			t.Fatal(err)
		}
		spin(time.Duration(i%13) * 20 * time.Microsecond)
		stopWithin("a language follower's Stop", follower.Stop)
	}
	noSurfacesGoroutines(t)
}

func TestStoppingAThemeFollowerBeforeItsPickWatchBindsEndsQuietly(t *testing.T) {
	client := themeClient(t)
	for i := range 300 {
		follower, err := FollowActiveTheme(client, "dark", func(Theme) {}, bg)
		if err != nil {
			t.Fatal(err)
		}
		spin(time.Duration(i%13) * 20 * time.Microsecond)
		stopWithin("a theme follower's Stop", follower.Stop)
	}
	noSurfacesGoroutines(t)
}

// SetDevice and Stop from the callback itself do not deadlock (Pi calls changed with no lock held).
func TestAFollowersCallbackMayStopItAndChangeTheDevice(t *testing.T) {
	notes := notesEnDe(t)
	client, closeAll := languageSetup(t, notes)
	defer closeAll()
	var follower atomic.Pointer[LanguageFollower]
	var calls atomic.Int32
	done := make(chan struct{})
	made, err := FollowActiveLanguage(client, []string{"en"}, func(*Language) {
		me := follower.Load()
		if me == nil || calls.Add(1) != 1 {
			return
		}
		me.SetDevice([]string{"de"})
		me.Stop()
		close(done)
	}, func(MessagesReport) {}, bg)
	if err != nil {
		t.Fatal(err)
	}
	follower.Store(made)
	go made.SetDevice([]string{"fr"})
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the callback's SetDevice and Stop did not return: the follower holds a lock across the callback")
	}
}

func TestAThemeFollowersCallbackMayStopItAndSetTheMode(t *testing.T) {
	client := themeClient(t)
	var follower atomic.Pointer[ThemeFollower]
	var calls atomic.Int32
	done := make(chan struct{})
	made, err := FollowActiveTheme(client, "dark", func(Theme) {
		me := follower.Load()
		if me == nil || calls.Add(1) != 1 {
			return
		}
		me.SetMode("light")
		me.Stop()
		close(done)
	}, bg)
	if err != nil {
		t.Fatal(err)
	}
	follower.Store(made)
	go made.SetMode("light")
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the callback's SetMode and Stop did not return: the follower holds a lock across the callback")
	}
}

// A change whose delivery panicked does not stop the next ones.
func TestAnOutboxDeliversAgainAfterADeliveryPanicked(t *testing.T) {
	var o outbox
	func() {
		defer func() { _ = recover() }()
		o.run(func() { panic("deliver") })
	}()
	delivered := false
	o.run(func() { delivered = true })
	if !delivered {
		t.Fatal("the outbox delivers nothing after a delivery panicked")
	}
}
