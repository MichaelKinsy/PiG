// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"path/filepath"
	"slices"
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
)

// TestRPCShortWaitOrderPostAwaitNotifyOvertakesReplyDeterministic exercises serveRPCMode through its existing rpcModeOptions / rpcTransport seams with the real Node fixture testdata/rpc-settle-wait.mjs (session_info_changed awaits a TCP peer, then calls ctx.ui.notify).
// A second command (set_thinking_level) in the same stdin batch uses an in-process thinking_level_select handler to hold commandLoop after set_session_name has woken from <-invoked and queued its RPCResponse on rpcResponseTurn, before inputTurn.End() drains it.
// While commandLoop is held there (the exact window between <-invoked and inputTurn.End()), releasing the Node handler causes Node to send ui.notify -> readLoop -> handleIncoming -> callLane -> UIBridge -> rpcUI.Notify -> writeImmediate, writing the notification to stdout BEFORE the set_session_name reply -> [effect reply], deterministically on the unchanged code.
func TestRPCShortWaitOrderPostAwaitNotifyOvertakesReplyDeterministic(t *testing.T) {
	cwd, agentDir := t.TempDir(), t.TempDir()
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: cwd, AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	defer services.Close()
	manager, err := coding.NewInMemorySessionManager(cwd)
	if err != nil {
		t.Fatal(err)
	}
	provider := ai.NewFauxProvider(ai.FauxConfig{})
	model := &ai.Model{
		ID:           "faux-1",
		Provider:     provider,
		Capabilities: ai.ModelCapabilities{MaxThinking: ai.ThinkingLevel(ai.ThinkingHigh)},
		ProviderMeta: ai.ProviderMetadata{ProviderID: provider.ID()},
	}

	fixture, err := filepath.Abs(filepath.Join("testdata", "rpc-settle-wait.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	t.Setenv("RPC_NAME_ADDR", ln.Addr().String())

	peerConnCh := make(chan net.Conn, 4)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				if line, err := bufio.NewReader(conn).ReadString(10); err == nil && line == "waiting\n" {
					peerConnCh <- conn
				}
			}(c)
		}
	}()

	notifyRead := make(chan struct{})
	var notifyReadOnce sync.Once
	subHost := subprocess.NewHost(cwd)
	subHost.SetMode("rpc")
	subHost.SetInboundObserver(func(_ string, env *subprocess.Envelope) {
		if env != nil && env.Type == subprocess.MsgCall && env.Call != nil && env.Call.Method == "ui.notify" {
			notifyReadOnce.Do(func() { close(notifyRead) })
		}
	})
	bridge := subprocess.NewUIBridge(nil)
	subHost.SetUIBridge(bridge)
	defer subHost.Shutdown("test done")
	loaded, err := subHost.Load(t.Context(), subprocess.ExtConfig{Name: "short-wait", Source: fixture, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	// An in-process thinking_level_select handler holds the second command of the same stdin batch before acknowledging,
	// keeping commandLoop inside inputTurn after set_session_name has woken from <-invoked and queued its reply.
	turnHeld := make(chan struct{})
	releaseTurn := make(chan struct{})
	var releaseOnce sync.Once
	unhold := func() { releaseOnce.Do(func() { close(releaseTurn) }) }
	defer unhold()

	factory := func(ctx context.Context, opts coding.CreateAgentSessionRuntimeOptions) (coding.CreateAgentSessionRuntimeResult, error) {
		base, err := coding.NewRuntime(coding.RuntimeOptions{
			Services:      services,
			ExtensionHost: &cliExtensionHost[struct{}]{host: subHost, bridge: bridge, factory: newCLISessionFactory(t.Context(), cliSessionInputs{Host: subHost, Bridge: bridge}, struct{}{}, nil)},
		})
		if err != nil {
			return coding.CreateAgentSessionRuntimeResult{}, err
		}
		session, err := base.New(coding.SessionStartOptions{
			Model:            model,
			ThinkingLevel:    ai.ThinkingOff,
			SkipBuiltinTools: true,
			SessionManager:   opts.SessionManager,
		})
		if err != nil {
			return coding.CreateAgentSessionRuntimeResult{}, err
		}
		holder := extension.Extension{Path: "turn-holder", Handlers: map[string][]extension.HandlerFn{
			"input": {func(args ...any) (any, error) {
				close(turnHeld)
				<-releaseTurn
				return map[string]any{"action": "handled"}, nil
			}},
		}}
		nodeExt := *loaded
		nodeExt.Handlers = map[string][]extension.HandlerFn{"session_info_changed": loaded.Handlers["session_info_changed"]}
		session.ReplaceRunner(inproc.NewRunner([]extension.Extension{nodeExt, holder}, cwd))
		return coding.CreateAgentSessionRuntimeResult{Session: session, Services: services}, nil
	}

	rt, err := coding.CreateAgentSessionRuntime(t.Context(), factory, coding.CreateAgentSessionRuntimeOptions{
		CWD: cwd, AgentDir: agentDir, SessionManager: manager,
	})
	if err != nil {
		t.Fatal(err)
	}

	var writeMu sync.Mutex
	var stdoutClosed atomic.Bool
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	rawStdout := newStdoutQueue(outW, func(error) {})
	defer rawStdout.Close()

	records := make(chan rpcRecord, 32)
	readDone := make(chan struct{})
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

	writeImmediate := func(v any) {
		writeMu.Lock()
		defer writeMu.Unlock()
		if stdoutClosed.Load() {
			return
		}
		writeJSONLine(rawStdout, v)
	}
	responses := &rpcResponseTurn{write: writeImmediate}
	writeRPC := func(v any) {
		switch v.(type) {
		case RPCResponse, rpcNullResponse, rpcUnknownCommandResponse:
			responses.complete(v)
		default:
			writeImmediate(v)
		}
	}
	rpcUI := newRPCUIContext(writeRPC)
	defer rpcUI.Close()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
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
	done := make(chan int, 1)
	go func() {
		done <- serveRPCMode(ctx, rt, rpcModeOptions{
			exitCode: &exitCode, host: host, cancel: cancel, shutdownGate: shutdownGate,
			stdoutClosed: &stdoutClosed, rawStdout: rawStdout,
			transport: rpcTransport{in: inR, out: outW, exit: func(int) {}},
			responses: responses, writeRPC: writeRPC, writeImmediate: writeImmediate,
			writeLine: func([]byte) {}, rpcUI: rpcUI,
			promptWG: &promptWG, bashWG: &bashWG, commandWG: &commandWG,
		})
	}()

	// Both lines in ONE Write so ReadJSONLBatches hands them in one inputBatch under one responses.Begin()..End().
	batch := `{"id":"c1","type":"set_session_name","name":"one"}` + "\n" + `{"id":"c2","type":"prompt","message":"hold"}` + "\n"
	if _, err := io.WriteString(inW, batch); err != nil {
		t.Fatal(err)
	}

	var peerConn net.Conn
	select {
	case peerConn = <-peerConnCh:
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("Node session_info_changed handler did not dial peer")
	}
	defer func() { _ = peerConn.Close() }()
	select {
	case <-turnHeld:
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("commandLoop did not reach the second command in the batch")
	}

	// Release the Node handler while commandLoop is still before inputTurn.End():
	// Node finishes await and sends ui.notify -> readLoop -> handleIncoming -> callLane -> UIBridge -> rpcUI.Notify -> writeImmediate.
	if _, err := peerConn.Write([]byte("go\n")); err != nil {
		t.Fatal(err)
	}

	select {
	case <-notifyRead:
	case <-time.After(testbudget.Wait(t)):
		unhold()
		t.Fatal("Node ui.notify never reached Conn.readLoop while inputTurn was held")
	}
	// Observe whether the callLane lets ui.notify overtake the held inputTurn before releasing it.
	// With the post-ack gate active this timer expires cleanly; if the gate is bypassed, ui.notify
	// writes to stdout within milliseconds and fails here deterministically.
	select {
	case rec := <-records:
		unhold()
		if isNameHandlerDone(rec, "session_info_changed") {
			t.Fatalf("ui.notify reached stdout before held command reply: %v", rec)
		}
		records <- rec
	case <-time.After(150 * time.Millisecond):
	}
	unhold()

	var seen []string
	deadline := time.After(testbudget.Wait(t))
	for len(seen) < 2 {
		select {
		case rec := <-records:
			if rec["type"] == "response" && rec["id"] == "c1" {
				seen = append(seen, "reply")
			}
			if isNameHandlerDone(rec, "session_info_changed") {
				seen = append(seen, "effect")
			}
		case <-deadline:
			t.Fatalf("timed out waiting for reply and effect; seen=%v", seen)
		}
	}
	unhold()
	_ = inW.Close()
	cancel()
	<-done
	_ = outW.Close()
	<-readDone

	want := []string{"reply", "effect"}
	if !slices.Equal(seen, want) {
		t.Fatalf("order = %v, want %v", seen, want)
	}
}
