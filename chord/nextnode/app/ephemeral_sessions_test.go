package app

import (
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// Ports packages/chord/test/node/app/ephemeral-sessions.test.ts (Pi main c23b9ec1): at most max, the oldest ending past it; each
// ends once its last link closed and none opened for the grace period, or when none opened within it of the login.
//
// Go forced difference: Pi's tests drive fake timers; Go has none, so the grace is a few tens of milliseconds and a test waits
// for it.

const ephemeralTestGrace = 60 * time.Millisecond

var ephemeralPrincipal = delta.JsonObjectOf("kind", "user", "id", "u", "name", "u")

func alive(sessions *EphemeralSessions, id string) bool {
	_, _, ok := sessions.Get(id)
	return ok
}

func TestEphemeralSessionsEndWithTheirLastLinkAfterTheGracePeriodALinkOpeningMeanwhileKeepsThem(t *testing.T) {
	sessions := NewEphemeralSessions(10, ephemeralTestGrace)
	sessions.Issue("a", "hash", ephemeralPrincipal)
	first, second := sessions.LinkOpened("a"), sessions.LinkOpened("a")
	first()
	time.Sleep(5 * ephemeralTestGrace)
	if !alive(sessions, "a") {
		t.Fatal("a session with an open link ended")
	}
	second()
	second()
	// Within the grace period a link opens again.
	again := sessions.LinkOpened("a")
	time.Sleep(5 * ephemeralTestGrace)
	if !alive(sessions, "a") {
		t.Fatal("a session with a reopened link ended")
	}
	again()
	deadline := time.Now().Add(5 * time.Second)
	for alive(sessions, "a") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if alive(sessions, "a") {
		t.Fatal("the session outlived its grace period")
	}
	if sessions.LinkOpened("a") != nil {
		t.Fatal("a link opened on an ended session")
	}
}

func TestEphemeralSessionsEndWhenNoLinkOpensWithinTheGracePeriodOfTheLogin(t *testing.T) {
	sessions := NewEphemeralSessions(10, ephemeralTestGrace)
	sessions.Issue("a", "hash", ephemeralPrincipal)
	deadline := time.Now().Add(5 * time.Second)
	for alive(sessions, "a") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if alive(sessions, "a") {
		t.Fatal("a session nobody connected to lived on")
	}
}

func TestEphemeralSessionsKeepAtMostMaxPastItTheOldestEnds(t *testing.T) {
	sessions := NewEphemeralSessions(2, time.Minute)
	for _, id := range []string{"a", "b", "c"} {
		sessions.Issue(id, "hash", ephemeralPrincipal)
	}
	if alive(sessions, "a") || !alive(sessions, "b") || !alive(sessions, "c") {
		t.Fatalf("alive: a %v b %v c %v", alive(sessions, "a"), alive(sessions, "b"), alive(sessions, "c"))
	}
	if !sessions.Revoke("b") || sessions.Revoke("b") {
		t.Fatal("Revoke b: want true then false")
	}
	sessions.Revoke("c")
}
