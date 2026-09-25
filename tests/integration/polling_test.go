//go:build integration

package integration

import (
	"strings"
	"testing"
	"time"
)

// pollInterval is the default sub-second cadence for poll loops in this
// package. 200ms is fast enough that LLM-driven assertions don't waste
// >0.2s of slack and slow enough that we don't hammer tmux.
const pollInterval = 200 * time.Millisecond

// pollUntil runs cond every pollInterval until it returns true or
// timeout elapses. Returns true if cond ever returned true.
//
// Use this in place of fixed time.Sleep("worst case") waits. The whole
// suite gets faster on the happy path while keeping the slow-LLM
// pessimistic ceiling intact.
func pollUntil(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(pollInterval)
	}
	return cond()
}

// waitForPaneContains polls a tmux pane until every needle appears, or
// timeout. Returns the last captured pane plus a bool indicating
// success. Unlike waitForContent (parity_test.go), this does NOT call
// t.Fatalf on miss: callers decide how to react. This matters for
// "may be silent abort" cases where the *absence* of a needle is the
// expected behavior.
func waitForPaneContains(session string, timeout time.Duration, needles ...string) (pane string, ok bool) {
	pollUntil(timeout, func() bool {
		out, _ := tmuxCommand("capture-pane", "-t", session, "-p").Output()
		pane = string(out)
		for _, n := range needles {
			if !strings.Contains(pane, n) {
				return false
			}
		}
		ok = true
		return true
	})
	return pane, ok
}

// settleAfter is a small fixed pause used after the polling condition
// fires to let the renderer flush trailing output. Most callers should
// NOT need this: it's only useful when the test asserts on additional
// state that may be written after the gate condition (e.g. tool output
// lines that follow the "✓" tick).
//
// Cap at 500ms; longer means a real bug.
func settleAfter() { time.Sleep(500 * time.Millisecond) }

// quietExit sends C-d to a tmux session and waits briefly for cleanup.
// Centralised so all parity tests use the same shutdown path.
func quietExit(t *testing.T, session string) {
	t.Helper()
	_ = tmuxCommand("send-keys", "-t", session, "C-d").Run()
	time.Sleep(150 * time.Millisecond)
}
