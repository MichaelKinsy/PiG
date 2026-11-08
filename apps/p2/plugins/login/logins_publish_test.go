package login

import (
	"context"
	"runtime"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

func startedLogins(t *testing.T) (*Logins, *attempt) {
	t.Helper()
	l := NewLogins(t.TempDir())
	t.Cleanup(l.Close)
	a := &attempt{id: "attempt", owner: "owner", providerID: "faux", cancel: context.CancelFunc(func() {}), prompts: map[string]*pendingPrompt{}, done: make(chan struct{})}
	l.active = a
	l.current = loginsState{owner: "owner", login: &loginState{attemptID: a.id, providerID: "faux", status: "working", notices: []any{}}}
	return l, a
}

func statusOf(l *Logins) string {
	login, _ := l.State()
	status, _ := login.(*delta.JsonObject).Value("status").(string)
	return status
}

// A listener reads the state and gives it to the host a moment later (the yield stands for the host's loop). Whoever finds
// a login finished by another goroutine, as Cancel does when the goroutine of the login ended it first, returns only once
// that end was published: the caller's own answer follows the state it saw.
func TestFinishFoundDoneByAnotherReturnsOnceItsEndIsPublished(t *testing.T) {
	for range 200 {
		l, a := startedLogins(t)
		var mu sync.Mutex
		published := "working"
		entered := make(chan struct{}, 1)
		l.Subscribe(func() {
			status := statusOf(l)
			select {
			case entered <- struct{}{}:
			default:
			}
			for range 50 {
				runtime.Gosched()
			}
			mu.Lock()
			published = status
			mu.Unlock()
		})
		other := make(chan struct{})
		go func() {
			defer close(other)
			l.finish(a, "cancelled", "Login cancelled.")
		}()
		// The other's publication is in flight: it made the end and its listener read it, and has not given it to the host.
		<-entered
		l.finish(a, "cancelled", "Login cancelled.")
		mu.Lock()
		got := published
		mu.Unlock()
		if got != "cancelled" {
			t.Fatalf("finish returned with %q published, want cancelled", got)
		}
		<-other
	}
}

// The publications of one login go out in the order of its changes: no older state after a newer one, so the last is the
// end's.
func TestPublicationsFollowTheOrderOfTheChanges(t *testing.T) {
	for range 500 {
		l, a := startedLogins(t)
		var mu sync.Mutex
		var seen []string
		l.Subscribe(func() {
			status := statusOf(l)
			runtime.Gosched()
			mu.Lock()
			seen = append(seen, status)
			mu.Unlock()
		})
		var workers sync.WaitGroup
		for range 3 {
			workers.Go(func() {
				for range 3 {
					l.update(a, func(*loginState) {})
				}
			})
		}
		workers.Go(func() { l.finish(a, "cancelled", "Login cancelled.") })
		workers.Wait()
		mu.Lock()
		ended := false
		for _, status := range seen {
			if status == "cancelled" {
				ended = true
			} else if ended {
				t.Fatalf("an older state was published after the end: %v", seen)
			}
		}
		if !ended || seen[len(seen)-1] != "cancelled" {
			t.Fatalf("the last publication is not the end: %v", seen)
		}
		mu.Unlock()
	}
}
