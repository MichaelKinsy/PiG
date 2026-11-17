package link

import "testing"

// The deliver path of a link end, with its allocations: a message that goes straight to the listener (every message of a
// link once its listener is set), and a burst queued ahead of the listener and then handed over (a child's link queues
// until its start message).
func BenchmarkLinkEndDeliver(b *testing.B) {
	message := []byte("message")
	b.Run("to-the-listener", func(b *testing.B) {
		end := &LinkEnd{}
		handed := 0
		end.OnMessage(func([]byte) { handed++ })
		b.ReportAllocs()
		for b.Loop() {
			end.deliver(message)
		}
		if handed != b.N {
			b.Fatalf("handed over %d messages, want %d", handed, b.N)
		}
	})
	b.Run("queued-64-then-handed-over", func(b *testing.B) {
		const burst = 64
		handed := 0
		listener := func([]byte) { handed++ }
		b.ReportAllocs()
		for b.Loop() {
			end := &LinkEnd{}
			for range burst {
				end.deliver(message)
			}
			end.OnMessage(listener)
		}
		if handed != b.N*burst {
			b.Fatalf("handed over %d messages, want %d", handed, b.N*burst)
		}
	})
}
