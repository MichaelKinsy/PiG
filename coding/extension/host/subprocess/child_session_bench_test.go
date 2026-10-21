package subprocess

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
)

// childStreamEvents returns the message_update events of one streamed assistant text of deltas deltas of 24 bytes, in the JSON shape the session's events have, followed by the message_end.
func childStreamEvents(deltas int) [][]byte {
	message := func(text string) string {
		return fmt.Sprintf(`{"role":"assistant","content":[{"type":"text","text":%q}],"api":"openai-completions","provider":"sentinel","model":"echo","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","timestamp":1}`, text)
	}
	events := make([][]byte, 0, deltas+2)
	events = append(events, []byte(`{"type":"message_start","message":`+message("")+`}`))
	var text strings.Builder
	for i := range deltas {
		delta := fmt.Sprintf("delta-%017d ", i)
		text.WriteString(delta)
		events = append(events, []byte(`{"type":"message_update","message":`+message(text.String())+`,"assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":`+fmt.Sprintf("%q", delta)+`,"partial":`+message(text.String())+`}}`))
	}
	return append(events, []byte(`{"type":"message_end","message":`+message(text.String())+`}`))
}

// BenchmarkChildSessionForward is the host cost of one streamed run of a child session: each op is one prompt whose events the session emits, and the extra metrics give the cost per event and the bytes the extension's connection receives.
func BenchmarkChildSessionForward(b *testing.B) {
	for _, deltas := range []int{16, 256, 4096} {
		b.Run(fmt.Sprintf("deltas=%d", deltas), func(b *testing.B) {
			hostEnd, peerEnd := loopbackPair(b)
			var received atomic.Int64
			go func() {
				buf := make([]byte, 64*1024)
				for {
					n, err := peerEnd.Read(buf)
					received.Add(int64(n))
					if err != nil {
						return
					}
				}
			}()
			b.Cleanup(func() { _ = peerEnd.Close() })
			conn := NewConn("bench", hostEnd)
			conn.Start(b.Context())
			b.Cleanup(func() { _ = conn.Close("bench done") })
			events := childStreamEvents(deltas)
			b.ReportAllocs()
			for b.Loop() {
				entry := &childSessionEntry{conn: conn, prefix: []byte(`{"id":"c1","seq":`)}
				for _, event := range events {
					entry.forward(event)
				}
			}
			b.ReportMetric(float64(received.Load())/float64(b.N), "wire-B/prompt")
		})
	}
}
