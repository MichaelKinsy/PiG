package subprocess

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

// TestNodeSocketReporterPatternDeliversLifecycleReports pins that a Pi
// extension shaped like herdr's agent-state reporter (issue 136) reaches its
// socket from every lifecycle event PiG dispatches.
//
// The reporter reads HERDR_* from the extension process environment, gates on
// ctx.mode === "tui", awaits a socket write from an async session_start, and
// reports "working" from agent_start and "idle" from agent_settled once
// ctx.isIdle() is true. Pi runs all of this unchanged, so each report must
// arrive, in order, carrying the session path read from ctx.sessionManager.
// herdr drops these reports for a pane whose foreground process it does not
// recognise as pi, but it still acknowledges them; that decision is the
// receiver's and is invisible to this test.
func TestNodeSocketReporterPatternDeliversLifecycleReports(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture dials a unix socket path; herdr uses a named pipe on windows")
	}
	sockDir := shortSockDir(t)
	sock := filepath.Join(sockDir, "h.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	type request struct {
		Method string `json:"method"`
		Params struct {
			PaneID      string `json:"pane_id"`
			Source      string `json:"source"`
			Agent       string `json:"agent"`
			State       string `json:"state"`
			SessionPath string `json:"agent_session_path"`
			Seq         int    `json:"seq"`
		} `json:"params"`
	}
	var mu sync.Mutex
	var got []request
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				line, err := bufio.NewReader(conn).ReadBytes('\n')
				if err != nil {
					return
				}
				var req request
				if json.Unmarshal(line, &req) != nil {
					return
				}
				mu.Lock()
				got = append(got, req)
				mu.Unlock()
				_, _ = conn.Write([]byte(`{"result":{"type":"ok"}}` + "\n"))
			}()
		}
	}()

	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_SOCKET_PATH", sock)
	t.Setenv("HERDR_PANE_ID", "w1:p1")

	sessionFile := filepath.Join(sockDir, "session.jsonl")
	var idleMu sync.Mutex
	idle := true
	setIdle := func(v bool) {
		idleMu.Lock()
		idle = v
		idleMu.Unlock()
	}
	fakeUI := newTestUIContext()
	h := newTestHost(t)
	h.SetMode("tui")
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(fakeUI)
	bridge.SetHostAction("getSessionID", func() string { return "sess-136" })
	bridge.SetHostAction("getSessionFile", func() string { return sessionFile })
	bridge.SetHostAction("isIdle", func() bool {
		idleMu.Lock()
		defer idleMu.Unlock()
		return idle
	})
	h.SetUIBridge(bridge)
	defer h.Shutdown("test done")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ext, err := h.Load(ctx, ExtConfig{
		Name:    "herdr-reporter-pattern",
		Source:  filepath.Join("testdata", "herdr-reporter-pattern.mjs"),
		Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{"session_start", "agent_start", "agent_settled"} {
		if len(ext.Handlers[event]) != 1 {
			t.Fatalf("extension registered %d %s handlers, want 1", len(ext.Handlers[event]), event)
		}
	}

	waitFor := func(n int, what string) []request {
		t.Helper()
		var snapshot []request
		pollUntil(t, 15*time.Second, what, func() bool {
			mu.Lock()
			defer mu.Unlock()
			snapshot = append([]request(nil), got...)
			return len(snapshot) >= n
		})
		return snapshot
	}

	if _, err := ext.Handlers["session_start"][0](map[string]any{"type": "session_start", "reason": "startup"}, ctx); err != nil {
		t.Fatalf("dispatch session_start: %v", err)
	}
	waitFor(2, "session_start never reached the socket (session report then idle state)")

	setIdle(false)
	h.BroadcastStateUpdate()
	if _, err := ext.Handlers["agent_start"][0](map[string]any{"type": "agent_start"}, ctx); err != nil {
		t.Fatalf("dispatch agent_start: %v", err)
	}
	waitFor(3, "agent_start never reached the socket")

	setIdle(true)
	h.BroadcastStateUpdate()
	if _, err := ext.Handlers["agent_settled"][0](map[string]any{"type": "agent_settled"}, ctx); err != nil {
		t.Fatalf("dispatch agent_settled: %v", err)
	}
	reports := waitFor(4, "agent_settled never reached the socket")

	want := []struct{ method, state string }{
		{"pane.report_agent_session", ""},
		{"pane.report_agent", "idle"},
		{"pane.report_agent", "working"},
		{"pane.report_agent", "idle"},
	}
	for i, w := range want {
		r := reports[i]
		if r.Method != w.method || r.Params.State != w.state {
			t.Errorf("report %d = %s/%q, want %s/%q", i, r.Method, r.Params.State, w.method, w.state)
		}
		if r.Params.PaneID != "w1:p1" || r.Params.Source != "herdr:pi" || r.Params.Agent != "pi" {
			t.Errorf("report %d identity = %+v, want pane w1:p1, source herdr:pi, agent pi", i, r.Params)
		}
		if r.Params.SessionPath != sessionFile {
			t.Errorf("report %d session path %q, want %q from ctx.sessionManager.getSessionFile()", i, r.Params.SessionPath, sessionFile)
		}
	}
}
