package coding

import (
	"context"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// failingPersistWarmProvider makes the Session file unwritable once the real
// warmer has captured the request, so the assistant write fails and reaches
// Agent.handleRunFailure while a status reader is running.
type failingPersistWarmProvider struct {
	warmingProvider
	failPersistence func()
	started         chan struct{}
	readerStarted   chan struct{}
	once            sync.Once
}

func (p *failingPersistWarmProvider) Stream(ctx context.Context, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	p.failPersistence()
	p.once.Do(func() {
		close(p.started)
		<-p.readerStarted
	})
	return p.warmingProvider.Stream(ctx, transcript, options)
}

// Session.CacheWarmingStatus reads the agent transcript through the warmer's
// current-context check. A run failure's append must not race it. Run under -race.
func TestCacheWarmingStatusSafeDuringPersistenceFailure(t *testing.T) {
	t.Setenv("PI_CACHE_RETENTION", "short")
	provider := &failingPersistWarmProvider{started: make(chan struct{}), readerStarted: make(chan struct{})}
	sess, err := NewSession(warmingServices(t, "idle"), SessionOptions{NoSession: true, Model: warmingModel(provider)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	go func() {
		for range sess.Events() {
		}
	}()
	badPath := t.TempDir()
	provider.failPersistence = func() { sess.Inner().SetPath(badPath) }
	stop := make(chan struct{})
	var readers sync.WaitGroup
	readers.Go(func() {
		<-provider.started
		_ = sess.CacheWarmingStatus()
		close(provider.readerStarted)
		for {
			select {
			case <-stop:
				return
			default:
				_ = sess.CacheWarmingStatus()
			}
		}
	})
	for range 16 {
		if _, err := sess.Send(context.Background(), "go"); err == nil {
			t.Error("Send accepted an unwritable Session file")
		}
	}
	close(stop)
	readers.Wait()
}
