package host

import (
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// BenchmarkBackpressureHoldRelease is the latency of one hold and its release over an off-loop link, one identity and a hundred
// (the harness of the reader / teardown backlog, host_backpressure_baseline_test.go). `reply` ends when the peer has the pong of
// a ping sent after the release: the reader handled both frames and, with pace, the ending they started ran to its first real
// wait. `torn down` ends when the host is quiet: the ending ran to its end. Only the host's side differs between a tree with
// and without pace, so the same file measures both.
func BenchmarkBackpressureHoldRelease(b *testing.B) {
	spec := specOf(Notes)
	for _, ids := range []int{1, 100} {
		for _, torn := range []bool{false, true} {
			name := "reply"
			if torn {
				name = "torn down"
			}
			b.Run(strconv.Itoa(ids)+" identities, "+name, func(b *testing.B) {
				s := rawLinked(b, func(h FacetHost) error { _, err := serveNotes(h, nil); return err }, rawLinkedOptions{Parent: true})
				loop, ok := s.Server.(*chordHost).loop().(*Loop)
				if !ok {
					b.Fatal("not a *Loop")
				}
				round := func(origin int) {
					s.Peer.Send(&link.HoldMessage{
						Origin: link.Some(origin), Hold: 0, Type: spec.Name, Version: spec.Version, Fingerprint: spec.Fingerprint,
						Params: mustJSONPart(b, jsonObject("session", "S"+strconv.Itoa(origin%ids))), Address: serverWireAddress,
					})
					s.Peer.Send(&link.ReleaseMessage{Origin: link.Some(origin), Hold: 0})
					rhSync(b, s.Peer)
					if torn {
						loop.quiesce()
					}
					s.Peer.mu.Lock()
					s.Peer.received = nil
					s.Peer.mu.Unlock()
				}
				for origin := 1; origin <= 50; origin++ {
					round(origin)
				}
				loop.quiesce()
				b.ReportAllocs()
				origin := 51
				for b.Loop() {
					round(origin)
					origin++
				}
			})
		}
	}
}
