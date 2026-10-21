package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"testing"
)

// Measures a retained Context through framing, response correlation and JSON decoding on a real in-process socket. It excludes external process and terminal costs.
func BenchmarkRetainedContextHostCall(b *testing.B) {
	for _, size := range []int{0, 1024, 64 << 10} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			client, server := net.Pipe()
			connection, host := newConn(client), newConn(server)
			connection.start()
			host.start()
			text := strings.Repeat("x", size)
			payload, err := json.Marshal(map[string]string{"text": text})
			if err != nil {
				b.Fatal(err)
			}
			done := make(chan struct{})
			go func() {
				defer close(done)
				for call := range host.incoming {
					if call.Type == msgCall {
						if err := host.send(envelope{Type: msgCallResult, ID: call.ID, CallResult: &callResultMsg{Result: payload}}); err != nil {
							return
						}
					}
				}
			}()
			defer func() { _ = client.Close(); _ = server.Close(); <-done; <-connection.done; <-host.done }()
			parent := connection.armParent("origin", b.Context(), b.Context())
			ctx := Context{ext: &Extension{conn: connection}, requestID: "origin", parent: parent, ctx: context.Background()}
			if err := connection.respond("origin", nil, nil); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				if got, err := ctx.GetEditorText(); err != nil || got != text {
					b.Fatal("retained host value lost")
				}
			}
		})
	}
}
