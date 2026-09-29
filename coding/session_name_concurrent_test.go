package coding

import (
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi agent-session.ts:3559-3563 appends, reads, and publishes one name with no await between them, so every setSessionName call publishes the name it appended. Concurrent Go callers must each publish their own name to subscribers, the wire, and extensions.
func TestSessionNameConcurrentCallersPublishOwnName(t *testing.T) {
	const workers, perWorker = 8, 64
	var mu sync.Mutex
	var extensionNames []string
	h := newRecoveryHarness(t, harnessOptions{extension: extension.Extension{Handlers: map[string][]extension.HandlerFn{
		"session_info_changed": {func(args ...any) (any, error) {
			mu.Lock()
			defer mu.Unlock()
			extensionNames = append(extensionNames, args[0].(extension.SessionInfoChangedEvent).Name)
			return nil, nil
		}},
	}}})
	s := h.session
	var subscriberNames []string
	s.Subscribe(func(event agent.AgentEvent) {
		if info, ok := event.(agent.SessionInfoChangedEvent); ok {
			mu.Lock()
			defer mu.Unlock()
			subscriberNames = append(subscriberNames, info.Name)
		}
	})
	want := make([]string, 0, workers*perWorker)
	for w := range workers {
		for i := range perWorker {
			want = append(want, fmt.Sprintf("worker-%d-name-%d", w, i))
		}
	}
	var wg sync.WaitGroup
	errs := make(chan error, workers*perWorker)
	for w := range workers {
		names := want[w*perWorker : (w+1)*perWorker]
		wg.Go(func() {
			for _, name := range names {
				if err := s.SetSessionName(name); err != nil {
					errs <- err
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if err := s.FlushEvents(t.Context()); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	var wireNames []string
	for _, event := range h.events {
		if info, ok := event.(agent.SessionInfoChangedEvent); ok {
			wireNames = append(wireNames, info.Name)
		}
	}
	h.mu.Unlock()
	slices.Sort(want)
	mu.Lock()
	defer mu.Unlock()
	for label, got := range map[string][]string{"subscriber": subscriberNames, "wire": wireNames, "extension": extensionNames} {
		got = slices.Clone(got)
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Errorf("%s published %d names (%d unique), want each of the %d submitted names once", label, len(got), len(slices.Compact(got)), len(want))
		}
	}
}
