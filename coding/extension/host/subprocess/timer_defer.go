package subprocess

import (
	"context"
	"sync"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// timerDeferState numbers the NotifyTimerDefer frames of one connection and records which of them the runtime acknowledged (NotifyTimerDeferAck).
type timerDeferState struct {
	mu      sync.Mutex
	sent    uint64
	acked   uint64
	changed chan struct{}
}

// next numbers the frame the caller is about to send. The caller sends frames in the order it numbers them.
func (s *timerDeferState) next() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent++
	return s.sent
}

// ack records the runtime's NotifyTimerDeferAck.
func (s *timerDeferState) ack(raw json.RawMessage) {
	var args TimerDeferAckArgs
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
func (s *timerDeferState) await(ctx context.Context, done <-chan struct{}) {
	for {
		s.mu.Lock()
		if s.acked >= s.sent {
			s.mu.Unlock()
			return
		}
		if s.changed == nil {
			s.changed = make(chan struct{})
		}
		changed := s.changed
		s.mu.Unlock()
		select {
		case <-changed:
		case <-done:
			return
		case <-ctx.Done():
			return
		}
	}
}
