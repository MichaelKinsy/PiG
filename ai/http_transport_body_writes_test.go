package ai

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// segmentsOf returns a request body of segments of the given sizes, each filled with its own letter.
func segmentsOf(sizes ...int) (*requestBody, []byte) {
	body := &requestBody{}
	var whole []byte
	for i, size := range sizes {
		segment := bytes.Repeat([]byte{byte('a' + i%26)}, size)
		body.add(segment)
		whole = append(whole, segment...)
	}
	return body, whole
}

// A request body held as segments reaches the server unchanged through the transport's vectored writes: a segment larger than one batch is split across batches, a body smaller than a batch is one write, and a segment boundary may fall anywhere in a batch.
func TestRequestBodySegmentsReachTheServerThroughVectoredWrites(t *testing.T) {
	for name, sizes := range map[string][]int{
		"one small segment":          {100},
		"several segments":           {5000, 70000, 3, 51000},
		"one segment past the batch": {requestWriteBatch + 12345},
		"segments across batches":    {requestWriteBatch - 7, 20, requestWriteBatch, 1},
	} {
		t.Run(name, func(t *testing.T) {
			body, whole := segmentsOf(sizes...)
			var got [sha256.Size]byte
			var length int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				got, length = sha256.Sum256(data), len(data)
			}))
			defer server.Close()
			transport := &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
				if err != nil {
					return nil, err
				}
				return newIdleTimeoutConn(conn, network), nil
			}}
			defer transport.CloseIdleConnections()
			request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL, body.reader())
			if err != nil {
				t.Fatal(err)
			}
			body.prepare(request)
			response, err := (&http.Client{Transport: transport}).Do(request)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if length != len(whole) || got != sha256.Sum256(whole) {
				t.Fatalf("the server received %d bytes, want %d with the same content", length, len(whole))
			}
		})
	}
}

// ReadFrom takes a body of a known length from its WriteTo, consumes exactly that length, and copies any other reader as io.Copy does.
func TestIdleTimeoutConnReadFrom(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	received := make(chan []byte, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			received <- nil
			return
		}
		defer conn.Close()
		data, _ := io.ReadAll(conn)
		received <- data
	}()
	dialed, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	conn := newIdleTimeoutConn(dialed, "tcp")

	body, whole := segmentsOf(40000, 3*requestWriteBatch/2, 9)
	limited := &io.LimitedReader{R: io.NopCloser(body.reader()), N: int64(len(whole))}
	if n, err := conn.ReadFrom(limited); err != nil || n != int64(len(whole)) || limited.N != 0 {
		t.Fatalf("ReadFrom of a segmented body = %d, %v, N=%d, want %d bytes", n, err, limited.N, len(whole))
	}
	plain := strings.Repeat("p", 70000)
	if n, err := conn.ReadFrom(strings.NewReader(plain)); err != nil || n != int64(len(plain)) {
		t.Fatalf("ReadFrom of a plain reader = %d, %v", n, err)
	}
	conn.Close()
	if data := <-received; !bytes.Equal(data, append(whole, plain...)) {
		t.Fatalf("the peer received %d bytes, want %d", len(data), len(whole)+len(plain))
	}
}

// A body longer than the length net/http declared is not sent past it.
func TestIdleTimeoutConnReadFromStopsAtTheDeclaredLength(t *testing.T) {
	server, client := net.Pipe()
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		_, _ = io.Copy(io.Discard, server)
	}()
	defer func() {
		client.Close()
		<-drained
	}()
	conn := newIdleTimeoutConn(client, "pipe")
	body, whole := segmentsOf(100, 100)
	limited := &io.LimitedReader{R: io.NopCloser(body.reader()), N: int64(len(whole) - 50)}
	if n, err := conn.ReadFrom(limited); err == nil || n != 0 {
		t.Fatalf("ReadFrom past the declared length = %d, %v, want an error and no bytes of the last segment", n, err)
	}
}
