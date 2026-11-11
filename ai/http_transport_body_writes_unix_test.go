//go:build !windows

package ai

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"syscall"
	"testing"
	"time"
)

// copyingConn hides idleTimeoutConn.ReadFrom, so net/http copies a request body through io.Copy's buffer as it did before.
type copyingConn struct{ net.Conn }

func cpuTime() time.Duration {
	var usage syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &usage)
	return time.Duration(usage.Utime.Nano() + usage.Stime.Nano())
}

// BenchmarkRequestBodyWrite sends the body of a long session's request (a few MB in segments of about 50 KB) over loopback HTTP to a server that discards it. cpu-ns/op is the process's CPU time per request, the sending side's and the server's together.
func BenchmarkRequestBodyWrite(b *testing.B) {
	sizes := make([]int, 0, 400)
	for range 90 {
		sizes = append(sizes, 51264, 300)
	}
	for name, wrap := range map[string]func(net.Conn, string) net.Conn{
		"vectored": func(conn net.Conn, network string) net.Conn { return newIdleTimeoutConn(conn, network) },
		"copy":     func(conn net.Conn, network string) net.Conn { return copyingConn{newIdleTimeoutConn(conn, network)} },
	} {
		b.Run(name, func(b *testing.B) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
			}))
			defer server.Close()
			transport := &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
				if err != nil {
					return nil, err
				}
				return wrap(conn, network), nil
			}}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport}
			body, whole := segmentsOf(sizes...)
			b.SetBytes(int64(len(whole)))
			b.ReportAllocs()
			start := cpuTime()
			b.ResetTimer()
			for range b.N {
				request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL, body.reader())
				if err != nil {
					b.Fatal(err)
				}
				body.prepare(request)
				response, err := client.Do(request)
				if err != nil {
					b.Fatal(err)
				}
				_, _ = io.Copy(io.Discard, response.Body)
				_ = response.Body.Close()
			}
			b.ReportMetric(float64(cpuTime()-start)/float64(b.N), "cpu-ns/op")
		})
	}
}
