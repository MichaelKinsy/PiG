package coding

import (
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
)

func BenchmarkSessionQueueReentrantPublication(b *testing.B) {
	for _, shape := range []string{"empty", "ordinary", "overflow"} {
		b.Run(shape, func(b *testing.B) {
			services, err := NewServices(ServicesOptions{CWD: b.TempDir(), AgentDir: b.TempDir()})
			if err != nil {
				b.Fatal(err)
			}
			session, err := NewSession(services, SessionOptions{Model: fakeModel(), NoSession: true, SkipBuiltinTools: true})
			if err != nil {
				b.Fatal(err)
			}
			done := make(chan struct{})
			go func() {
				defer close(done)
				for event := range session.Events() {
					AcknowledgeEvent(event)
				}
			}()
			defer func() { _ = session.Close(); <-done }()
			count := 0
			switch shape {
			case "ordinary":
				count = 1
			case "overflow":
				count = 2 * cap(session.rawEvents)
			}
			session.Subscribe(func(event agent.AgentEvent) {
				if _, ok := event.(agent.AgentStartEvent); ok {
					for range count {
						session.QueueFollowUp("queued input", nil)
					}
				}
			})
			b.ReportAllocs()
			for b.Loop() {
				session.emitOrderedEventSync(agent.AgentStartEvent{})
				if err := session.FlushEvents(b.Context()); err != nil {
					b.Fatal(err)
				}
				session.ClearQueue()
			}
		})
	}
}
