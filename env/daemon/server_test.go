package daemon

// pi: packages/env/daemon/src/main.rs

// Ports the protocol rules of packages/env/docs/protocol.md that packages/env/test exercises only through the client:
// framing errors, request JSON errors, hello, cancel modes, liveness, handle limits and write ordering.

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// peer is a client of one in-process daemon over pipes.
type peer struct {
	t      *testing.T
	input  *io.PipeWriter
	frames chan *Frame
	done   chan error
	mu     sync.Mutex
	sync   string
}

func startServe(t *testing.T, options Options) *peer {
	t.Helper()
	inputReader, inputWriter := io.Pipe()
	outputReader, outputWriter := io.Pipe()
	p := &peer{t: t, input: inputWriter, frames: make(chan *Frame, 1024), done: make(chan error, 1)}
	go func() {
		p.done <- Serve(inputReader, outputWriter, "tok", options)
		_ = outputWriter.Close()
	}()
	p.attach(outputReader)
	t.Cleanup(func() {
		_ = inputWriter.Close()
		select {
		case <-p.done:
		case <-time.After(10 * time.Second):
			t.Error("the daemon did not stop after its input ended")
		}
	})
	return p
}

// attach reads the sync line from the daemon's output and then its frames into p.frames until the output ends.
func (p *peer) attach(outputReader io.Reader) {
	p.t.Helper()
	reader := bufio.NewReader(outputReader)
	line, err := reader.ReadString('\n')
	if err != nil {
		p.t.Fatalf("sync line: %v", err)
	}
	p.sync = line
	go func() {
		defer close(p.frames)
		for {
			frame, err := ReadFrame(reader)
			if err != nil || frame == nil {
				return
			}
			p.frames <- frame
		}
	}()
}

func (p *peer) send(kind byte, id uint32, json any, payload []byte) {
	p.t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := WriteFrame(p.input, &Frame{Kind: kind, ID: id, JSON: json, Payload: payload}); err != nil {
		p.t.Fatal(err)
	}
}

func (p *peer) request(id uint32, json Object, payload []byte) {
	p.send(FrameRequest, id, json, payload)
}

// next returns the next frame that is not a ping.
func (p *peer) next() *Frame {
	p.t.Helper()
	for {
		select {
		case frame, ok := <-p.frames:
			if !ok {
				p.t.Fatal("the daemon closed its output")
			}
			if frame.Kind != FramePing {
				return frame
			}
		case <-time.After(20 * time.Second):
			p.t.Fatal("no frame from the daemon")
		}
	}
}

// reply returns the result or error of request id, skipping its events.
func (p *peer) reply(id uint32) *Frame {
	p.t.Helper()
	for {
		frame := p.next()
		if frame.ID == id && (frame.Kind == FrameResult || frame.Kind == FrameError) {
			return frame
		}
	}
}

func object(t *testing.T, frame *Frame) Object {
	t.Helper()
	value, ok := frame.JSON.(Object)
	if !ok {
		t.Fatalf("frame JSON %v is not an object", frame.JSON)
	}
	return value
}

func TestServeStartsWithTheSyncLineAndAnswersHello(t *testing.T) {
	p := startServe(t, Options{Version: "9.9.9"})
	if p.sync != "PI-ENV tok\n" {
		t.Fatalf("sync line %q", p.sync)
	}
	p.request(1, Object{"op": "hello", "protocol": float64(1)}, nil)
	reply := p.reply(1)
	if reply.Kind != FrameResult {
		t.Fatalf("hello: %+v", reply)
	}
	hello := object(t, reply)
	if hello["protocol"] != float64(1) || hello["version"] != "9.9.9" || hello["pid"] != float64(os.Getpid()) {
		t.Fatalf("hello %v", hello)
	}
	osNames := map[string]string{"darwin": "macos"}
	wantOS := runtime.GOOS
	if mapped, ok := osNames[wantOS]; ok {
		wantOS = mapped
	}
	arches := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}
	wantArch := runtime.GOARCH
	if mapped, ok := arches[wantArch]; ok {
		wantArch = mapped
	}
	separator := "/"
	if runtime.GOOS == "windows" {
		separator = `\`
	}
	if hello["os"] != wantOS || hello["arch"] != wantArch || hello["separator"] != separator || hello["tmpdir"] != tmpdir() || hello["cwd"] == "" {
		t.Fatalf("hello %v: os %s arch %s", hello, wantOS, wantArch)
	}
	if _, ok := hello["driveCwds"].(Object); !ok {
		t.Fatalf("driveCwds %v is not an object", hello["driveCwds"])
	}
}

func TestServeAnswersARequestThatDoesNotParseAndAnUnknownOperationWithEINVAL(t *testing.T) {
	p := startServe(t, Options{})
	// A request whose JSON does not parse: valid framing, JSON length 3.
	var raw bytes.Buffer
	raw.Write([]byte{0, 0, 0, 9 + 3, FrameRequest, 0, 0, 0, 7, 0, 0, 0, 3, '{', '{', '{'})
	p.mu.Lock()
	_, err := p.input.Write(raw.Bytes())
	p.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if reply := p.reply(7); reply.Kind != FrameError || object(t, reply)["code"] != "EINVAL" {
		t.Fatalf("unparseable request: %+v", reply)
	}
	p.request(8, Object{"op": "frobnicate"}, nil)
	if reply := p.reply(8); reply.Kind != FrameError || object(t, reply)["code"] != "EINVAL" || !strings.Contains(object(t, reply)["message"].(string), "frobnicate") {
		t.Fatalf("unknown operation: %+v", reply)
	}
	p.request(9, Object{"op": "lstat"}, nil)
	if reply := p.reply(9); reply.Kind != FrameError || object(t, reply)["code"] != "EINVAL" {
		t.Fatalf("lstat without a path: %+v", reply)
	}
}

func TestServeEndsTheConnectionOnAFramingError(t *testing.T) {
	inputReader, inputWriter := io.Pipe()
	outputReader, outputWriter := io.Pipe()
	go func() { _, _ = io.Copy(io.Discard, outputReader) }()
	done := make(chan error, 1)
	go func() { done <- Serve(inputReader, outputWriter, "tok", Options{}) }()
	// A frame length below the 9 bytes of the fixed header.
	if _, err := inputWriter.Write([]byte{0, 0, 0, 3, 1, 0, 0}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a framing error ended the connection without an error")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the daemon kept serving after a framing error")
	}
	_ = outputWriter.Close()
}

func TestPreadClampsToTheLargestPayloadAndWriteChunksKeepOrder(t *testing.T) {
	p := startServe(t, Options{})
	directory := t.TempDir()
	big := filepath.Join(directory, "big.bin")
	content := make([]byte, MaxPayload+4096)
	for index := range content {
		content[index] = byte(index * 13)
	}
	p.request(1, Object{"op": "write", "path": big}, content[:1])
	if reply := p.reply(1); reply.Kind != FrameResult {
		t.Fatalf("write: %+v", reply)
	}
	if err := os.WriteFile(big, content, 0o600); err != nil {
		t.Fatal(err)
	}
	p.request(2, Object{"op": "open", "path": big, "noFollow": false}, nil)
	opened := object(t, p.reply(2))
	handle := opened["handle"]
	p.request(3, Object{"op": "pread", "handle": handle, "offset": float64(0), "length": float64(len(content))}, nil)
	read := p.reply(3)
	if len(read.Payload) != MaxPayload || !bytes.Equal(read.Payload, content[:MaxPayload]) {
		t.Fatalf("pread returned %d bytes, want the %d of one frame", len(read.Payload), MaxPayload)
	}

	// Chunk writes to one handle run in arrival order, though they are sent without waiting.
	target := filepath.Join(directory, "chunks.txt")
	p.request(4, Object{"op": "write", "path": target, "keep": true}, []byte("0"))
	kept := object(t, p.reply(4))["handle"]
	var want bytes.Buffer
	want.WriteString("0")
	for index := uint32(1); index <= 200; index++ {
		piece := strconv.Itoa(int(index)) + ","
		want.WriteString(piece)
		p.request(100+index, Object{"op": "writeChunk", "handle": kept}, []byte(piece))
	}
	for index := uint32(1); index <= 200; index++ {
		if reply := p.reply(100 + index); reply.Kind != FrameResult {
			t.Fatalf("chunk %d: %+v", index, reply)
		}
	}
	p.request(5, Object{"op": "close", "handle": kept}, nil)
	p.reply(5)
	if got, err := os.ReadFile(target); err != nil || !bytes.Equal(got, want.Bytes()) {
		t.Fatalf("chunks written out of order or lost: %v (%d bytes, want %d)", err, len(got), want.Len())
	}
}

func TestAFailedChunkPoisonsTheWriteHandleAndHandlesAreLimited(t *testing.T) {
	s := &server{
		out: newOutput(), files: map[uint64]*fileHandle{}, dirs: map[uint64]*dirHandle{}, nextHandle: 1,
		controls: map[uint32]*control{},
	}
	// A handle opened read-only cannot take a write: the first chunk fails, and later chunks fail without writing.
	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() }) // Windows cannot remove the temp directory under an open handle
	id, failure := s.insertFile(file, path)
	if failure != nil {
		t.Fatal(failure)
	}
	request := Object{"op": "writeChunk", "handle": float64(id)}
	// Node (libuv) names a write to a read-only handle EBADF on POSIX and EPERM on Windows, where the kernel answers
	// ERROR_ACCESS_DENIED (internal/nodeerrno); the daemon reports what the platform's Node would.
	want := "EBADF"
	if runtime.GOOS == "windows" {
		want = "EPERM"
	}
	if _, _, failure := s.dispatch(1, request, []byte("x"), newControl()); failure == nil || failure.Code != want {
		t.Fatalf("first chunk: %+v, want %s", failure, want)
	}
	if _, _, failure := s.dispatch(2, request, []byte("y"), newControl()); failure == nil || failure.Code != "EBADF" || failure.Message != "an earlier write failed" {
		t.Fatalf("second chunk: %+v", failure)
	}
	if _, _, failure := s.dispatch(3, Object{"op": "writeChunk", "handle": float64(999)}, nil, newControl()); failure == nil || failure.Message != "unknown handle" {
		t.Fatalf("unknown handle: %+v", failure)
	}

	for len(s.files)+len(s.dirs) < maxHandles {
		if _, failure := s.insertFile(file, path); failure != nil {
			t.Fatal(failure)
		}
	}
	if _, failure := s.insertFile(file, path); failure == nil || failure.Code != "EMFILE" {
		t.Fatalf("handle %d: %+v, want EMFILE", maxHandles+1, failure)
	}
}
