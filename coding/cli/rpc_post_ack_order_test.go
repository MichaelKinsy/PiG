// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type failingOutWriter struct {
	err error
}

func (w failingOutWriter) Write([]byte) (int, error) { return 0, w.err }

// startRPCInProcessHarness wires serveRPCMode with a real subprocess.Host, custom extensions, and optional hooks
// on responses.write and transport.out so tests can deterministically exercise single-command reply windows,
// batch windows, multi-extension emissions, slot calls, and every failure/cancellation/shutdown release path.
type rpcInProcessHarness struct {
	t           *testing.T
	host        *rpcHost
	subHost     *subprocess.Host
	rt          *coding.Runtime
	services    *coding.AgentSessionServices
	rawStdout   *stdoutQueue
	rpcUI       *rpcUIContext
	inW         *io.PipeWriter
	outW        *io.PipeWriter
	cancel      context.CancelFunc
	records     chan rpcRecord
	done        chan struct{}
	readDone    chan struct{}
	closeOnce   sync.Once
	outCloseNow func()
}

func (h *rpcInProcessHarness) close() {
	h.closeOnce.Do(func() {
		_ = h.inW.Close()
		h.cancel()
		select {
		case <-h.done:
		case <-time.After(testbudget.Wait(h.t)):
			h.t.Errorf("serveRPCMode did not exit within testbudget.Wait")
		}
		h.rawStdout.Close()
		if h.outCloseNow != nil {
			h.outCloseNow()
		}
		select {
		case <-h.readDone:
		case <-time.After(testbudget.Wait(h.t)):
			h.t.Errorf("RPC output reader did not exit within testbudget.Wait")
		}
		h.rpcUI.Close()
		h.subHost.Shutdown("test done")
		h.services.Close()
	})
}

type rpcHarnessOptions struct {
	extensions    []subprocess.ExtConfig
	extraInproc   []extension.Extension
	models        []*ai.Model
	startThinking ai.ModelThinkingLevel
	wrapWrite     func(write func(any)) func(any)
	outWriter     io.Writer
	onStdoutErr   func(error)
	inboundObs    func(string, *subprocess.Envelope)
}

func startRPCInProcessHarness(t *testing.T, opts rpcHarnessOptions) *rpcInProcessHarness {
	t.Helper()
	cwd, agentDir := t.TempDir(), t.TempDir()
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: cwd, AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { services.Close() })
	manager, err := coding.NewInMemorySessionManager(cwd)
	if err != nil {
		t.Fatal(err)
	}
	models := opts.models
	if len(models) == 0 {
		provider := ai.NewFauxProvider(ai.FauxConfig{})
		models = []*ai.Model{{
			ID:           "faux-1",
			Provider:     provider,
			Capabilities: ai.ModelCapabilities{MaxThinking: ai.ThinkingLevel(ai.ThinkingHigh)},
			ProviderMeta: ai.ProviderMetadata{ProviderID: provider.ID()},
		}}
	}

	subHost := subprocess.NewHost(cwd)
	subHost.SetMode("rpc")
	bridge := subprocess.NewUIBridge(nil)
	subHost.SetUIBridge(bridge)
	if opts.inboundObs != nil {
		subHost.SetInboundObserver(opts.inboundObs)
	}
	var loaded []extension.Extension
	for _, cfg := range opts.extensions {
		ext, err := subHost.Load(t.Context(), cfg)
		if err != nil {
			subHost.Shutdown("load failed")
			t.Fatal(err)
		}
		loaded = append(loaded, *ext)
	}
	loaded = append(loaded, opts.extraInproc...)

	factory := func(ctx context.Context, rOpts coding.CreateAgentSessionRuntimeOptions) (coding.CreateAgentSessionRuntimeResult, error) {
		base, err := coding.NewRuntime(coding.RuntimeOptions{
			Services:      services,
			ExtensionHost: &cliExtensionHost[struct{}]{host: subHost, bridge: bridge, factory: newCLISessionFactory(t.Context(), cliSessionInputs{Host: subHost, Bridge: bridge}, struct{}{}, nil)},
		})
		if err != nil {
			return coding.CreateAgentSessionRuntimeResult{}, err
		}
		startThinking := opts.startThinking
		if startThinking == "" {
			startThinking = ai.ThinkingOff
		}
		session, err := base.New(coding.SessionStartOptions{
			Model:            models[0],
			ThinkingLevel:    startThinking,
			SkipBuiltinTools: true,
			SessionManager:   rOpts.SessionManager,
		})
		if err != nil {
			return coding.CreateAgentSessionRuntimeResult{}, err
		}
		if len(loaded) > 0 {
			session.ReplaceRunner(inproc.NewRunner(loaded, cwd))
		}
		return coding.CreateAgentSessionRuntimeResult{Session: session, Services: services}, nil
	}

	rt, err := coding.CreateAgentSessionRuntime(t.Context(), factory, coding.CreateAgentSessionRuntimeOptions{
		CWD: cwd, AgentDir: agentDir, SessionManager: manager,
	})
	if err != nil {
		subHost.Shutdown("runtime failed")
		t.Fatal(err)
	}

	var writeMu sync.Mutex
	var stdoutClosed atomic.Bool
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	var sink io.Writer = outW
	if opts.outWriter != nil {
		sink = opts.outWriter
	}
	ctx, cancel := context.WithCancel(t.Context())
	rawStdout := newStdoutQueue(sink, func(err error) {
		if opts.onStdoutErr != nil {
			opts.onStdoutErr(err)
		}
		cancel()
	})
	t.Cleanup(func() { rawStdout.Close() })

	records := make(chan rpcRecord, 64)
	readDone := make(chan struct{})
	if opts.outWriter == nil {
		go func() {
			defer close(readDone)
			sc := bufio.NewScanner(outR)
			for sc.Scan() {
				var rec rpcRecord
				if json.Unmarshal(sc.Bytes(), &rec) == nil {
					records <- rec
				}
			}
		}()
	} else {
		close(readDone)
	}

	writeImmediate := func(v any) {
		writeMu.Lock()
		defer writeMu.Unlock()
		if stdoutClosed.Load() {
			return
		}
		writeJSONLine(rawStdout, v)
	}
	respWrite := writeImmediate
	if opts.wrapWrite != nil {
		respWrite = opts.wrapWrite(writeImmediate)
	}
	responses := &rpcResponseTurn{write: respWrite}
	writeRPC := func(v any) {
		switch v.(type) {
		case RPCResponse, rpcNullResponse, rpcUnknownCommandResponse:
			responses.complete(v)
		default:
			writeImmediate(v)
		}
	}
	rpcUI := newRPCUIContext(writeRPC)
	t.Cleanup(func() { rpcUI.Close() })

	shutdownGate := newRPCShutdownGate()
	var promptWG, bashWG, commandWG sync.WaitGroup
	host := &rpcHost{
		ctx: ctx, cancel: cancel,
		ui: rpcUI, writeRPC: writeRPC, responses: responses,
		rt: rt, promptWG: &promptWG, tasks: &rpcTaskGroup{},
		conversionErr:   make(chan error, 1),
		requestShutdown: shutdownGate.Request,
		shutdown:        shutdownGate,
		stdoutWait:      func(ctx context.Context) { rawStdout.Wait(ctx) },
	}
	var exitCode int
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = serveRPCMode(ctx, rt, rpcModeOptions{
			exitCode: &exitCode, host: host, cancel: cancel, shutdownGate: shutdownGate,
			stdoutClosed: &stdoutClosed, rawStdout: rawStdout,
			transport: rpcTransport{in: inR, out: sink, exit: func(int) {}},
			responses: responses, writeRPC: writeRPC, writeImmediate: writeImmediate,
			writeLine: func([]byte) {}, rpcUI: rpcUI,
			promptWG: &promptWG, bashWG: &bashWG, commandWG: &commandWG,
		})
	}()

	h := &rpcInProcessHarness{
		t: t, host: host, subHost: subHost, rt: rt,
		services: services, rawStdout: rawStdout, rpcUI: rpcUI,
		inW: inW, outW: outW, cancel: cancel,
		records: records, done: done, readDone: readDone,
		outCloseNow: func() { _ = outW.Close() },
	}
	t.Cleanup(func() { h.close() })
	return h
}

// TestRPCSingleCommandPostAckOrderThroughResponseWriteSeam proves the single-command (batch size 1) window:
// wrapping rpcResponseTurn.write pauses commandLoop right before writeImmediate(reply); releasing the Node TCP peer
// delivers ui.notify on Conn.readLoop while reply has not written yet; the post-ack gate holds ui.notify off stdout
// until writeImmediate(reply) finishes, guaranteeing [reply effect] for set_session_name, set_thinking_level,
// and cycle_thinking_level.
func TestRPCSingleCommandPostAckOrderThroughResponseWriteSeam(t *testing.T) {
	fixture, err := filepath.Abs(filepath.Join("testdata", "rpc-settle-wait.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		event   string
		command string
	}{
		{"set_session_name", "session_info_changed", `{"id":"c1","type":"set_session_name","name":"single"}`},
		{"set_thinking_level", "thinking_level_select", `{"id":"c1","type":"set_thinking_level","level":"low"}`},
		{"cycle_thinking_level", "thinking_level_select", `{"id":"c1","type":"cycle_thinking_level"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			peer := newUnawaitedPeer(t, -1)
			t.Setenv("RPC_NAME_ADDR", peer.addr())

			pausedAtReply := make(chan struct{})
			releaseReply := make(chan struct{})
			var releaseReplyOnce sync.Once
			unpauseReply := func() { releaseReplyOnce.Do(func() { close(releaseReply) }) }
			defer unpauseReply()

			notifyArrived := make(chan struct{})
			var notifyOnce sync.Once

			h := startRPCInProcessHarness(t, rpcHarnessOptions{
				extensions: []subprocess.ExtConfig{{Name: "short-wait", Source: fixture, Enabled: true}},
				inboundObs: func(_ string, env *subprocess.Envelope) {
					if env != nil && env.Type == subprocess.MsgCall && env.Call != nil && env.Call.Method == "ui.notify" {
						notifyOnce.Do(func() { close(notifyArrived) })
					}
				},
				wrapWrite: func(orig func(any)) func(any) {
					var pausedOnce sync.Once
					return func(v any) {
						if resp, ok := v.(RPCResponse); ok && resp.ID.String() == `"c1"` {
							pausedOnce.Do(func() {
								close(pausedAtReply)
								<-releaseReply
							})
						}
						orig(v)
					}
				},
			})

			if _, err := io.WriteString(h.inW, tc.command+"\n"); err != nil {
				t.Fatal(err)
			}
			peer.awaitHandler(t)
			select {
			case <-pausedAtReply:
			case <-time.After(testbudget.Wait(t)):
				t.Fatal("commandLoop never reached reply write")
			}

			// Release Node handler while reply write is paused and wait until Conn.readLoop has read ui.notify.
			peer.release()
			select {
			case <-notifyArrived:
			case <-time.After(testbudget.Wait(t)):
				t.Fatal("Node ui.notify never arrived on Conn.readLoop")
			}

			// Observe while the reply write is still paused so callLane has time to dispatch ui.notify
			// if the post-ack gate is bypassed. With the gate active, no notification can reach stdout yet.
			observe := time.NewTimer(150 * time.Millisecond)
			defer observe.Stop()
		observeLoop:
			for {
				select {
				case rec := <-h.records:
					if isNameHandlerDone(rec, tc.event) {
						unpauseReply()
						t.Fatal("ui.notify reached stdout before command reply write")
					}
				case <-observe.C:
					break observeLoop
				}
			}

			unpauseReply()

			var seen []string
			deadline := time.After(testbudget.Wait(t))
			for len(seen) < 2 {
				select {
				case rec := <-h.records:
					if rec["type"] == "response" && rec["id"] == "c1" {
						seen = append(seen, "reply")
					}
					if isNameHandlerDone(rec, tc.event) {
						seen = append(seen, "effect")
					}
				case <-deadline:
					t.Fatalf("timed out waiting for reply and effect; seen=%v", seen)
				}
			}
			want := []string{"reply", "effect"}
			if !slices.Equal(seen, want) {
				t.Fatalf("order = %v, want %v", seen, want)
			}
		})
	}
}

// TestRPCTwoNodeExtensionsPostAckOrder proves Review Q1:
// when two Node extensions listen on session_info_changed, handler 2 starts after handler 1 settles while c1 reply
// may still be queued on rpcResponseTurn. Because invocation.Acknowledged(ctx) is already true when handler 2 begins,
// handler 2's ackHold starts sealed and its prefix ui.notify waits for c1 reply too (matching Pi runner.ts:1110).
func TestRPCTwoNodeExtensionsPostAckOrder(t *testing.T) {
	t.Run("ext1-awaits-then-returns-ext2-prefix-notifies", func(t *testing.T) {
		dir := t.TempDir()
		ext1Path := filepath.Join(dir, "ext1.mjs")
		ext2Path := filepath.Join(dir, "ext2.mjs")
		ext1Src := `import { connect } from "node:net";
export default function (pi) {
  pi.on("session_info_changed", async () => {
    await new Promise((resolve, reject) => {
      const s = connect({ host: "127.0.0.1", port: Number(process.env.EXT1_PORT) });
      s.on("connect", () => s.write("waiting\n"));
      s.on("data", () => { s.destroy(); resolve(); });
      s.on("error", reject);
    });
  });
}
`
		ext2Src := `export default function (pi) {
  pi.on("session_info_changed", (_ev, ctx) => {
    ctx.ui.notify("ext2-prefix", "info");
  });
}
`
		if err := os.WriteFile(ext1Path, []byte(ext1Src), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(ext2Path, []byte(ext2Src), 0o600); err != nil {
			t.Fatal(err)
		}

		peer := newUnawaitedPeer(t, -1)
		_, port, err := net.SplitHostPort(peer.addr())
		if err != nil {
			t.Fatal(err)
		}
		t.Setenv("EXT1_PORT", port)

		turnHeld := make(chan struct{})
		releaseTurn := make(chan struct{})
		var releaseOnce sync.Once
		unhold := func() { releaseOnce.Do(func() { close(releaseTurn) }) }
		defer unhold()

		ext2Notified := make(chan struct{})
		var ext2Once sync.Once

		h := startRPCInProcessHarness(t, rpcHarnessOptions{
			extensions: []subprocess.ExtConfig{
				{Name: "ext1", Source: ext1Path, Enabled: true},
				{Name: "ext2", Source: ext2Path, Enabled: true},
			},
			extraInproc: []extension.Extension{{
				Path: "turn-holder",
				Handlers: map[string][]extension.HandlerFn{
					"input": {func(args ...any) (any, error) {
						close(turnHeld)
						<-releaseTurn
						return map[string]any{"action": "handled"}, nil
					}},
				},
			}},
			inboundObs: func(_ string, env *subprocess.Envelope) {
				if env != nil && env.Type == subprocess.MsgCall && env.Call != nil && env.Call.Method == "ui.notify" {
					var payload struct {
						Message string `json:"message"`
					}
					if json.Unmarshal(env.Call.Args, &payload) == nil && payload.Message == "ext2-prefix" {
						ext2Once.Do(func() { close(ext2Notified) })
					}
				}
			},
		})

		batch := `{"id":"c1","type":"set_session_name","name":"two-ext"}` + "\n" + `{"id":"c2","type":"prompt","message":"hold"}` + "\n"
		if _, err := io.WriteString(h.inW, batch); err != nil {
			t.Fatal(err)
		}
		peer.awaitHandler(t)
		select {
		case <-turnHeld:
		case <-time.After(testbudget.Wait(t)):
			t.Fatal("batch did not reach turn holder")
		}

		// Release ext1 while inputTurn is still held open: ext1 finishes, Runner.Emit invokes ext2 (prefix notify)
		// BEFORE inputTurn.End() writes c1 reply.
		peer.release()
		select {
		case <-ext2Notified:
		case <-time.After(testbudget.Wait(t)):
			t.Fatal("ext2 ui.notify never arrived while inputTurn was held")
		}
		unhold()

		var seen []string
		deadline := time.After(testbudget.Wait(t))
		for len(seen) < 2 {
			select {
			case rec := <-h.records:
				if rec["type"] == "response" && rec["id"] == "c1" {
					seen = append(seen, "reply")
				}
				if rec["type"] == "extension_ui_request" && rec["method"] == "notify" {
					if msg, _ := rec["message"].(string); msg == "ext2-prefix" {
						seen = append(seen, msg)
					}
				}
			case <-deadline:
				t.Fatalf("timed out waiting for reply + ext2; seen=%v", seen)
			}
		}
		want := []string{"reply", "ext2-prefix"}
		if !slices.Equal(seen, want) {
			t.Fatalf("order = %v, want %v", seen, want)
		}
	})

	t.Run("ext1-post-await-notify-and-ext2-prefix-notify", func(t *testing.T) {
		dir := t.TempDir()
		ext1Path := filepath.Join(dir, "ext1.mjs")
		ext2Path := filepath.Join(dir, "ext2.mjs")
		ext1Src := `import { connect } from "node:net";
export default function (pi) {
  pi.on("session_info_changed", async (_ev, ctx) => {
    await new Promise((resolve, reject) => {
      const s = connect({ host: "127.0.0.1", port: Number(process.env.EXT1_PORT2) });
      s.on("connect", () => s.write("waiting\n"));
      s.on("data", () => { s.destroy(); resolve(); });
      s.on("error", reject);
    });
    ctx.ui.notify("ext1-post-await", "info");
  });
}
`
		ext2Src := `export default function (pi) {
  pi.on("session_info_changed", (_ev, ctx) => {
    ctx.ui.notify("ext2-prefix", "info");
  });
}
`
		if err := os.WriteFile(ext1Path, []byte(ext1Src), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(ext2Path, []byte(ext2Src), 0o600); err != nil {
			t.Fatal(err)
		}

		peer := newUnawaitedPeer(t, -1)
		_, port, err := net.SplitHostPort(peer.addr())
		if err != nil {
			t.Fatal(err)
		}
		t.Setenv("EXT1_PORT2", port)

		turnHeld := make(chan struct{})
		releaseTurn := make(chan struct{})
		var releaseOnce sync.Once
		unhold := func() { releaseOnce.Do(func() { close(releaseTurn) }) }
		defer unhold()

		ext1Notified := make(chan struct{})
		var ext1Once sync.Once

		h := startRPCInProcessHarness(t, rpcHarnessOptions{
			extensions: []subprocess.ExtConfig{
				{Name: "ext1", Source: ext1Path, Enabled: true},
				{Name: "ext2", Source: ext2Path, Enabled: true},
			},
			extraInproc: []extension.Extension{{
				Path: "turn-holder",
				Handlers: map[string][]extension.HandlerFn{
					"input": {func(args ...any) (any, error) {
						close(turnHeld)
						<-releaseTurn
						return map[string]any{"action": "handled"}, nil
					}},
				},
			}},
			inboundObs: func(_ string, env *subprocess.Envelope) {
				if env != nil && env.Type == subprocess.MsgCall && env.Call != nil && env.Call.Method == "ui.notify" {
					var payload struct {
						Message string `json:"message"`
					}
					if json.Unmarshal(env.Call.Args, &payload) == nil && payload.Message == "ext1-post-await" {
						ext1Once.Do(func() { close(ext1Notified) })
					}
				}
			},
		})

		batch := `{"id":"c1","type":"set_session_name","name":"two-ext-2"}` + "\n" + `{"id":"c2","type":"prompt","message":"hold"}` + "\n"
		if _, err := io.WriteString(h.inW, batch); err != nil {
			t.Fatal(err)
		}
		peer.awaitHandler(t)
		select {
		case <-turnHeld:
		case <-time.After(testbudget.Wait(t)):
			t.Fatal("batch did not reach turn holder")
		}

		// Release ext1 while inputTurn is held: ext1 sends ui.notify (held on post-ack gate), then we unhold inputTurn.
		peer.release()
		select {
		case <-ext1Notified:
		case <-time.After(testbudget.Wait(t)):
			t.Fatal("ext1 ui.notify never arrived while inputTurn was held")
		}
		unhold()

		var seen []string
		deadline := time.After(testbudget.Wait(t))
		for len(seen) < 3 {
			select {
			case rec := <-h.records:
				if rec["type"] == "response" && rec["id"] == "c1" {
					seen = append(seen, "reply")
				}
				if rec["type"] == "extension_ui_request" && rec["method"] == "notify" {
					if msg, _ := rec["message"].(string); msg == "ext1-post-await" || msg == "ext2-prefix" {
						seen = append(seen, msg)
					}
				}
			case <-deadline:
				t.Fatalf("timed out waiting for reply + ext1 + ext2; seen=%v", seen)
			}
		}
		want := []string{"reply", "ext1-post-await", "ext2-prefix"}
		if !slices.Equal(seen, want) {
			t.Fatalf("order = %v, want %v", seen, want)
		}
	})

	t.Run("packed-two-node-extensions", func(t *testing.T) {
		dir := t.TempDir()
		ext1Path := filepath.Join(dir, "ext1.mjs")
		ext2Path := filepath.Join(dir, "ext2.mjs")
		ext1Src := `import { connect } from "node:net";
export default function (pi) {
  pi.on("session_info_changed", async (_ev, ctx) => {
    await new Promise((resolve, reject) => {
      const s = connect({ host: "127.0.0.1", port: Number(process.env.PACKED_EXT1_PORT) });
      s.on("connect", () => s.write("waiting\n"));
      s.on("data", () => { s.destroy(); resolve(); });
      s.on("error", reject);
    });
    ctx.ui.notify("packed-ext1", "info");
  });
}
`
		ext2Src := `export default function (pi) {
  pi.on("session_info_changed", (_ev, ctx) => {
    ctx.ui.notify("packed-ext2", "info");
  });
}
`
		if err := os.WriteFile(ext1Path, []byte(ext1Src), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(ext2Path, []byte(ext2Src), 0o600); err != nil {
			t.Fatal(err)
		}
		peer := newUnawaitedPeer(t, 0)
		_, port, err := net.SplitHostPort(peer.addr())
		if err != nil {
			t.Fatal(err)
		}
		home := t.TempDir()
		agent := filepath.Join(home, "agent")
		writeReasoningModel(t, agent)
		env := []string{
			"HOME=" + home,
			"PIG_HOME=" + filepath.Join(home, ".pig"),
			"PIG_CODING_AGENT_DIR=" + agent,
			"PACKED_EXT1_PORT=" + port,
		}
		p := startRPCProcessAt(t, t.TempDir(), env, "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-session", "--model", "probe/r1", "-e", ext1Path, "-e", ext2Path)
		awaitReady(p)
		p.sendJSON(map[string]any{"id": "p1", "type": "set_session_name", "name": "packed"})
		var seen []string
		p.await("packed reply and both extension notifications", func(r rpcRecord) bool {
			if r["type"] == "response" && r["id"] == "p1" {
				seen = append(seen, "reply")
			}
			if r["type"] == "extension_ui_request" && r["method"] == "notify" {
				if msg, _ := r["message"].(string); msg == "packed-ext1" || msg == "packed-ext2" {
					seen = append(seen, msg)
				}
			}
			return len(seen) == 3
		})
		want := []string{"reply", "packed-ext1", "packed-ext2"}
		if !slices.Equal(seen, want) {
			t.Fatalf("order = %v, want %v", seen, want)
		}
		p.closeAndWait("after packed two-extension order")
	})
}

// TestRPCBatchSetWidgetSlotDoesNotDeadlock proves stall case 3 end-to-end on the real Node runtime:
// a single stdin batch carries [set_session_name (post-await setWidget), set_thinking_level (prefix setWidget)].
// set_thinking_level prefix setWidget must NOT deadlock on set_session_name gated post-await setWidget;
// order must be [thinking-prefix-widget, set_session_name reply, set_thinking_level reply, session-post-await-widget].
func TestRPCBatchSetWidgetSlotDoesNotDeadlock(t *testing.T) {
	dir := t.TempDir()
	extPath := filepath.Join(dir, "widget-slot.mjs")
	extSrc := `import { connect } from "node:net";
export default function (pi) {
  pi.on("session_info_changed", async (_ev, ctx) => {
    await new Promise((resolve, reject) => {
      const s = connect({ host: "127.0.0.1", port: Number(process.env.WIDGET_PORT) });
      s.on("connect", () => s.write("waiting\n"));
      s.on("data", () => { s.destroy(); resolve(); });
      s.on("error", reject);
    });
    ctx.ui.setWidget("w-post", ["post-await"]);
  });
  pi.on("thinking_level_select", (_ev, ctx) => {
    ctx.ui.setWidget("w-prefix", ["prefix"]);
  });
}
`
	if err := os.WriteFile(extPath, []byte(extSrc), 0o600); err != nil {
		t.Fatal(err)
	}
	peer := newUnawaitedPeer(t, 0)
	_, port, err := net.SplitHostPort(peer.addr())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("WIDGET_PORT", port)

	h := startRPCInProcessHarness(t, rpcHarnessOptions{
		extensions: []subprocess.ExtConfig{{Name: "widget-slot", Source: extPath, Enabled: true}},
	})

	batch := `{"id":"c1","type":"set_session_name","name":"w"}` + "\n" + `{"id":"c2","type":"set_thinking_level","level":"low"}` + "\n"
	if _, err := io.WriteString(h.inW, batch); err != nil {
		t.Fatal(err)
	}

	var seen []string
	deadline := time.After(testbudget.Wait(t))
	for len(seen) < 4 {
		select {
		case rec := <-h.records:
			if rec["type"] == "response" {
				if id, _ := rec["id"].(string); id == "c1" || id == "c2" {
					seen = append(seen, "reply:"+id)
				}
			}
			if rec["type"] == "extension_ui_request" && rec["method"] == "setWidget" {
				if key, _ := rec["widgetKey"].(string); key == "w-prefix" || key == "w-post" {
					seen = append(seen, "widget:"+key)
				}
			}
		case <-deadline:
			t.Fatalf("timed out waiting for batch setWidget order (possible slot deadlock); seen=%v", seen)
		}
	}
	want := []string{"widget:w-prefix", "reply:c1", "reply:c2", "widget:w-post"}
	if !slices.Equal(seen, want) {
		t.Fatalf("order = %v, want %v", seen, want)
	}
}

// TestRPCModelCommandsPostAckOrder verifies set_model and cycle_model when the model switch changes the thinking level
// (reasoning model -> non-reasoning model): without a model_select handler (complete == nil), thinking_level_select
// post-await notify follows the set_model / cycle_model reply; with an async model_select handler (complete != nil),
// the post-ack gate is released before model_select runs so thinking_level_select does not stall behind model_select.
func TestRPCModelCommandsPostAckOrder(t *testing.T) {
	dir := t.TempDir()
	extPath := filepath.Join(dir, "model-thinking.mjs")
	extSrc := `import { connect } from "node:net";
export default function (pi) {
  pi.on("thinking_level_select", async (_ev, ctx) => {
    await new Promise((resolve, reject) => {
      const s = connect({ host: "127.0.0.1", port: Number(process.env.MODEL_THINK_PORT) });
      s.on("connect", () => s.write("waiting\n"));
      s.on("data", () => { s.destroy(); resolve(); });
      s.on("error", reject);
    });
    ctx.ui.notify("thinking-after-model", "info");
  });
  if (process.env.MODEL_SELECT_PORT) {
    pi.on("model_select", async () => {
      await new Promise((resolve, reject) => {
        const s = connect({ host: "127.0.0.1", port: Number(process.env.MODEL_SELECT_PORT) });
        s.on("connect", () => s.write("waiting\n"));
        s.on("data", () => { s.destroy(); resolve(); });
        s.on("error", reject);
      });
    });
  }
}
`
	if err := os.WriteFile(extPath, []byte(extSrc), 0o600); err != nil {
		t.Fatal(err)
	}

	writeTwoModels := func(t *testing.T, agentDir string) {
		t.Helper()
		if err := os.MkdirAll(agentDir, 0o700); err != nil {
			t.Fatal(err)
		}
		models := `{"providers":{"probe":{"baseUrl":"http://127.0.0.1:1","apiKey":"unused","api":"openai-completions","models":[{"id":"r1","name":"R1","reasoning":true},{"id":"r2","name":"R2","reasoning":false}]}}}`
		if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(models), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	for _, cmd := range []string{"set_model", "cycle_model"} {
		for _, withModelSelect := range []bool{false, true} {
			name := cmd + "/no-model-select"
			if withModelSelect {
				name = cmd + "/async-model-select"
			}
			t.Run(name, func(t *testing.T) {
				peer := newUnawaitedPeer(t, 0)
				_, port, err := net.SplitHostPort(peer.addr())
				if err != nil {
					t.Fatal(err)
				}
				home := t.TempDir()
				agent := filepath.Join(home, "agent")
				writeTwoModels(t, agent)
				env := []string{
					"HOME=" + home,
					"PIG_HOME=" + filepath.Join(home, ".pig"),
					"PIG_CODING_AGENT_DIR=" + agent,
					"MODEL_THINK_PORT=" + port,
				}
				var msPeer *unawaitedPeer
				if withModelSelect {
					msPeer = newUnawaitedPeer(t, -1)
					_, msPort, err := net.SplitHostPort(msPeer.addr())
					if err != nil {
						t.Fatal(err)
					}
					env = append(env, "MODEL_SELECT_PORT="+msPort)
				}
				p := startRPCProcessAt(t, t.TempDir(), env, "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-session", "--model", "probe/r1", "--thinking", "high", "-e", extPath)
				awaitReady(p)

				var msg map[string]any
				if cmd == "set_model" {
					msg = map[string]any{"id": "m1", "type": "set_model", "provider": "probe", "modelId": "r2"}
				} else {
					msg = map[string]any{"id": "m1", "type": "cycle_model"}
				}
				p.sendJSON(msg)
				if msPeer != nil {
					msPeer.awaitHandler(t)
				}
				var seen []string
				p.await("model reply and thinking_level_select notify", func(r rpcRecord) bool {
					if r["type"] == "response" && r["id"] == "m1" {
						seen = append(seen, "reply")
					}
					if r["type"] == "extension_ui_request" && r["method"] == "notify" && r["message"] == "thinking-after-model" {
						seen = append(seen, "effect")
						if msPeer != nil {
							msPeer.release()
						}
					}
					return len(seen) == 2
				})
				want := []string{"reply", "effect"}
				if withModelSelect {
					want = []string{"effect", "reply"}
				}
				if !slices.Equal(seen, want) {
					t.Fatalf("order = %v, want %v", seen, want)
				}
				p.closeAndWait("after model switch")
			})
		}
	}
}

// TestRPCPostAckGateReleasePaths verifies every CLI-level release path where a command reply may fail, be aborted,
// or hit a stdout write error: the gate closes promptly and leaves zero leaked goroutines.
func TestRPCPostAckGateReleasePaths(t *testing.T) {
	t.Run("command-error-and-idempotent-release", func(t *testing.T) {
		h := &rpcHost{}
		rel1 := h.beginReplyGate("session_info_changed")
		ch1 := h.activeReplyGate("session_info_changed")
		if ch1 == nil {
			t.Fatal("activeReplyGate returned nil while gate open")
		}
		rel2 := h.beginReplyGate("session_info_changed")
		ch2 := h.activeReplyGate("session_info_changed")
		if ch2 == nil || ch2 == ch1 {
			t.Fatal("second beginReplyGate did not become active gate")
		}
		// Release first, then releaseAllPostAckGates, then call both releases again (must not panic or leak).
		rel1()
		select {
		case <-ch1:
		default:
			t.Fatal("rel1 did not close ch1")
		}
		if got := h.activeReplyGate("session_info_changed"); got != ch2 {
			t.Fatal("releasing ch1 lost ch2")
		}
		h.releaseAllPostAckGates()
		select {
		case <-ch2:
		default:
			t.Fatal("releaseAllPostAckGates did not close ch2")
		}
		if got := h.activeReplyGate("session_info_changed"); got != nil {
			t.Fatal("activeReplyGate non-nil after releaseAllPostAckGates")
		}
		// Calling release after releaseAllPostAckGates must be a safe no-op (no double close panic).
		rel1()
		rel2()
	})

	t.Run("abort-and-session-close-with-never-returning-handler", func(t *testing.T) {
		fixture, err := filepath.Abs(filepath.Join("testdata", "rpc-settle-wait.mjs"))
		if err != nil {
			t.Fatal(err)
		}
		peer := newUnawaitedPeer(t, -1) // handler never receives "go\n"
		t.Setenv("RPC_NAME_ADDR", peer.addr())
		baseline := testenv.BaselineGoroutines()

		h := startRPCInProcessHarness(t, rpcHarnessOptions{
			extensions: []subprocess.ExtConfig{{Name: "never-returns", Source: fixture, Enabled: true}},
		})

		if _, err := io.WriteString(h.inW, `{"id":"c1","type":"set_session_name","name":"stuck-handler"}`+"\n"); err != nil {
			t.Fatal(err)
		}
		peer.awaitHandler(t)
		deadline := time.After(testbudget.Wait(t))
		for {
			select {
			case rec := <-h.records:
				if rec["type"] == "response" && rec["id"] == "c1" {
					goto replied
				}
			case <-deadline:
				t.Fatal("set_session_name reply never arrived for never-returning handler")
			}
		}
	replied:
		if got := h.host.activeReplyGate("session_info_changed"); got != nil {
			t.Fatal("post-ack gate remained open after set_session_name reply was written")
		}
		// Close session / cancel serveRPCMode while the handler is still waiting forever on the TCP peer.
		h.close()
		baseline.RequireEnded(t, testbudget.Wait(t))
	})

	t.Run("stdout-write-error-releases-gate", func(t *testing.T) {
		fixture, err := filepath.Abs(filepath.Join("testdata", "rpc-settle-wait.mjs"))
		if err != nil {
			t.Fatal(err)
		}
		peer := newUnawaitedPeer(t, 0)
		t.Setenv("RPC_NAME_ADDR", peer.addr())
		baseline := testenv.BaselineGoroutines()

		writeErrSeen := make(chan struct{})
		var errOnce sync.Once
		h := startRPCInProcessHarness(t, rpcHarnessOptions{
			extensions: []subprocess.ExtConfig{{Name: "write-err", Source: fixture, Enabled: true}},
			outWriter:  failingOutWriter{err: errors.New("broken stdout pipe")},
			onStdoutErr: func(error) {
				errOnce.Do(func() { close(writeErrSeen) })
			},
		})
		if _, err := io.WriteString(h.inW, `{"id":"c1","type":"set_session_name","name":"broken-pipe"}`+"\n"); err != nil {
			t.Fatal(err)
		}
		select {
		case <-writeErrSeen:
		case <-time.After(testbudget.Wait(t)):
			t.Fatal("stdout write error was never observed")
		}
		select {
		case <-h.done:
		case <-time.After(testbudget.Wait(t)):
			t.Fatal("serveRPCMode hung after stdout write error")
		}
		if got := h.host.activeReplyGate("session_info_changed"); got != nil {
			t.Fatal("post-ack gate remained open after stdout write error")
		}
		h.close()
		baseline.RequireEnded(t, testbudget.Wait(t))
	})

	t.Run("session-error-releases-gate", func(t *testing.T) {
		fixture, err := filepath.Abs(filepath.Join("testdata", "rpc-settle-wait.mjs"))
		if err != nil {
			t.Fatal(err)
		}
		peer := newUnawaitedPeer(t, -1)
		t.Setenv("RPC_NAME_ADDR", peer.addr())
		baseline := testenv.BaselineGoroutines()

		h := startRPCInProcessHarness(t, rpcHarnessOptions{
			extensions: []subprocess.ExtConfig{{Name: "err-release", Source: fixture, Enabled: true}},
		})
		// Send invalid thinking level or empty name error, plus close session before FlushEvents/SetSessionName error.
		if _, err := io.WriteString(h.inW, `{"id":"e1","type":"set_session_name","name":"   "}`+"\n"+`{"id":"e2","type":"set_thinking_level","level":"high"}`+"\n"); err != nil {
			t.Fatal(err)
		}
		var seen int
		deadline := time.After(testbudget.Wait(t))
		for seen < 2 {
			select {
			case rec := <-h.records:
				if rec["type"] == "response" && (rec["id"] == "e1" || rec["id"] == "e2") {
					seen++
				}
			case <-deadline:
				t.Fatalf("timed out waiting for error/success replies; seen=%d", seen)
			}
		}
		if got := h.host.activeReplyGate("session_info_changed"); got != nil {
			t.Fatal("session_info_changed gate still active")
		}
		if got := h.host.activeReplyGate("thinking_level_select"); got != nil {
			t.Fatal("thinking_level_select gate still active")
		}
		// Ensure multiple sequential commands never accumulate stale gates.
		for i := range 3 {
			if _, err := io.WriteString(h.inW, `{"id":"s`+strconv.Itoa(i)+`","type":"set_session_name","name":"n`+strconv.Itoa(i)+`"}`+"\n"); err != nil {
				t.Fatal(err)
			}
		}
		for i := range 3 {
			wantID := "s" + strconv.Itoa(i)
			for {
				select {
				case rec := <-h.records:
					if rec["type"] == "response" && rec["id"] == wantID {
						goto next
					}
				case <-deadline:
					t.Fatalf("timed out waiting for reply %s", wantID)
				}
			}
		next:
		}
		if got := h.host.activeReplyGate("session_info_changed"); got != nil {
			t.Fatal("session_info_changed gate leaked after sequential commands")
		}
		h.close()
		baseline.RequireEnded(t, testbudget.Wait(t))
	})
}
