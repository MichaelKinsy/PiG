//go:build !windows

package daemon

import (
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestServePingsAndStopsEverythingWhenTheClientGoesSilent(t *testing.T) {
	silent := make(chan struct{})
	var p *peer
	p = startServe(t, Options{PingInterval: 20 * time.Millisecond, SilenceLimit: 400 * time.Millisecond, OnSilence: func() {
		close(silent)
		_ = p.input.Close()
	}})
	p.request(1, Object{"op": "exec", "cwd": t.TempDir(), "command": "echo $$; exec sleep 60"}, nil)
	var pid int
	for pid == 0 {
		frame := p.next()
		if frame.Kind == FrameEvent && frame.ID == 1 {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(frame.Payload)))
		}
	}
	pings := 0
	for frame := range p.frames {
		if frame.Kind == FramePing {
			pings++
		}
	}
	select {
	case <-silent:
	default:
		t.Fatal("the silence limit never fired")
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
