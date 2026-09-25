package shared

import (
	"testing"
	"testing/synctest"
	"time"
)

// rpc.ts refreshes lastFrameAt for every kind and closes only when silence is strictly greater than deadMs. Virtual time proves both boundaries without scheduler-dependent sleeps.
func TestRpcLivenessAnyFrameAndStrictBoundary(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		connection := newRecordedConnection()
		deadMs := float64(90)
		peer := CreatePeer(connection, PeerOptions{DeadMs: &deadMs})
		defer func() { _ = peer.Close(); peer.Wait() }()
		time.Sleep(60 * time.Millisecond)
		synctest.Wait()
		connection.deliver(`{"kind":"event","service":"lane","payload":null}`)
		time.Sleep(90 * time.Millisecond)
		synctest.Wait()
		connection.mu.Lock()
		closed := connection.closed
		connection.mu.Unlock()
		if closed {
			t.Fatal("ordinary event did not refresh liveness, or equal silence was treated as dead")
		}
		time.Sleep(30 * time.Millisecond)
		synctest.Wait()
		connection.mu.Lock()
		closed = connection.closed
		connection.mu.Unlock()
		if !closed {
			t.Fatal("peer survived silence greater than deadMs")
		}
	})
}

func TestRpcLivenessDisabled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		connection := newRecordedConnection()
		zero := float64(0)
		peer := CreatePeer(connection, PeerOptions{DeadMs: &zero})
		defer func() { _ = peer.Close(); peer.Wait() }()
		time.Sleep(time.Hour)
		synctest.Wait()
		connection.mu.Lock()
		closed := connection.closed
		connection.mu.Unlock()
		if closed || len(connection.sent) != 0 {
			t.Fatal("disabled liveness sent a ping or closed the peer")
		}
	})
}
