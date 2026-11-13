package subprocess

import (
	"context"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// seqAcks numbers the frames of one kind that the host sends on one connection and records which of them the runtime acknowledged: NotifyTimerDefer (NotifyTimerDeferAck), and a NotifyInvalidate that asks for its acknowledgement (NotifyInvalidateAck).
type seqAcks struct {
	mu      sync.Mutex
	sent    uint64
	acked   uint64
	changed chan struct{}
}

// next numbers the frame the caller is about to send. The caller sends frames in the order it numbers them.
func (s *seqAcks) next() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent++
	return s.sent
}

// ack records the runtime's acknowledgement, whose arguments carry the frame's number as seq (TimerDeferAckArgs, InvalidateAckArgs).
func (s *seqAcks) ack(raw json.RawMessage) {
	var args struct {
		Seq uint64 `json:"seq"`
	}
	if json.Unmarshal(raw, &args) != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if args.Seq <= s.acked {
		return
	}
	s.acked = args.Seq
	if s.changed != nil {
		close(s.changed)
		s.changed = nil
	}
}

// await waits until every frame sent so far is acknowledged, or the connection closed, or ctx ended. A closed connection runs no callback, so it has nothing left to hold.
func (s *seqAcks) await(ctx context.Context, done <-chan struct{}) {
	s.awaitBy(ctx, done, nil)
}

// awaitBy is await with a deadline: it reports false when expired fires before the acknowledgement, the connection's end and ctx's.
func (s *seqAcks) awaitBy(ctx context.Context, done <-chan struct{}, expired <-chan time.Time) bool {
	for {
		s.mu.Lock()
		if s.acked >= s.sent {
			s.mu.Unlock()
			return true
		}
		if s.changed == nil {
			s.changed = make(chan struct{})
		}
		changed := s.changed
		s.mu.Unlock()
		select {
		case <-changed:
		case <-done:
			return true
		case <-ctx.Done():
			return true
		case <-expired:
			return false
		}
	}
}
