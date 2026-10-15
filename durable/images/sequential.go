package images

import (
	"context"
	"sync"

	"github.com/MichaelKinsy/PiG/durable/tools"
)

// sequential prepares through inner one call at a time in call order, as Pi's prepare runs its synchronous work on the one
// calling thread before the next call can start.
type sequential struct {
	inner tools.ImageProcessor

	mu sync.Mutex
	// last is closed once the latest call has finished, or given up, and every call before it has finished.
	last chan struct{}
}

func newSequential(inner tools.ImageProcessor) *sequential { return &sequential{inner: inner} }

// Prepare waits for every earlier call to finish, then prepares on the caller's goroutine. A caller whose context is
// cancelled while it waits returns the context's error without preparing, and the calls after it still wait for those
// before it.
func (s *sequential) Prepare(ctx context.Context, data []byte, mimeType string, limits tools.ImageLimits) (*tools.PreparedImage, error) {
	turn := make(chan struct{})
	s.mu.Lock()
	prev := s.last
	s.last = turn
	s.mu.Unlock()
	if prev != nil {
		select {
		case <-prev:
		case <-ctx.Done():
			go func() {
				<-prev
				close(turn)
			}()
			return nil, ctx.Err()
		}
	}
	defer close(turn)
	return s.inner.Prepare(ctx, data, mimeType, limits)
}
