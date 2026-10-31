package http

import (
	"bytes"
	"io"
	nethttp "net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Benchmark of the HTTP component (slice 10 §7): a route's overhead per request against a plain net/http handler (keep-alive
// round trips), and `bytes` throughput for a 100 MiB file (`FileResponse`) against io.Copy of the open file. Ports
// packages/chord/test/next/bench/http.ts (Pi: median µs per request over bursts, MiB/s of one download; Go: the standard
// benchmark loop and b.SetBytes). Run by hand, not in CI:
//
//	go test ./chord/nextnode/http -run '^$' -bench . -benchtime 2000x

const benchFile = 100 * 1024 * 1024

// getAll GETs url over client reading the whole body; it returns its length.
func getAll(b *testing.B, client *nethttp.Client, url string) int64 {
	b.Helper()
	response, err := client.Get(url)
	if err != nil {
		b.Fatal(err)
	}
	length, err := io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if err != nil {
		b.Fatal(err)
	}
	return length
}

func benchServer(b *testing.B) (plain *httptest.Server, port int, file string) {
	b.Helper()
	file = filepath.Join(b.TempDir(), "big.bin")
	if err := os.WriteFile(file, bytes.Repeat([]byte{1}, benchFile), 0o600); err != nil {
		b.Fatal(err)
	}
	// A plain server: the same JSON body, the same file through io.Copy.
	plain = httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		if r.URL.Path == "/file" {
			opened, err := os.Open(file)
			if err != nil {
				w.WriteHeader(500)
				return
			}
			defer opened.Close()
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Length", strconv.Itoa(benchFile))
			_, _ = io.Copy(w, opened)
			return
		}
		body := `{"ok":true}`
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = io.WriteString(w, body)
	}))
	b.Cleanup(plain.Close)
	// The component with Chord's own listener: one plugin with the same route and the file.
	h, err := host.CreateHost(host.HostOptions{Principal: principalServer(), OnError: func(host.HostError) {}})
	if err != nil {
		b.Fatal(err)
	}
	component := CreateHttp(h, HttpOptions{Listen: &ListenAddress{Host: "127.0.0.1"}, OnError: func(HttpError) {}}, background)
	routes := contributing(HttpContribution{Routes: []HttpRouteEntry{
		RouteFunc("GET /json", func(HttpRequest, host.Ctx) (HttpResponse, error) {
			return jsonOf(map[string]any{"ok": true}), nil
		}),
		RouteFunc("GET /file", func(HttpRequest, host.Ctx) (HttpResponse, error) {
			return FileResponse(file, FileResponseOptions{})
		}),
	}})
	if err := h.Load([]host.HostFacet{{Name: "chord.http", Facet: component.Facet()}, {Name: "@bench/routes", Facet: routes}}, background); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		component.Close(background)
		_ = h.Close(background)
	})
	address, err := component.Listening(background)
	if err != nil {
		b.Fatal(err)
	}
	return plain, address.Port, file
}

func BenchmarkHTTPRequestRoundTrip(b *testing.B) {
	plain, port, _ := benchServer(b)
	for _, each := range []struct {
		name string
		url  string
	}{{"plain", plain.URL + "/json"}, {"route", "http://127.0.0.1:" + strconv.Itoa(port) + "/json"}} {
		b.Run(each.name, func(b *testing.B) {
			client := &nethttp.Client{Transport: &nethttp.Transport{MaxConnsPerHost: 1}}
			defer client.CloseIdleConnections()
			for i := 0; i < 200; i++ {
				getAll(b, client, each.url)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				getAll(b, client, each.url)
			}
		})
	}
}

func BenchmarkHTTPFileThroughput(b *testing.B) {
	plain, port, _ := benchServer(b)
	for _, each := range []struct {
		name string
		url  string
	}{{"pipeline", plain.URL + "/file"}, {"fileResponse", "http://127.0.0.1:" + strconv.Itoa(port) + "/file"}} {
		b.Run(each.name, func(b *testing.B) {
			client := &nethttp.Client{Transport: &nethttp.Transport{MaxConnsPerHost: 1}}
			defer client.CloseIdleConnections()
			getAll(b, client, each.url)
			b.SetBytes(benchFile)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if got := getAll(b, client, each.url); got != benchFile {
					b.Fatalf("read %d bytes", got)
				}
			}
		})
	}
}
