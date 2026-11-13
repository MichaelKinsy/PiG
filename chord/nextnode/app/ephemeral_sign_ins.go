package app

import (
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// Ports packages/chord/src/node/app/ephemeral-sign-ins.ts (Pi ff7414d51: `sessions` until then)
//
// Ephemeral sign-ins (slice 13 §3, carry-over to slice 11): a pick-a-name login's sign-ins, in memory only, at most max (past
// it the oldest ends), and each ending with its client's connection: once the last Chord link authenticated with it closed and
// none opened for a grace period (a page reload, a reconnect after a drop), or when none opened within that period of the login.
//
// Go forced differences: a timer is a time.AfterFunc whose callback takes the sign-ins' lock (the server's process does not wait
// for it: an unstopped timer holds nothing); the grace is a time.Duration.

// EphemeralGrace is how long an ephemeral sign-in lives without a link: a minute, past a reload and the reconnect loop's pauses.
const EphemeralGrace = 60 * time.Second

// timerHandle is a started timer: Stop cancels it.
type timerHandle interface{ Stop() bool }

// afterFunc starts a timer calling fn after d; time.AfterFunc outside tests, a fake one the tests fire by hand (Go has no global
// fake timers: the unexported seam stands in for vi.useFakeTimers).
type afterFunc func(d time.Duration, fn func()) timerHandle

func realAfterFunc(d time.Duration, fn func()) timerHandle { return time.AfterFunc(d, fn) }

// ephemeralSignIn is one ephemeral sign-in: its secret's hash, its principal, its open links, its end's timer while it has none.
type ephemeralSignIn struct {
	hash      string
	principal data.Principal
	links     int
	timer     timerHandle
}

// EphemeralSignIns are the server's ephemeral sign-ins.
type EphemeralSignIns struct {
	max   int
	grace time.Duration
	after afterFunc

	mu      sync.Mutex
	signIns map[string]*ephemeralSignIn
	// order is insertion order, age order: the first id is the oldest.
	order []string
}

// NewEphemeralSignIns makes the sign-ins: at most max, each ending grace after its last link (EphemeralGrace for 0).
func NewEphemeralSignIns(max int, grace time.Duration) *EphemeralSignIns {
	if grace == 0 {
		grace = EphemeralGrace
	}
	return &EphemeralSignIns{max: max, grace: grace, after: realAfterFunc, signIns: map[string]*ephemeralSignIn{}}
}

// Issue makes sign-in id (its secret's hash hash) for principal; the oldest ends past the cap.
func (e *EphemeralSignIns) Issue(id, hash string, principal data.Principal) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if previous, ok := e.signIns[id]; ok {
		e.revokeLocked(id, previous)
	}
	signIn := &ephemeralSignIn{hash: hash, principal: principal}
	e.signIns[id] = signIn
	e.order = append(e.order, id)
	e.waitForLinkLocked(id, signIn)
	for len(e.signIns) > e.max && len(e.order) > 0 {
		oldest := e.order[0]
		e.revokeLocked(oldest, e.signIns[oldest])
	}
}

// Get is sign-in id's hash and principal, while it lives.
func (e *EphemeralSignIns) Get(id string) (hash string, principal data.Principal, ok bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	signIn, ok := e.signIns[id]
	if !ok {
		return "", nil, false
	}
	return signIn.hash, signIn.principal, true
}

// Revoke ends sign-in id; whether it was one.
func (e *EphemeralSignIns) Revoke(id string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	signIn, ok := e.signIns[id]
	if !ok {
		return false
	}
	e.revokeLocked(id, signIn)
	return true
}

func (e *EphemeralSignIns) revokeLocked(id string, signIn *ephemeralSignIn) {
	if signIn.timer != nil {
		signIn.timer.Stop()
	}
	delete(e.signIns, id)
	for i, each := range e.order {
		if each == id {
			e.order = append(e.order[:i], e.order[i+1:]...)
			break
		}
	}
}

// LinkOpened says a link authenticated with sign-in id opened: it returns what to call when it closes; nil for no such sign-in.
func (e *EphemeralSignIns) LinkOpened(id string) (closed func()) {
	e.mu.Lock()
	defer e.mu.Unlock()
	signIn, ok := e.signIns[id]
	if !ok {
		return nil
	}
	signIn.links++
	if signIn.timer != nil {
		signIn.timer.Stop()
		signIn.timer = nil
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			e.mu.Lock()
			defer e.mu.Unlock()
			signIn.links--
			if signIn.links == 0 && e.signIns[id] == signIn {
				e.waitForLinkLocked(id, signIn)
			}
		})
	}
}

// waitForLinkLocked ends sign-in unless a link opens within the grace period.
func (e *EphemeralSignIns) waitForLinkLocked(id string, signIn *ephemeralSignIn) {
	var timer timerHandle
	timer = e.after(e.grace, func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		// A stopped or replaced timer does nothing.
		if signIn.timer == timer && signIn.links == 0 && e.signIns[id] == signIn {
			e.revokeLocked(id, signIn)
		}
	})
	signIn.timer = timer
}
