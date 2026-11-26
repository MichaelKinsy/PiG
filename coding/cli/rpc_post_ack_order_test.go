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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// waitActiveReplyGateNil polls activeReplyGate(event) until it returns nil (since releasePostAck runs
// in a macrotask after the response record is written to stdout) or fails when testbudget.Wait expires.
func waitActiveReplyGateNil(t *testing.T, host *rpcHost, event, desc string) {
	t.Helper()
	deadline := time.After(testbudget.Wait(t))
	for {
		if host.activeReplyGate(event) == nil {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("post-ack gate for %q remained active (%s)", event, desc)
		case <-time.After(time.Millisecond):
		}
	}
}

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

			// Wait for callLane to reach its hold-or-dispatch decision: ui.notify must park in awaitPostAckGate
			// while the reply write is still paused, and must not reach stdout ahead of the reply.
			buffered := waitSubprocessCallHeldInPostAckGate(t, h.records, func(rec rpcRecord) error {
				if isNameHandlerDone(rec, tc.event) {
					unpauseReply()
					return errors.New("ui.notify reached stdout before command reply write")
				}
				return nil
			})

			unpauseReply()

			var seen []string
			recordEvent := func(rec rpcRecord) {
				if rec["type"] == "response" && rec["id"] == "c1" {
					seen = append(seen, "reply")
				}
				if isNameHandlerDone(rec, tc.event) {
					seen = append(seen, "effect")
				}
			}
			for _, rec := range buffered {
				recordEvent(rec)
			}
			deadline := time.After(testbudget.Wait(t))
			for len(seen) < 2 {
				select {
				case rec := <-h.records:
					recordEvent(rec)
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
// while set_session_name's post-await ui.setWidget ("w-post") is OBSERVABLY blocked on its post-ack gate
// BEFORE set_thinking_level's prefix ui.setWidget ("w-prefix") runs, w-prefix must NOT deadlock on w-post's
// widget slot; order must be [widget:w-prefix, reply:c1, reply:c2, widget:w-post].
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
	peer := newUnawaitedPeer(t, -1)
	_, port, err := net.SplitHostPort(peer.addr())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("WIDGET_PORT", port)

	// An in-process thinking_level_select handler registered before the Node handler pauses the batch
	// right before Node's prefix setWidget runs, so the test can release peer, observe w-post parked in
	// awaitPostAckGate, and ONLY THEN let Node's prefix w-prefix run while w-post is genuinely held.
	beforePrefix := make(chan struct{})
	allowPrefix := make(chan struct{})
	var allowPrefixOnce sync.Once
	unblockPrefix := func() { allowPrefixOnce.Do(func() { close(allowPrefix) }) }
	defer unblockPrefix()

	holdBeforePrefix := extension.Extension{
		Path: "hold-before-prefix",
		Handlers: map[string][]extension.HandlerFn{
			"input": {func(args ...any) (any, error) {
				close(beforePrefix)
				<-allowPrefix
				return map[string]any{"action": "handled"}, nil
			}},
		},
	}

	h := startRPCInProcessHarness(t, rpcHarnessOptions{
		extensions:  []subprocess.ExtConfig{{Name: "widget-slot", Source: extPath, Enabled: true}},
		extraInproc: []extension.Extension{holdBeforePrefix},
	})

	batch := `{"id":"c1","type":"set_session_name","name":"w"}` + "\n" +
		`{"id":"c-hold","type":"prompt","message":"hold"}` + "\n" +
		`{"id":"c2","type":"set_thinking_level","level":"low"}` + "\n"
	if _, err := io.WriteString(h.inW, batch); err != nil {
		t.Fatal(err)
	}

	peer.awaitHandler(t)
	select {
	case <-beforePrefix:
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("batch did not reach thinking_level_select before-prefix barrier")
	}

	// Release peer so Node sends w-post, and confirm w-post is OBSERVABLY parked in awaitPostAckGate
	// BEFORE allowing Node's thinking_level_select prefix w-prefix to run.
	peer.release()
	buffered := waitSubprocessCallHeldInPostAckGate(t, h.records, func(rec rpcRecord) error {
		if rec["type"] == "extension_ui_request" && rec["method"] == "setWidget" && rec["widgetKey"] == "w-post" {
			unblockPrefix()
			return errors.New("w-post reached stdout before batch replies while gate was closed")
		}
		return nil
	})
	unblockPrefix()

	var seen []string
	recordEvent := func(rec rpcRecord) {
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
	}
	for _, rec := range buffered {
		recordEvent(rec)
	}
	deadline := time.After(testbudget.Wait(t))
	for len(seen) < 4 {
		select {
		case rec := <-h.records:
			recordEvent(rec)
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

	writeModels := func(t *testing.T, agentDir, modelsJSON string) {
		t.Helper()
		if err := os.MkdirAll(agentDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(modelsJSON), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	modelTopologies := []struct {
		name           string
		modelsJSON     string
		startModel     string
		targetProvider string
		targetModel    string
		extraArgs      []string
	}{
		{
			name:           "same-provider",
			modelsJSON:     `{"providers":{"probe":{"baseUrl":"http://127.0.0.1:1","apiKey":"unused","api":"openai-completions","models":[{"id":"r1","name":"R1","reasoning":true},{"id":"r2","name":"R2","reasoning":false}]}}}`,
			startModel:     "probe/r1",
			targetProvider: "probe",
			targetModel:    "r2",
		},
		{
			name:           "cross-provider",
			modelsJSON:     `{"providers":{"probe":{"baseUrl":"http://127.0.0.1:1","apiKey":"unused","api":"openai-completions","models":[{"id":"r1","name":"R1","reasoning":true}]},"probe-alt":{"baseUrl":"http://127.0.0.1:1","apiKey":"unused","api":"openai-completions","models":[{"id":"nr2","name":"NR2","reasoning":false}]}}}`,
			startModel:     "probe/r1",
			targetProvider: "probe-alt",
			targetModel:    "nr2",
			extraArgs:      []string{"--models", "probe/r1,probe-alt/nr2"},
		},
	}

	for _, topo := range modelTopologies {
		for _, cmd := range []string{"set_model", "cycle_model"} {
			for _, withModelSelect := range []bool{false, true} {
				name := topo.name + "/" + cmd + "/no-model-select"
				if withModelSelect {
					name = topo.name + "/" + cmd + "/async-model-select"
				}
				t.Run(name, func(t *testing.T) {
					peer := newUnawaitedPeer(t, 0)
					_, port, err := net.SplitHostPort(peer.addr())
					if err != nil {
						t.Fatal(err)
					}
					home := t.TempDir()
					agent := filepath.Join(home, "agent")
					writeModels(t, agent, topo.modelsJSON)
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
					args := append([]string{"--no-extensions", "--no-skills", "--no-prompt-templates", "--no-session", "--model", topo.startModel, "--thinking", "high", "-e", extPath}, topo.extraArgs...)
					p := startRPCProcessAt(t, t.TempDir(), env, args...)
					awaitReady(p)

					var msg map[string]any
					if cmd == "set_model" {
						msg = map[string]any{"id": "m1", "type": "set_model", "provider": topo.targetProvider, "modelId": topo.targetModel}
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
		waitActiveReplyGateNil(t, h.host, "session_info_changed", "after set_session_name reply was written")
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
		// Fail ONLY the c1 response record (not every record on stdout), and observe activeReplyGate
		// becoming nil after the failed write and BEFORE serveRPCMode exits.
		allowShutdown := make(chan struct{})
		var allowShutdownOnce sync.Once
		releaseShutdown := func() { allowShutdownOnce.Do(func() { close(allowShutdown) }) }
		defer releaseShutdown()

		h := startRPCInProcessHarness(t, rpcHarnessOptions{
			extensions: []subprocess.ExtConfig{{Name: "write-err", Source: fixture, Enabled: true}},
			outWriter: funcWriter(func(p []byte) (int, error) {
				if strings.Contains(string(p), `"id":"c1"`) && strings.Contains(string(p), `"type":"response"`) {
					return 0, errors.New("broken stdout pipe on c1 response")
				}
				return len(p), nil
			}),
			onStdoutErr: func(error) {
				errOnce.Do(func() { close(writeErrSeen) })
				<-allowShutdown
			},
		})
		if _, err := io.WriteString(h.inW, `{"id":"c1","type":"set_session_name","name":"broken-pipe"}`+"\n"); err != nil {
			t.Fatal(err)
		}
		select {
		case <-writeErrSeen:
		case <-time.After(testbudget.Wait(t)):
			t.Fatal("stdout write error on c1 response was never observed")
		}
		// Gate must release promptly after the failed write while serveRPCMode is still running (before cancel/exit).
		waitActiveReplyGateNil(t, h.host, "session_info_changed", "after failed c1 stdout write and before serveRPCMode exit")
		select {
		case <-h.done:
			t.Fatal("serveRPCMode exited before allowShutdown was released")
		default:
		}
		releaseShutdown()
		select {
		case <-h.done:
		case <-time.After(testbudget.Wait(t)):
			t.Fatal("serveRPCMode hung after stdout write error")
		}
		waitActiveReplyGateNil(t, h.host, "session_info_changed", "after serveRPCMode exit")
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
		// Inject a persistence failure into SessionManager so SetSessionName fails AFTER
		// beginReplyGate("session_info_changed") has already registered the gate for e1.
		// Restore the valid in-memory session afterward and verify e2 succeeds.
		origSession := h.rt.Session()
		origInner := origSession.Inner()
		sessDir := filepath.Join(t.TempDir(), "sessions")
		brokenMgr := codingagent.NewSessionManagerWithDir(t.TempDir(), sessDir)
		brokenInner, err := brokenMgr.Create("", "")
		if err != nil {
			t.Fatal(err)
		}
		// Persist one entry so brokenInner marks itself flushed, then replace its file with a directory
		// so the next AppendSessionInfoName fails inside SetSessionName AFTER beginReplyGate is registered.
		if _, err := brokenInner.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "seed"}}}}); err != nil {
			t.Fatal(err)
		}
		sessPath := brokenInner.Path()
		if err := os.Remove(sessPath); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(sessPath, 0o700); err != nil {
			t.Fatal(err)
		}
		origSession.ReplaceInner(brokenInner)

		if _, err := io.WriteString(h.inW, `{"id":"e1","type":"set_session_name","name":"fails-after-gate"}`+"\n"); err != nil {
			t.Fatal(err)
		}
		counts := map[string]int{}
		deadline := time.After(testbudget.Wait(t))
		for counts["e1"] == 0 {
			select {
			case rec := <-h.records:
				if rec["type"] == "response" {
					id, _ := rec["id"].(string)
					counts[id]++
					if id == "e1" {
						if ok, _ := rec["success"].(bool); ok {
							t.Fatalf("e1 expected success=false error response after gate registration, got %v", rec)
						}
					}
				}
			case <-deadline:
				t.Fatalf("timed out waiting for e1 error reply; counts=%v", counts)
			}
		}
		waitActiveReplyGateNil(t, h.host, "session_info_changed", "after e1 SetSessionName error")

		origSession.ReplaceInner(origInner)
		if _, err := io.WriteString(h.inW, `{"id":"e2","type":"set_thinking_level","level":"high"}`+"\n"); err != nil {
			t.Fatal(err)
		}
		for counts["e2"] == 0 {
			select {
			case rec := <-h.records:
				if rec["type"] == "response" {
					id, _ := rec["id"].(string)
					counts[id]++
					if id == "e2" {
						if ok, _ := rec["success"].(bool); !ok {
							t.Fatalf("e2 expected success=true response, got %v", rec)
						}
					}
				}
			case <-deadline:
				t.Fatalf("timed out waiting for e2 success reply; counts=%v", counts)
			}
		}
		if counts["e1"] != 1 || counts["e2"] != 1 {
			t.Fatalf("expected exactly one response for e1 and e2, got counts=%v", counts)
		}
		waitActiveReplyGateNil(t, h.host, "session_info_changed", "after e1+e2")
		waitActiveReplyGateNil(t, h.host, "thinking_level_select", "after e1+e2")
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
		waitActiveReplyGateNil(t, h.host, "session_info_changed", "after sequential commands")
		h.close()
		baseline.RequireEnded(t, testbudget.Wait(t))
	})
}
