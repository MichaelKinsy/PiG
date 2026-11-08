package app

import (
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// Ports packages/chord/src/node/app/ephemeral-sessions.ts
//
// Ephemeral sessions (slice 13 §3, carry-over to slice 11): a pick-a-name login's sessions, in memory only, at most max (past
// it the oldest ends), and each ending with its client's connection: once the last Chord link authenticated with it closed and
// none opened for a grace period (a page reload, a reconnect after a drop), or when none opened within that period of the login.
//
// Go forced differences: a timer is a time.AfterFunc whose callback takes the sessions' lock (the server's process does not wait
// for it: an unstopped timer holds nothing); the grace is a time.Duration.

// EphemeralGrace is how long an ephemeral session lives without a link: a minute, past a reload and the reconnect loop's pauses.
const EphemeralGrace = 60 * time.Second

// ephemeralSession is one ephemeral session: its secret's hash, its principal, its open links, its end's timer while it has none.
type ephemeralSession struct {
	hash      string
	principal data.Principal
	links     int
	timer     *time.Timer
}

// EphemeralSessions are the server's ephemeral sessions.
type EphemeralSessions struct {
	max   int
	grace time.Duration

	mu       sync.Mutex
	sessions map[string]*ephemeralSession
	// order is insertion order, age order: the first id is the oldest.
	order []string
}

// NewEphemeralSessions makes the sessions: at most max, each ending grace after its last link (EphemeralGrace for 0).
func NewEphemeralSessions(max int, grace time.Duration) *EphemeralSessions {
	if grace == 0 {
		grace = EphemeralGrace
	}
	return &EphemeralSessions{max: max, grace: grace, sessions: map[string]*ephemeralSession{}}
}

// Issue makes session id (its secret's hash hash) for principal; the oldest ends past the cap.
func (e *EphemeralSessions) Issue(id, hash string, principal data.Principal) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if previous, ok := e.sessions[id]; ok {
		e.revokeLocked(id, previous)
	}
	session := &ephemeralSession{hash: hash, principal: principal}
	e.sessions[id] = session
	e.order = append(e.order, id)
	e.waitForLinkLocked(id, session)
	for len(e.sessions) > e.max && len(e.order) > 0 {
		oldest := e.order[0]
		e.revokeLocked(oldest, e.sessions[oldest])
	}
}

// Get is session id's hash and principal, while it lives.
func (e *EphemeralSessions) Get(id string) (hash string, principal data.Principal, ok bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	session, ok := e.sessions[id]
	if !ok {
		return "", nil, false
	}
	return session.hash, session.principal, true
}

// Revoke ends session id; whether it was one.
func (e *EphemeralSessions) Revoke(id string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	session, ok := e.sessions[id]
	if !ok {
		return false
	}
	e.revokeLocked(id, session)
	return true
}

func (e *EphemeralSessions) revokeLocked(id string, session *ephemeralSession) {
	if session.timer != nil {
		session.timer.Stop()
	}
	delete(e.sessions, id)
	for i, each := range e.order {
		if each == id {
			e.order = append(e.order[:i], e.order[i+1:]...)
			break
		}
	}
}

// LinkOpened says a link authenticated with session id opened: it returns what to call when it closes; nil for no such session.
func (e *EphemeralSessions) LinkOpened(id string) (closed func()) {
	e.mu.Lock()
	defer e.mu.Unlock()
	session, ok := e.sessions[id]
	if !ok {
		return nil
	}
	session.links++
	if session.timer != nil {
		session.timer.Stop()
		session.timer = nil
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			e.mu.Lock()
			defer e.mu.Unlock()
			session.links--
			if session.links == 0 && e.sessions[id] == session {
				e.waitForLinkLocked(id, session)
			}
		})
	}
}

// waitForLinkLocked ends session unless a link opens within the grace period.
func (e *EphemeralSessions) waitForLinkLocked(id string, session *ephemeralSession) {
	var timer *time.Timer
	timer = time.AfterFunc(e.grace, func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		// A stopped or replaced timer does nothing.
		if session.timer == timer && session.links == 0 && e.sessions[id] == session {
			e.revokeLocked(id, session)
		}
	})
	session.timer = timer
}
