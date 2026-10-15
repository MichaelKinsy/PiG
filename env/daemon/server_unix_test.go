//go:build !windows

package daemon

// pi: packages/env/daemon/src/sys/unix.rs

import (
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestServePingsAndStopsEverythingWhenTheClientGoesSilent(t *testing.T) {
	silent := make(chan struct{})
	// OnSilence runs on the daemon's ping goroutine, so it only signals; the test closes the input once it sees the signal.
	p := startServe(t, Options{PingInterval: 50 * time.Millisecond, SilenceLimit: 2 * time.Second, OnSilence: func() { close(silent) }})
	p.request(1, Object{"op": "exec", "cwd": t.TempDir(), "command": "echo $$; exec sleep 60"}, nil)
	// The client answers pings, as a live client does, until the command has started; only then does it go silent. Otherwise a
	// host slow to start the shell reaches the silence limit before the command exists. The limit leaves the live client's
	// ping round trip (daemon, pipe, test goroutine, pipe, daemon) room for a loaded host: under -race beside other packages a
	// 400 ms limit fired while the client was still answering.
	var pid int
	for pid == 0 {
		var frame *Frame
		select {
		case next, ok := <-p.frames:
			if !ok {
				t.Fatal("the daemon closed its output before the command started")
			}
			frame = next
		case <-time.After(20 * time.Second):
			t.Fatal("no frame from the daemon")
		}
		switch {
		case frame.Kind == FramePing:
			p.send(FramePing, 0, Object{}, nil)
		case frame.Kind == FrameEvent && frame.ID == 1:
			pid, _ = strconv.Atoi(strings.TrimSpace(string(frame.Payload)))
		}
	}
	select {
	case <-silent:
	case <-time.After(20 * time.Second):
		t.Fatal("the silence limit never fired")
	}
	_ = p.input.Close()
	pings := 0
	for frame := range p.frames {
		if frame.Kind == FramePing {
			pings++
		}
	}
	if pings < 3 {
		t.Fatalf("%d pings in the silent period, want several", pings)
	}
	deadline := time.Now().Add(5 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("the command (pid %d) outlived a silent client", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// main.rs hello and sys/unix.rs tmpdir: a daemon built for Android reports `android` and falls back to Termux's
// $PREFIX/tmp, also when it is the linux/amd64 build that make build-env-daemons packages as pi-env-android-x64.
func TestHelloAndTmpdirFollowTheTargetSystem(t *testing.T) {
	saved := targetOS
	t.Cleanup(func() { targetOS = saved })
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(key, "")
	}
	t.Setenv("PREFIX", "/data/data/com.termux/files/usr")
	targetOS = "android"
	if got := osName(); got != "android" {
		t.Fatalf("osName() = %q, want android", got)
	}
	if got := tmpdir(); got != "/data/data/com.termux/files/usr/tmp" {
		t.Fatalf("tmpdir() = %q, want Termux's $PREFIX/tmp", got)
	}
	targetOS = "linux"
	if got := tmpdir(); got != "/tmp" {
		t.Fatalf("tmpdir() = %q, want /tmp", got)
	}
	targetOS = "darwin"
	if got := osName(); got != "macos" {
		t.Fatalf("osName() = %q, want macos", got)
	}
}
