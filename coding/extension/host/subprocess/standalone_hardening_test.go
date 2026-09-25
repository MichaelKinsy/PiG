package subprocess

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

func TestStandaloneProcessExitFailsTheLoad(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executable script fixture requires a Unix host")
	}
	script := filepath.Join(t.TempDir(), "exit-before-connect")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 7\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	host := NewHost(t.TempDir())
	_, loadErrors := host.LoadAll(testbudget.Context(t), []ExtConfig{{Name: "never-connects", Path: script, Enabled: true}})
	if len(loadErrors) != 1 {
		t.Fatalf("load errors = %v, want one", loadErrors)
	}
	var loadErr *LoadError
	if !errors.As(loadErrors[0], &loadErr) || loadErr.Code != "process_exited" {
		t.Fatalf("load error = %v, want process_exited", loadErrors[0])
	}
	var exitErr *exec.ExitError
	if !errors.As(loadErrors[0], &exitErr) || exitErr.ExitCode() != 7 {
		t.Fatalf("load error = %v, want child exit status 7", loadErrors[0])
	}
}

func TestStandaloneRebuildHint(t *testing.T) {
	if h := standaloneRebuildHint(ExtConfig{Name: "ctx", Path: "/x/ctx"}); !strings.Contains(h, "prebuilt") || !strings.Contains(h, "rebuild") {
		t.Fatalf("standalone config should yield a rebuild hint, got %q", h)
	}
	if h := standaloneRebuildHint(ExtConfig{Name: "ws", Source: "/src/ws"}); h != "" {
		t.Fatalf("source-mode config should yield no hint, got %q", h)
	}
}

func TestFrameSkewHint(t *testing.T) {
	const size = uint64(20 << 20)
	// Not recent -> no attribution.
	if h := frameSkewHint(ExtConfig{Name: "ctx"}, size, false); h != "" {
		t.Fatalf("no recent oversized frame should yield no hint, got %q", h)
	}
	// Standalone, recent -> names the size and tells the author to rebuild.
	hs := frameSkewHint(ExtConfig{Name: "ctx", Path: "/x/ctx"}, size, true)
	if !strings.Contains(hs, "20.0 MiB") || !strings.Contains(hs, "Rebuild this prebuilt") {
		t.Fatalf("standalone skew hint missing size/remedy: %q", hs)
	}
	// Source-mode, recent -> reassures pig will rebuild.
	hsrc := frameSkewHint(ExtConfig{Name: "ws", Source: "/src/ws"}, size, true)
	if !strings.Contains(hsrc, "pig will rebuild it on next load") {
		t.Fatalf("source skew hint should promise auto-rebuild: %q", hsrc)
	}
}

func TestLoadError_HintAppended(t *testing.T) {
	e := &LoadError{Phase: "connect", Code: "process_exited", Err: errors.New("exited before connecting"), Hint: "rebuild it"}
	got := e.Error()
	if !strings.Contains(got, "connect:process_exited") || !strings.Contains(got, "exited before connecting") {
		t.Fatalf("base error text missing: %q", got)
	}
	if !strings.Contains(got, "rebuild it") {
		t.Fatalf("hint not appended to error: %q", got)
	}
	// No hint -> no trailing separator.
	plain := (&LoadError{Phase: "build", Code: "x", Err: errors.New("boom")}).Error()
	if strings.Contains(plain, "\u2014") {
		t.Fatalf("hintless error should not carry a separator: %q", plain)
	}
}

// TestConn_RecentOversizedFrameTracking drives the production write path:
// sending a frame above the legacy cap must be recorded so a later disconnect
// can be attributed to frame-cap skew; a small frame must not be.
func TestConn_RecentOversizedFrameTracking(t *testing.T) {
	hostEnd, extEnd := net.Pipe()
	c := NewConn("t", hostEnd)
	c.Start(t.Context())

	// Drain whatever the host writes so net.Pipe writes complete.
	go func() { _, _ = io.Copy(io.Discard, extEnd) }()

	// Small frame first: not oversized.
	if err := c.Send(&Envelope{Type: MsgNotify, Notify: &NotifyPayload{}}); err != nil {
		t.Fatalf("send small: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if _, ok := c.RecentOversizedFrame(); ok {
		t.Fatal("small frame should not be flagged oversized")
	}

	// A frame above the legacy 16 MiB cap.
	big, _ := json.Marshal(strings.Repeat("x", 20*1024*1024))
	if err := c.Send(&Envelope{Type: MsgCallResult, ID: "1", CallResult: &CallResultPayload{Result: big}}); err != nil {
		t.Fatalf("send big: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if sz, ok := c.RecentOversizedFrame(); ok {
			if sz <= legacyFrameSize {
				t.Fatalf("recorded size %d not above legacy cap %d", sz, legacyFrameSize)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("oversized frame was not recorded")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestHandleIncoming_AttributesFrameSkewDisconnect drives the real crash path:
// a standalone extension that disconnects right after the host sent an
// oversized frame must surface a frame-cap-skew rebuild hint through onCrash,
// not a bare "crashed" report.
func TestHandleIncoming_AttributesFrameSkewDisconnect(t *testing.T) {
	hostEnd, extEnd := net.Pipe()
	c := NewConn("ctx", hostEnd)
	c.Start(t.Context())

	// Simulate the host having just sent a 20 MiB frame to this extension.
	c.lastLargeSize.Store(uint64(20 << 20))
	c.lastLargeAtNs.Store(time.Now().UnixNano())

	// Peer disconnects -> host readLoop ends -> Incoming() closes.
	_ = extEnd.Close()

	h := NewHost(t.TempDir())
	reasonCh := make(chan string, 1)
	disabledCh := make(chan bool, 1)
	h.SetCrashHandler(func(name string, delay time.Duration, disabled bool, reason string) {
		disabledCh <- disabled
		reasonCh <- reason
	})

	me := &managedExt{
		config:     ExtConfig{Name: "ctx", Path: "/x/ctx"}, // standalone (no Source)
		host:       h,
		supervisor: NewSupervisor(DefaultSupervisorConfig()),
		conn:       c,
	}
	go h.handleIncoming(me)

	select {
	case reason := <-reasonCh:
		if d := <-disabledCh; d {
			t.Fatalf("first crash should not disable; reason=%q", reason)
		}
		if !strings.Contains(reason, "frame") || !strings.Contains(reason, "Rebuild this prebuilt") {
			t.Fatalf("disconnect not attributed to frame-cap skew for a standalone ext: %q", reason)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("onCrash was not invoked after disconnect")
	}
}

func TestFormatCrashNotice(t *testing.T) {
	if got := FormatCrashNotice("ws", 2*time.Second, true, "circuit tripped"); got != `extension "ws" disabled: circuit tripped` {
		t.Fatalf("disabled case: %q", got)
	}
	got := FormatCrashNotice("ctx", 2*time.Second, false, "rebuild it")
	if !strings.Contains(got, `extension "ctx" crashed, restart in 2s`) || !strings.Contains(got, ": rebuild it") {
		t.Fatalf("reason case missing delay/separator/reason: %q", got)
	}
	if got := FormatCrashNotice("ctx", 2*time.Second, false, ""); got != `extension "ctx" crashed, restart in 2s` {
		t.Fatalf("bare case: %q", got)
	}
}
