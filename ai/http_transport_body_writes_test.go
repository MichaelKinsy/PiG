package ai

import (
	"bufio"
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
			_ = response.Body.Close()
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
	defer func() { _ = listener.Close() }()
	received := make(chan []byte, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			received <- nil
			return
		}
		defer func() { _ = conn.Close() }()
		data, _ := io.ReadAll(conn)
		received <- data
	}()
	dialed, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	conn := newIdleTimeoutConn(dialed, "tcp")

	body, whole := segmentsOf(40000, 3*requestWriteBatch/2, 9)
	limited := &io.LimitedReader{R: body.reader(), N: int64(len(whole))}
	if n, err := conn.ReadFrom(limited); err != nil || n != int64(len(whole)) || limited.N != 0 {
		t.Fatalf("ReadFrom of a segmented body = %d, %v, N=%d, want %d bytes", n, err, limited.N, len(whole))
	}
	plain := strings.Repeat("p", 70000)
	if n, err := conn.ReadFrom(strings.NewReader(plain)); err != nil || n != int64(len(plain)) {
		t.Fatalf("ReadFrom of a plain reader = %d, %v", n, err)
	}
	_ = conn.Close()
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
		_ = client.Close()
		<-drained
	}()
	conn := newIdleTimeoutConn(client, "pipe")
	body, whole := segmentsOf(100, 100)
	limited := &io.LimitedReader{R: body.reader(), N: int64(len(whole) - 50)}
	if n, err := conn.ReadFrom(limited); err == nil || n != 0 {
		t.Fatalf("ReadFrom past the declared length = %d, %v, want an error and no bytes of the last segment", n, err)
	}
}

// reusingBody is a request body whose WriteTo hands its writer one buffer again and again, as bufio.Reader and os.File do: the writer may
// not keep what it is given.
type reusingBody struct {
	data []byte
	read int
	buf  [4096]byte
}

func (b *reusingBody) Read(p []byte) (int, error) {
	if b.read == len(b.data) {
		return 0, io.EOF
	}
	n := copy(p, b.data[b.read:])
	b.read += n
	return n, nil
}

func (b *reusingBody) WriteTo(w io.Writer) (int64, error) {
	var total int64
	for b.read < len(b.data) {
		n := copy(b.buf[:], b.data[b.read:])
		b.read += n
		written, err := w.Write(b.buf[:n])
		total += int64(written)
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// A body that is not a requestBody is copied as io.Copy copies it, so a reader that reuses its buffer between writes arrives intact: bufio.Reader, an
// io.MultiReader over readers without WriteTo, and a reader of its own.
func TestRequestBodiesOtherThanSegmentsReachTheServerUnchanged(t *testing.T) {
	data := make([]byte, 307220)
	for i := range data {
		data[i] = byte(i*7 + i/251)
	}
	bodies := map[string]func() io.Reader{
		"bufio.Reader": func() io.Reader { return bufio.NewReaderSize(bytes.NewReader(data), 4096) },
		"MultiReader": func() io.Reader {
			return io.MultiReader(iotestOneByteLess(data[:1000]), iotestOneByteLess(data[1000:]))
		},
		"WriteTo that reuses": func() io.Reader { return &reusingBody{data: data} },
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			var got [sha256.Size]byte
			var length int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				received, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				got, length = sha256.Sum256(received), len(received)
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
			request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL, io.NopCloser(body()))
			if err != nil {
				t.Fatal(err)
			}
			request.ContentLength = int64(len(data))
			response, err := (&http.Client{Transport: transport}).Do(request)
			if err != nil {
				t.Fatal(err)
			}
			_ = response.Body.Close()
			if length != len(data) || got != sha256.Sum256(data) {
				t.Fatalf("the server received %d bytes, want %d with the same content", length, len(data))
			}
		})
	}
}

// iotestOneByteLess is a reader without WriteTo that fills at most len(p)-1 bytes, so io.Copy's loop reuses its buffer.
type oneByteLess struct{ r *bytes.Reader }

func iotestOneByteLess(data []byte) io.Reader { return oneByteLess{bytes.NewReader(data)} }

func (o oneByteLess) Read(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:len(p)-1]
	}
	return o.r.Read(p)
}
