package subprocess

import (
	"io"
	"net"
	"testing"
)

type discardAdmitWriter struct{ io.Writer }

func (discardAdmitWriter) Close() error { return nil }

// A process that was killed but not yet reaped still takes control lines until the kernel closes its stdin, so neither the exit channel nor the noop probe refuses a claim on it. A member connection the host saw close unexpectedly marks the share dead, and claim must refuse it while the probe still succeeds.
func TestRetentionClaimRefusesShareWithClosedMemberConnection(t *testing.T) {
	t.Parallel()
	r := NewRuntimeRetention()
	share := newProcessShare("node:group", nil, make(chan struct{}), discardAdmitWriter{io.Discard}, nil)
	r.register(share)
	share.dead.Store(true)
	if got := r.claim("node:group"); got != nil {
		t.Fatal("claim handed out a share whose member connection closed")
	}
	share.dead.Store(false)
	if got := r.claim("node:group"); got != share {
		t.Fatal("claim refused a live share")
	}
}

// Every connection the host did not close withdraws its process from retention, packed or isolated: the isolated Node factory process is claimed by name and entry on the next reload exactly as a packed cell is claimed by group, and it too still takes control lines until it is reaped.
func TestUnexpectedConnectionCloseWithdrawsItsProcessFromRetention(t *testing.T) {
	t.Parallel()
	for _, packed := range []bool{false, true} {
		name := "isolated"
		if packed {
			name = "packed"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			hostEnd, peerEnd := net.Pipe()
			conn := NewConn("member", hostEnd)
			conn.Start(t.Context())
			h := NewHost(t.TempDir())
			key := "node-extension:member\x00entry"
			me := &managedExt{config: ExtConfig{Name: "member"}, host: h}
			share := newProcessShare(key, me, make(chan struct{}), discardAdmitWriter{io.Discard}, nil)
			if packed {
				me.packedCellKey = "cell"
				me.packedProcess = &packedProcessState{key: "cell", share: share}
			} else {
				me.share = share
			}
			withConn(me, conn)
			h.exts["member"] = me
			h.retention().register(share)
			_ = peerEnd.Close()
			h.handleIncoming(me, conn)
			if got := h.retention().claim(key); got != nil {
				t.Fatal("reload claimed a process whose connection closed without the host closing it")
			}
		})
	}
}

// A connection the host closes itself, as a reload closes a retiring generation, leaves the process claimable.
func TestHostClosedConnectionKeepsItsProcessRetained(t *testing.T) {
	t.Parallel()
	hostEnd, peerEnd := net.Pipe()
	defer func() { _ = peerEnd.Close() }()
	conn := NewConn("member", hostEnd)
	conn.Start(t.Context())
	h := NewHost(t.TempDir())
	key := "node-extension:member\x00entry"
	me := &managedExt{config: ExtConfig{Name: "member"}, host: h}
	share := newProcessShare(key, me, make(chan struct{}), discardAdmitWriter{io.Discard}, nil)
	me.share = share
	withConn(me, conn)
	h.retention().register(share)
	me.shuttingDown.Store(true)
	_ = conn.Close("retire")
	h.handleIncoming(me, conn)
	if got := h.retention().claim(key); got != share {
		t.Fatal("a generation the host retired withdrew its process from retention")
	}
}
