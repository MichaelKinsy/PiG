package subprocess

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// restartReader is one kind of caller that reads a published extension from a goroutine of its own, and the production entry point
// it goes through. A restart that rewrites what the reader reads without the reader's guard is reported by the race detector.
type restartReader struct {
	name string
	read func(r *restartReaderEnv)
}

type restartReaderEnv struct {
	h    *Host
	name string
	me   *managedExt
	held *extension.Extension
	ctx  context.Context
	// defaults records the generation labels FlagDefault returned, in the order one reader saw them.
	defaults []string
}

// restartReaders is every reader kind of the registration a restart replaces: the owner loop's state pushes, the host's flag resolution, the
// runners' tool, command and event calls, the extension's own host calls and the status readers.
var restartReaders = []restartReader{
	{"flag resolution", func(r *restartReaderEnv) {
		if v, _ := r.h.FlagDefault(r.name, "probe-flag").(string); v != "" {
			r.defaults = append(r.defaults, v)
		}
		_ = r.h.FlagDefault("", "probe-flag")
	}},
	{"state push", func(r *restartReaderEnv) { r.h.BroadcastStateUpdate() }},
	{"command call", func(r *restartReaderEnv) {
		if command, ok := r.held.Command("probe"); ok {
			_ = command.Handler(r.ctx, "")
		}
	}},
	{"tool call", func(r *restartReaderEnv) {
		if tool, ok := r.held.RegisteredTool("greet"); ok {
			_, _ = tool.Definition.Execute(r.ctx, "call", []byte(`{"name":"x"}`), nil)
		}
	}},
	{"event dispatch", func(r *restartReaderEnv) {
		for _, handler := range r.held.EventHandlers("session_start") {
			_, _ = handler(map[string]any{"type": "session_start"})
		}
	}},
	{"extension list", func(r *restartReaderEnv) {
		_ = r.h.Extensions()
		_ = r.h.ExtensionCount()
	}},
	{"event subscription call", func(r *restartReaderEnv) {
		args := json.RawMessage(`{"event":"turn_end","handlerId":900}`)
		_, _ = r.h.handleEventSubscription(r.me, &CallPayload{Method: "event.subscribe", Args: args})
		_, _ = r.h.handleEventSubscription(r.me, &CallPayload{Method: "event.unsubscribe", Args: args})
	}},
	{"thinking runs call", func(r *restartReaderEnv) {
		args := json.RawMessage(`{"enabled":true}`)
		_, _ = r.h.handleOwnThinkingRuns(r.me, &CallPayload{Method: "own_thinking_runs", Args: args})
	}},
	{"status", func(r *restartReaderEnv) {
		_ = r.h.ProviderNames(r.name)
		_ = r.h.OAuthProviderNames(r.name)
		_ = r.h.MemberProcessID(r.name)
		_ = r.h.IsShuttingDown()
		r.h.notifyReloadStarted()
	}},
	{"stop check", func(r *restartReaderEnv) {
		_ = r.me.stoppedByOwner()
		_ = r.me.runtimeProcess()
		_ = processIdentity(r.me)
	}},
}

// restartRig is a published extension whose process crashes on demand and is restarted by its supervisor. The generation it registers is
// in its "probe" command and its "probe-flag" default ("gen 1", "gen 2", ...), so a reader can tell which registration it was served.
type restartRig struct {
	h     *Host
	name  string
	me    *managedExt
	held  *extension.Extension
	crash func(ctx context.Context)
}

var restartSupervisor = SupervisorConfig{MaxCrashes: 100, CrashWindow: time.Minute, InitialDelay: time.Millisecond, MaxDelay: 10 * time.Millisecond, BackoffFactor: 2}

func newGoRestartRig(t *testing.T) *restartRig {
	t.Helper()
	binPath := buildFixture(t)
	shortSockDir(t)
	t.Setenv("FIXTURE_GENERATION_FILE", filepath.Join(t.TempDir(), "generation"))
	h := newTestHost(t)
	h.SetUIBridge(newTestBridge(&mockUIContext{}))
	t.Cleanup(func() { h.Shutdown("test done") })
	recordCrashNotices(t, h)
	if _, err := h.Load(t.Context(), ExtConfig{Name: "fixture", Path: binPath, Enabled: true, SupervisorConfig: restartSupervisor}); err != nil {
		t.Fatal(err)
	}
	return newRestartRig(t, h, "fixture", func(held *extension.Extension) func(context.Context) {
		return func(ctx context.Context) {
			if crash, ok := held.Command("crash"); ok {
				_ = crash.Handler(ctx, "")
			}
		}
	})
}

// newNodeRestartRig is an isolated Node extension: its process is a factory launcher's, so a restart also replaces the process share it runs in.
func newNodeRestartRig(t *testing.T) *restartRig {
	t.Helper()
	skipWithoutNodeExtensions(t)
	nodeCellRequireNode(t)
	counter := filepath.Join(t.TempDir(), "generation")
	source := fmt.Sprintf(`import {existsSync, readFileSync, writeFileSync} from "node:fs";
export default function(pi) {
 const generation = existsSync(%[1]q) ? Number(readFileSync(%[1]q, "utf8")) + 1 : 1;
 writeFileSync(%[1]q, String(generation));
 const label = "gen " + generation;
 pi.registerCommand("probe", {description: label, handler: async () => {}});
 pi.registerFlag("probe-flag", {description: label, type: "string", default: label});
 pi.registerTool({name: "greet", label: "greet", description: label, parameters: {type: "object", properties: {}}, execute: async () => ({content: [{type: "text", text: "hello"}]})});
 pi.registerTool({name: "crash", label: "crash", description: label, parameters: {type: "object", properties: {}}, execute: async () => { process.exit(23); }});
 pi.on("session_start", async () => {});
}`, counter)
	entry := filepath.Join(t.TempDir(), "node-rig.mjs")
	if err := os.WriteFile(entry, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	h := NewHost(t.TempDir())
	h.SetUIBridge(newTestBridge(&mockUIContext{}))
	t.Cleanup(func() { h.Shutdown("test done") })
	recordCrashNotices(t, h)
	cfg := ExtConfig{Name: "noderig", Source: entry, Enabled: true, Isolation: "strict", RuntimeKind: "subprocess", RuntimeLanguage: "node", EntrypointKind: "factory", SDKName: "pi-node", SupervisorConfig: restartSupervisor}
	if _, errs := h.LoadAll(t.Context(), []ExtConfig{cfg}); len(errs) > 0 {
		t.Fatal(errs)
	}
	return newRestartRig(t, h, "noderig", func(held *extension.Extension) func(context.Context) {
		return func(ctx context.Context) {
			if crash, ok := held.RegisteredTool("crash"); ok {
				_, _ = crash.Definition.Execute(ctx, "crash", []byte(`{}`), nil)
			}
		}
	})
}

// generationServe is the process half of a fused extension that registers the generation it is on ("gen 1", "gen 2", ...) in its "probe" command, its
// "probe-flag" default and its "greet" tool, as the other rigs do, and answers every request. crash ends its connection as a process crash does. onState,
// when it is not nil, is called with the state of every state_update the extension receives.
func generationServe(name string, onState func(*StatePayload)) (serve func(net.Conn) error, crash func()) {
	var starts atomic.Int32
	var current atomic.Pointer[net.Conn]
	serve = func(conn net.Conn) error {
		label := fmt.Sprintf("gen %d", starts.Add(1))
		current.Store(&conn)
		reg := &RegisterPayload{
			Name:     name,
			Commands: []CommandDecl{{Name: "probe", Description: label}},
			Flags:    []FlagDecl{{Name: "probe-flag", Type: "string", Description: label, Default: json.RawMessage(strconv.Quote(label))}},
			Tools:    []ToolDecl{{Name: "greet", Description: label, Parameters: json.RawMessage(`{"type":"object"}`)}},
			Handlers: []HandlerDecl{{Event: "session_start", HandlerID: 1}},
		}
		if err := writeEnvelopeTo(conn, &Envelope{Type: MsgRegister, Register: reg}); err != nil {
			return err
		}
		for {
			env, err := readEnvelopeFrom(conn)
			if err != nil {
				return nil
			}
			switch env.Type {
			case MsgPing:
				if env.Ping != nil {
					_ = writeEnvelopeTo(conn, &Envelope{Type: MsgPong, Pong: &PongPayload{Nonce: env.Ping.Nonce}})
				}
			case MsgRequest:
				_ = writeEnvelopeTo(conn, &Envelope{Type: MsgResponse, ID: env.ID, Response: &ResponsePayload{}})
			case MsgNotify:
				if onState != nil && env.Notify != nil && env.Notify.Method == "state_update" {
					var update struct {
						State StatePayload `json:"state"`
					}
					if json.Unmarshal(env.Notify.Args, &update) == nil {
						onState(&update.State)
					}
				}
			}
		}
	}
	crash = func() {
		if conn := current.Load(); conn != nil {
			_ = (*conn).Close()
		}
	}
	return serve, crash
}

// newFusedRestartRig is a fused extension: its factory runs in this process over an in-memory connection, and the same adoption restarts it.
func newFusedRestartRig(t *testing.T) *restartRig {
	t.Helper()
	h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	h.SetUIBridge(newTestBridge(&mockUIContext{}))
	t.Cleanup(func() { h.Shutdown("test done") })
	recordCrashNotices(t, h)
	serve, crash := generationServe("fused", nil)
	cfg := ExtConfig{Name: "fused", Enabled: true, SupervisorConfig: restartSupervisor}
	if _, err := h.LoadInProcess(t.Context(), cfg, serve); err != nil {
		t.Fatal(err)
	}
	return newRestartRig(t, h, "fused", func(*extension.Extension) func(context.Context) {
		return func(context.Context) { crash() }
	})
}

func newRestartRig(t *testing.T, h *Host, name string, crashOf func(*extension.Extension) func(context.Context)) *restartRig {
	t.Helper()
	h.mu.Lock()
	me := h.exts[name]
	h.mu.Unlock()
	if me == nil || me.ext == nil {
		t.Fatalf("%s is not published", name)
	}
	return &restartRig{h: h, name: name, me: me, held: me.ext, crash: crashOf(me.ext)}
}

// A restart replaces the registration of an extension that is already published, on the supervisor's goroutine (startExtOnce, connectExt, adoptConn),
// while every kind of caller reads it on a goroutine of its own. Each reader kind runs alone against the same real restarts, so a race names its
// reader, and the host serves a registration of one generation at every moment: the flag default a reader resolves is never older than the one it
// resolved before.
func TestRestartReplacesRegistrationBesideEveryReader(t *testing.T) {
	for _, mode := range []struct {
		name string
		rig  func(*testing.T) *restartRig
	}{{"go isolated", newGoRestartRig}, {"node isolated", newNodeRestartRig}, {"fused", newFusedRestartRig}} {
		t.Run(mode.name, func(t *testing.T) {
			for _, kind := range restartReaders {
				t.Run(kind.name, func(t *testing.T) { driveRestartsBesideReader(t, mode.rig(t), kind) })
			}
		})
	}
}

func driveRestartsBesideReader(t *testing.T, rig *restartRig, kind restartReader) {
	t.Helper()
	ctx, end := context.WithTimeout(t.Context(), testbudget.Wait(t))
	defer end()
	// A reader's call into a connection that is being replaced ends when the readers are told to stop.
	readCtx, stopReading := context.WithCancel(ctx)
	defer stopReading()
	var readers sync.WaitGroup
	var seen atomic.Int64
	results := make([]*restartReaderEnv, 2)
	for i := range results {
		env := &restartReaderEnv{h: rig.h, name: rig.name, me: rig.me, held: rig.held, ctx: readCtx}
		results[i] = env
		readers.Go(func() {
			for readCtx.Err() == nil {
				kind.read(env)
				seen.Add(1)
				runtime.Gosched()
			}
		})
	}
	const restarts = 2
	for generation := 2; generation < 2+restarts; generation++ {
		rig.crash(ctx)
		waitDeclarationGeneration(t, rig.h, rig.name, rig.held, fmt.Sprintf("gen %d", generation))
	}
	stopReading()
	readers.Wait()
	if seen.Load() == 0 {
		t.Fatal("the reader never ran")
	}
	for _, env := range results {
		if err := orderedGenerations(env.defaults); err != nil {
			t.Fatalf("flag defaults resolved while the extension restarted: %v", err)
		}
	}
}

// orderedGenerations reports whether the generation labels never go back: a reader that resolved "gen 2" does not later resolve "gen 1".
func orderedGenerations(labels []string) error {
	last := 0
	for _, label := range labels {
		var generation int
		if _, err := fmt.Sscanf(strings.TrimSpace(label), "gen %d", &generation); err != nil {
			return fmt.Errorf("label %q is not a generation", label)
		}
		if generation < last {
			return fmt.Errorf("resolved %q after gen %d", label, last)
		}
		last = generation
	}
	return nil
}
