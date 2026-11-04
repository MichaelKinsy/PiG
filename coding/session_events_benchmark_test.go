package coding

import "testing"

// BenchmarkSessionSendLongTurnEvents measures one Send of a reply of 5,000 text deltas under each way a program can treat Events: drained by a reader that acknowledges, discarded with DiscardEvents, and never called.
func BenchmarkSessionSendLongTurnEvents(b *testing.B) {
	prov := longTurnProvider(5000)
	b.Setenv("PIG_HOME", b.TempDir())
	svcs, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: b.TempDir()})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(svcs.Close)
	for _, tc := range []struct {
		name  string
		setup func(*Session)
	}{
		{"drained", func(s *Session) {
			events := s.Events()
			go func() {
				for ev := range events {
					AcknowledgeEvent(ev)
				}
			}()
		}},
		{"discarded", func(s *Session) { s.DiscardEvents() }},
		{"unread", func(*Session) {}},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				b.StopTimer()
				sess, err := NewSession(svcs, SessionOptions{Model: fakeModelWithProvider(prov), NoSession: true, SkipBuiltinTools: true})
				if err != nil {
					b.Fatal(err)
				}
				tc.setup(sess)
				b.StartTimer()
				if _, err := sess.Send(b.Context(), "hi"); err != nil {
					b.Fatal(err)
				}
				b.StopTimer()
				_ = sess.Close()
				b.StartTimer()
			}
		})
	}
}
