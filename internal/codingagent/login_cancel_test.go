package codingagent

import (
	"context"
	"testing"
)

// TestCancelActiveLogin_AbortsRegisteredLogin proves Esc/Ctrl+C reaches a
// background OAuth login: after beginLogin registers its cancel, cancelActiveLogin
// invokes it (the login context becomes Done) and reports that it consumed the key.
func TestCancelActiveLogin_AbortsRegisteredLogin(t *testing.T) {
	m := &InteractiveMode{}
	ctx, cancel := context.WithCancel(context.Background())
	m.beginLogin(cancel)

	if !m.cancelActiveLogin() {
		t.Fatal("cancelActiveLogin returned false with a login active; key would fall through instead of cancelling")
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("login context not cancelled; the polling goroutine would keep waiting")
	}
}

// TestCancelActiveLogin_NoLoginIsNoOp proves an interrupt keystroke with no login
// in progress does not get swallowed: cancelActiveLogin returns false so the
// normal clear-editor / abort handling still runs.
func TestCancelActiveLogin_NoLoginIsNoOp(t *testing.T) {
	m := &InteractiveMode{}
	if m.cancelActiveLogin() {
		t.Fatal("cancelActiveLogin reported a cancel with no login active; Ctrl+C/Esc would be swallowed")
	}
}

// TestEndLogin_ClearsOnlyMatchingGeneration proves a login that finishes clears
// its own canceller (so a later Ctrl+C is not swallowed) but never a newer
// login's canceller.
func TestEndLogin_ClearsOnlyMatchingGeneration(t *testing.T) {
	m := &InteractiveMode{}

	// A completes; a subsequent cancel must find nothing active.
	_, cancelA := context.WithCancel(context.Background())
	tokenA := m.beginLogin(cancelA)
	m.endLogin(tokenA)
	if m.cancelActiveLogin() {
		t.Fatal("cancelActiveLogin cancelled after the login finished; a stale login would swallow Ctrl+C")
	}

	// B is registered, then A's late endLogin must not clear B.
	ctxB, cancelB := context.WithCancel(context.Background())
	m.beginLogin(cancelB)
	m.endLogin(tokenA) // stale token from A
	if !m.cancelActiveLogin() {
		t.Fatal("A's stale endLogin cleared B's canceller; B could no longer be cancelled")
	}
	select {
	case <-ctxB.Done():
	default:
		t.Fatal("B's context not cancelled")
	}
}
