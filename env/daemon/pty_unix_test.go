//go:build linux || darwin

package daemon

// pi: packages/env/daemon/src/pty.rs

// Ports the rules of packages/env/docs/protocol.md that the terminal tests of packages/env/test reach only through the
// client: a terminal's process group dies with the connection and with a silent client, and `started` comes first.

import (
	"syscall"
	"testing"
	"time"
)

// startTerminal runs `sleep 60` in a terminal as request 1 and returns its pid from the `started` event, answering
// pings meanwhile as a live client does.
func startTerminal(t *testing.T, p *peer) int {
	t.Helper()
	p.request(1, Object{"op": "pty", "cwd": t.TempDir(), "argv": []any{"sleep", "60"}, "cols": float64(80), "rows": float64(24)}, nil)
	for {
		var frame *Frame
		select {
		case next, ok := <-p.frames:
			if !ok {
				t.Fatal("the daemon closed its output before the terminal started")
			}
			frame = next
		case <-time.After(20 * time.Second):
			t.Fatal("no frame from the daemon")
		}
		switch {
		case frame.Kind == FramePing:
			p.send(FramePing, 0, Object{}, nil)
		case frame.Kind == FrameEvent && frame.ID == 1:
			value := object(t, frame)
			if value["kind"] != "started" {
				t.Fatalf("first event of the terminal = %v, want started", value)
			}
			pid, _ := value["pid"].(float64)
			return int(pid)
		default:
			t.Fatalf("frame before started: %+v", frame)
		}
	}
}

func waitGone(t *testing.T, pid int, why string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("the terminal's program (pid %d) outlived %s", pid, why)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestServeKillsTerminalsWhenTheConnectionEnds(t *testing.T) {
	p := startServe(t, Options{})
	pid := startTerminal(t, p)
	_ = p.input.Close()
	waitGone(t, pid, "its connection")
}

func TestServeKillsTerminalsWhenTheClientGoesSilent(t *testing.T) {
	silent := make(chan struct{})
	p := startServe(t, Options{PingInterval: 50 * time.Millisecond, SilenceLimit: 2 * time.Second, OnSilence: func() { close(silent) }})
	pid := startTerminal(t, p)
	select {
	case <-silent:
	case <-time.After(20 * time.Second):
		t.Fatal("the silence limit never fired")
	}
	waitGone(t, pid, "a silent client")
}

func TestPtyWriteIsAnsweredInArrivalOrder(t *testing.T) {
	p := startServe(t, Options{})
	startTerminal(t, p)
	// Answered by the goroutine that reads frames, not the worker pool, so the replies follow the requests.
	for id := uint32(10); id < 20; id++ {
		p.request(id, Object{"op": "ptyWrite", "target": float64(1)}, []byte("x"))
	}
	for id := uint32(10); id < 20; id++ {
		frame := p.next()
		for frame.Kind == FrameEvent {
			// The terminal echoes the input.
			frame = p.next()
		}
		if frame.Kind != FrameResult || frame.ID != id {
			t.Fatalf("reply = kind %d id %d, want result %d", frame.Kind, frame.ID, id)
		}
	}
	p.send(FrameCancel, 1, Object{}, nil)
	if frame := p.reply(1); frame.Kind != FrameError || object(t, frame)["code"] != "aborted" {
		t.Fatalf("the cancelled terminal replied %+v", frame)
	}
}
