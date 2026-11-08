package sdk

import (
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// serveEventFrames runs an extension with one event handler on an in-process socket and returns a function that writes one prebuilt
// request frame and waits for the response: the SDK's read, decode, dispatch and reply, without the host's encode or process spawn.
func serveEventFrames(tb testing.TB, frame []byte) (call func(), stop func()) {
	tb.Helper()
	client, server := net.Pipe()
	connection, host := newConn(client), newConn(server)
	ext := New("bench")
	// Registered before the connection is set: with a connection OnEvent makes the host call event.subscribe, which no host answers here.
	ext.OnEvent("before_provider_request", func(Context, map[string]any) (any, error) { return nil, nil })
	ext.conn = connection
	connection.start()
	host.start()
	loopDone := make(chan error, 1)
	go func() { loopDone <- ext.loop() }()
	call = func() {
		if err := host.writeFrame(frame); err != nil {
			tb.Fatal(err)
		}
		for env := range host.incoming {
			if env.Type == msgResponse {
				if env.Response != nil && env.Response.Error != nil {
					tb.Fatalf("event failed: %+v", env.Response.Error)
				}
				return
			}
		}
		tb.Fatal("connection ended before the response")
	}
	return call, func() {
		_ = host.send(envelope{Type: msgShutdown, Shutdown: &shutdownMsg{Reason: "done"}})
		<-loopDone
		_ = client.Close()
		_ = server.Close()
	}
}

func eventFrame(size int) []byte {
	text := strings.Repeat("The quick brown fox \"jumps\"\n\tover the lazy dög — 日本語, c:\\dir\\file; ", 64)[:4096]
	quoted := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`).Replace(text)
	image := strings.Repeat("iVBORw0KGgoAAAANSUhEUgAAALQAAAC0CAIAAACyr5FlAAAAA3NCSVQICAjb4U/gAAAgAElEQVR4nOy9", 1300)[:97<<10]
	var b strings.Builder
	b.WriteString(`{"type":"request","id":"req-1","request":{"method":"event","event":"before_provider_request","handler_id":1,"args":`)
	b.WriteString(`{"type":"before_provider_request","payload":{"model":"pf-1","stream":true,"messages":[`)
	for i := 0; b.Len() < size; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		fmt.Fprintf(&b, `{"role":%q,"content":[{"type":"text","text":"%s"}],"n":%d,"ok":true,"x":null}`, role, quoted, i)
		if i%128 == 127 {
			fmt.Fprintf(&b, `,{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,%s"}}]}`, image)
		}
	}
	b.WriteString(`]},"extra":[1,2.5,-3e2]}}}`)
	return []byte(b.String())
}

// BenchmarkEventFrame is the LangSmith workload's shape: before_provider_request frames of 1 KiB to 43 MiB.
func BenchmarkEventFrame(b *testing.B) {
	for _, size := range []int{4 << 10, 64 << 10, 1 << 20, 16 << 20, 43 << 20} {
		name := fmt.Sprintf("%dKiB", size>>10)
		if size >= 1<<20 {
			name = fmt.Sprintf("%dMiB", size>>20)
		}
		frame := eventFrame(size)
		if !json.Valid(frame) {
			b.Fatal("bench frame is not valid JSON")
		}
		b.Run(name, func(b *testing.B) {
			call, stop := serveEventFrames(b, frame)
			defer stop()
			b.SetBytes(int64(len(frame)))
			b.ReportAllocs()
			for b.Loop() {
				call()
			}
		})
	}
}
