//go:build linux || darwin

package daemon

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// goroutineGrace bounds how long a goroutine may take to return after its owner ended.
const goroutineGrace = 10 * time.Second

// Every goroutine a connection starts ends with the connection: the frame reader, the worker pool, the writer, the
// pinger, windowed-output deliverers, exec kill retries, terminal readers and writers, watchers and the ping pump of this
// test's own peer. The workload touches each kind, then ends the connection while a terminal, a watch and a file handle
// are still open, and the goroutines Serve started must all be gone once Serve returned.
func TestServeLeavesNoGoroutineBehindWhenItsConnectionEnds(t *testing.T) {
	baseline := testenv.BaselineGoroutines()
	inputReader, inputWriter := io.Pipe()
	outputReader, outputWriter := io.Pipe()
	served := make(chan error, 1)
	go func() {
		served <- Serve(inputReader, outputWriter, "tok", Options{PingInterval: 20 * time.Millisecond})
		_ = outputWriter.Close()
	}()
	p := &peer{t: t, input: inputWriter, frames: make(chan *Frame, 1024), done: served}
	p.attach(outputReader)

	cwd := t.TempDir()
	p.request(1, Object{"op": "exec", "cwd": cwd, "command": "echo plain"}, nil)
	if reply := p.reply(1); reply.Kind != FrameResult {
		t.Fatalf("exec: %+v", reply)
	}
	window := Object{"maxBytes": float64(1 << 20), "maxLines": float64(1 << 20), "minIntervalMs": float64(1), "bytesPerSecond": float64(1 << 30)}
	p.request(2, Object{"op": "exec", "cwd": cwd, "command": "echo windowed", "window": window}, nil)
	if reply := p.reply(2); reply.Kind != FrameResult {
		t.Fatalf("windowed exec: %+v", reply)
	}
	p.request(3, Object{"op": "exec", "cwd": cwd, "argv": []any{"sleep", "30"}}, nil)
	p.send(FrameCancel, 3, Object{}, nil)
	if reply := p.reply(3); reply.Kind != FrameError {
		t.Fatalf("cancelled exec: %+v", reply)
	}
	p.request(4, Object{"op": "exec", "cwd": cwd, "command": "sleep 30", "window": window}, nil)
	p.send(FrameCancel, 4, Object{"mode": "kill"}, nil)
	if reply := p.reply(4); reply.Kind != FrameResult {
		t.Fatalf("killed exec: %+v", reply)
	}

	file := filepath.Join(cwd, "data")
	if err := os.WriteFile(file, []byte("a\nb\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p.request(5, Object{"op": "open", "path": file}, nil)
	if reply := p.reply(5); reply.Kind != FrameResult {
		t.Fatalf("open: %+v", reply)
	}

	p.request(6, Object{"op": "watch", "targets": []any{Object{"path": cwd, "recursive": true}}}, nil)
	awaitEvent(t, p, 6, "ready")
	p.request(7, Object{"op": "pty", "cwd": cwd, "argv": []any{"sleep", "60"}, "cols": float64(80), "rows": float64(24)}, nil)
	awaitEvent(t, p, 7, "started")

	// The connection ends with the watch, the terminal and the handle open.
	_ = p.input.Close()
	select {
	case <-served:
	case <-time.After(goroutineGrace):
		t.Fatal("Serve did not return after its input ended")
	}
	_ = outputReader.Close()
	baseline.RequireEnded(t, goroutineGrace)
}

// awaitEvent reads frames, answering pings, until request id sends an event of the given kind.
func awaitEvent(t *testing.T, p *peer, id uint32, kind string) {
	t.Helper()
	for {
		select {
		case frame, ok := <-p.frames:
			if !ok {
				t.Fatalf("the daemon closed its output before request %d sent %s", id, kind)
			}
			switch {
			case frame.Kind == FramePing:
				p.send(FramePing, 0, Object{}, nil)
			case frame.Kind == FrameEvent && frame.ID == id && object(t, frame)["kind"] == kind:
				return
			case frame.Kind == FrameError && frame.ID == id:
				t.Fatalf("request %d failed before %s: %+v", id, kind, frame)
			}
		case <-time.After(20 * time.Second):
			t.Fatalf("request %d sent no %s event", id, kind)
		}
	}
}
