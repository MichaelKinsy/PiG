// SPDX-License-Identifier: MIT

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/factoryload"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

func requireClosedSignal(t *testing.T, ch <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-ch:
	default:
		t.Fatalf("required synchronization signal %q was not observed", label)
	}
}

func TestRPCPostAckSyncBridgeCallAfterHeldStatusDoesNotDeadlockBatch(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_, _ = conn.Write([]byte("ok"))
			_ = conn.Close()
		}
	}()
	t.Setenv("RPC_DEADLOCK_ADDR", ln.Addr().String())

	extDir := t.TempDir()
	ext1Path := filepath.Join(extDir, "ext1-deadlock.mjs")
	ext1Src := `import net from "node:net";

function waitPeer() {
  const [host, port] = process.env.RPC_DEADLOCK_ADDR.split(":");
  return new Promise((resolve, reject) => {
    const sock = net.connect(Number(port), host);
    let buf = "";
    sock.setEncoding("utf8");
    sock.on("data", (d) => { buf += d; });
    sock.on("end", () => resolve(buf));
    sock.on("error", reject);
  });
}

export default function (pi) {
  pi.registerCommand("cmd", {
    description: "command in same stdin batch",
    handler: async () => {
      console.error("EXT1: /cmd started");
    },
  });
  pi.on("session_info_changed", async (event, ctx) => {
    if (event.name !== "trigger1") return;
    console.error("EXT1: session_info_changed trigger1 start");
    await waitPeer();
    console.error("EXT1: after waitPeer -> calling setStatus + getContextUsage");
    ctx.ui.setStatus("deadlock-key", "after-await");
    const u = ctx.getContextUsage();
    console.error("EXT1: getContextUsage returned", JSON.stringify(u));
  });
}
`
	if err := os.WriteFile(ext1Path, []byte(ext1Src), 0o600); err != nil {
		t.Fatal(err)
	}

	providerMatrix := []struct {
		name          string
		providerID    string
		modelID       string
		maxThinking   ai.ThinkingLevel
		startThinking ai.ModelThinkingLevel
		targetLevel   string
	}{
		{
			name:          "faux-default-high",
			providerID:    "faux-alpha",
			modelID:       "alpha-1",
			maxThinking:   ai.ThinkingLevel(ai.ThinkingHigh),
			startThinking: ai.ThinkingOff,
			targetLevel:   "high",
		},
		{
			name:          "faux-beta-medium-from-low",
			providerID:    "faux-beta",
			modelID:       "beta-2",
			maxThinking:   ai.ThinkingLevel(ai.ThinkingMedium),
			startThinking: ai.ThinkingLow,
			targetLevel:   "medium",
		},
	}

	for _, pm := range providerMatrix {
		t.Run(pm.name, func(t *testing.T) {
			provider := ai.NewFauxProvider(ai.FauxConfig{ProviderID: pm.providerID, Model: pm.modelID})
			model := &ai.Model{
				ID:           pm.modelID,
				Provider:     provider,
				Capabilities: ai.ModelCapabilities{MaxThinking: pm.maxThinking},
				ProviderMeta: ai.ProviderMetadata{ProviderID: provider.ID()},
			}

			syncCallArrived := make(chan struct{})
			var syncOnce sync.Once
			barrierErr := make(chan error, 1)
			holdExt, err := factoryload.LoadExtensionFromFactory(func(api extension.API) error {
				api.OnThinkingLevelSelect(func(ctx context.Context, _ extension.ThinkingLevelSelectEvent) error {
					select {
					case <-syncCallArrived:
						return nil
					case <-time.After(testbudget.Wait(t)):
						err := errors.New("timed out waiting for getContextUsage sync call arrival barrier")
						select {
						case barrierErr <- err:
						default:
						}
						return err
					case <-ctx.Done():
						err := fmt.Errorf("context cancelled waiting for syncCallArrived barrier: %w", ctx.Err())
						select {
						case barrierErr <- err:
						default:
						}
						return err
					}
				})
				return nil
			}, ".", extension.CreateEventBus(), extension.CreateExtensionRuntime(), "test:hold-batch-"+pm.name)
			if err != nil {
				t.Fatal(err)
			}

			h := startRPCInProcessHarness(t, rpcHarnessOptions{
				extensions:    []subprocess.ExtConfig{{Name: "ext1-deadlock", Source: ext1Path, Enabled: true}},
				extraInproc:   []extension.Extension{holdExt},
				models:        []*ai.Model{model},
				startThinking: pm.startThinking,
				inboundObs: func(_ string, env *subprocess.Envelope) {
					if env != nil && env.Type == subprocess.MsgCall && env.Call != nil {
						t.Logf("INBOUND CALL: method=%s parent=%s", env.Call.Method, env.Call.ParentRequestID)
						if env.Call.Method == "getContextUsage" {
							syncOnce.Do(func() { close(syncCallArrived) })
						}
					}
				},
			})
			defer h.close()

			batch := `{"id":"r1","type":"set_session_name","name":"trigger1"}` + "\n" +
				`{"id":"r2","type":"set_thinking_level","level":"` + pm.targetLevel + `"}` + "\n" +
				`{"id":"r3","type":"prompt","message":"/cmd"}` + "\n"
			if _, err := io.WriteString(h.inW, batch); err != nil {
				t.Fatal(err)
			}

			deadline := time.After(testbudget.Wait(t))
			seen := map[string]bool{}
			for !seen["r1"] || !seen["r2"] || !seen["r3"] {
				select {
				case err := <-barrierErr:
					t.Fatalf("required barrier failed: %v", err)
				case rec := <-h.records:
					t.Logf("REC: type=%v id=%v method=%v", rec["type"], rec["id"], rec["method"])
					if rec["type"] == "response" {
						if id, _ := rec["id"].(string); id != "" {
							seen[id] = true
						}
					}
				case <-deadline:
					t.Fatalf("DEADLOCK REPRODUCED: timed out after 5s waiting for batch replies (seen=%v)", seen)
				}
			}
			select {
			case err := <-barrierErr:
				t.Fatalf("required barrier failed: %v", err)
			default:
			}
			requireClosedSignal(t, syncCallArrived, "syncCallArrived (getContextUsage)")
		})
	}
}

// TestRPCPostAckTwoRepliesInOneBatchStayTogether verifies review Finding #5:
// when one stdin batch contains two commands (r1=set_session_name whose post-ack ui.notify arrives while
// the batch is still open, followed by r2=set_thinking_level), BOTH replies (r1 and r2) are written before the
// released post-ack ui.notify lands on stdout (macrotask release after microtask batch drains).
func TestRPCPostAckTwoRepliesInOneBatchStayTogether(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_, _ = conn.Write([]byte("ok"))
			_ = conn.Close()
		}
	}()
	t.Setenv("RPC_BATCH2_ADDR", ln.Addr().String())

	extPath := filepath.Join(t.TempDir(), "batch2-notify.mjs")
	extSrc := `import net from "node:net";
function waitPeer() {
  const [host, port] = process.env.RPC_BATCH2_ADDR.split(":");
  return new Promise((resolve, reject) => {
    const sock = net.connect(Number(port), host);
    sock.on("data", () => {});
    sock.on("end", () => resolve());
    sock.on("error", reject);
  });
}
export default function (pi) {
  pi.on("session_info_changed", async (_event, ctx) => {
    await waitPeer();
    ctx.ui.notify("post-ack-batch2", "info");
  });
}
`
	if err := os.WriteFile(extPath, []byte(extSrc), 0o600); err != nil {
		t.Fatal(err)
	}

	notifyArrived := make(chan struct{})
	var notifyOnce sync.Once
	barrierErr := make(chan error, 1)
	holdExt, err := factoryload.LoadExtensionFromFactory(func(api extension.API) error {
		api.OnThinkingLevelSelect(func(ctx context.Context, _ extension.ThinkingLevelSelectEvent) error {
			select {
			case <-notifyArrived:
				return nil
			case <-time.After(testbudget.Wait(t)):
				err := errors.New("timed out waiting for post-ack ui.notify arrival barrier")
				select {
				case barrierErr <- err:
				default:
				}
				return err
			case <-ctx.Done():
				err := fmt.Errorf("context cancelled waiting for notifyArrived barrier: %w", ctx.Err())
				select {
				case barrierErr <- err:
				default:
				}
				return err
			}
		})
		return nil
	}, ".", extension.CreateEventBus(), extension.CreateExtensionRuntime(), "test:hold-batch2")
	if err != nil {
		t.Fatal(err)
	}

	h := startRPCInProcessHarness(t, rpcHarnessOptions{
		extensions:  []subprocess.ExtConfig{{Name: "batch2-notify", Source: extPath, Enabled: true}},
		extraInproc: []extension.Extension{holdExt},
		inboundObs: func(_ string, env *subprocess.Envelope) {
			if env != nil && env.Type == subprocess.MsgCall && env.Call != nil && env.Call.Method == "ui.notify" {
				notifyOnce.Do(func() { close(notifyArrived) })
			}
		},
	})
	defer h.close()

	batch := `{"id":"r1","type":"set_session_name","name":"b1"}` + "\n" +
		`{"id":"r2","type":"set_thinking_level","level":"high"}` + "\n"
	if _, err := io.WriteString(h.inW, batch); err != nil {
		t.Fatal(err)
	}

	var order []string
	deadline := time.After(testbudget.Wait(t))
	for len(order) < 3 {
		select {
		case err := <-barrierErr:
			t.Fatalf("required barrier failed: %v", err)
		case rec := <-h.records:
			switch rec["type"] {
			case "response":
				if id, _ := rec["id"].(string); id == "r1" || id == "r2" {
					order = append(order, "reply:"+id)
				}
			case "extension_ui_request":
				if rec["method"] == "notify" {
					order = append(order, "effect:notify")
				}
			}
		case <-deadline:
			t.Fatalf("timed out waiting for [reply:r1 reply:r2 effect:notify], got %v", order)
		}
	}
	select {
	case err := <-barrierErr:
		t.Fatalf("required barrier failed: %v", err)
	default:
	}
	requireClosedSignal(t, notifyArrived, "notifyArrived (ui.notify)")
	want := []string{"reply:r1", "reply:r2", "effect:notify"}
	if !slices.Equal(order, want) {
		t.Fatalf("batch order = %v, want %v (post-ack effect must not interleave between batch replies)", order, want)
	}
}

// TestRPCPostAckReleasePathsWithActuallyHeldCall verifies review Finding #3:
// tests stdout write error and session/host shutdown WITH a real post-ack ui.notify
// ACTUALLY HELD on the gate before the release trigger fires.
func TestRPCPostAckReleasePathsWithActuallyHeldCall(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_, _ = conn.Write([]byte("ok"))
			_ = conn.Close()
		}
	}()
	t.Setenv("RPC_HELD_ADDR", ln.Addr().String())

	extPath := filepath.Join(t.TempDir(), "held-notify.mjs")
	extSrc := `import net from "node:net";
function waitPeer() {
  const [host, port] = process.env.RPC_HELD_ADDR.split(":");
  return new Promise((resolve, reject) => {
    const sock = net.connect(Number(port), host);
    sock.on("data", () => {});
    sock.on("end", () => resolve());
    sock.on("error", reject);
  });
}
export default function (pi) {
  pi.on("session_info_changed", async (_event, ctx) => {
    await waitPeer();
    ctx.ui.notify("held-call", "info");
  });
}
`
	if err := os.WriteFile(extPath, []byte(extSrc), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("stdout-write-error-with-call-held", func(t *testing.T) {
		notifyArrived := make(chan struct{})
		var notifyOnce sync.Once
		writeErrSeen := make(chan struct{})
		var writeErrOnce sync.Once
		barrierErr := make(chan error, 1)

		var failWrites atomic.Bool

		h := startRPCInProcessHarness(t, rpcHarnessOptions{
			extensions: []subprocess.ExtConfig{{Name: "held-notify", Source: extPath, Enabled: true}},
			outWriter: funcWriter(func(p []byte) (int, error) {
				if failWrites.Load() {
					return 0, errors.New("broken pipe")
				}
				return len(p), nil
			}),
			onStdoutErr: func(error) {
				writeErrOnce.Do(func() { close(writeErrSeen) })
			},
			inboundObs: func(_ string, env *subprocess.Envelope) {
				if env != nil && env.Type == subprocess.MsgCall && env.Call != nil && env.Call.Method == "ui.notify" {
					notifyOnce.Do(func() { close(notifyArrived) })
				}
			},
			wrapWrite: func(orig func(any)) func(any) {
				return func(v any) {
					if resp, ok := v.(RPCResponse); ok && resp.ID.String() == `"w1"` {
						// Wait until ui.notify has ACTUALLY arrived and is held on the gate before failing stdout write.
						select {
						case <-notifyArrived:
						case <-time.After(testbudget.Wait(t)):
							select {
							case barrierErr <- errors.New("timed out in wrapWrite waiting for ui.notify arrival before failing stdout write"):
							default:
							}
							orig(v)
							return
						}
						failWrites.Store(true)
					}
					orig(v)
				}
			},
		})
		if _, err := io.WriteString(h.inW, `{"id":"w1","type":"set_session_name","name":"fail-out"}`+"\n"); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-barrierErr:
			t.Fatalf("required barrier failed: %v", err)
		case <-notifyArrived:
		case <-time.After(testbudget.Wait(t)):
			t.Fatal("ui.notify never arrived to be held")
		}
		select {
		case err := <-barrierErr:
			t.Fatalf("required barrier failed: %v", err)
		case <-writeErrSeen:
		case <-time.After(testbudget.Wait(t)):
			t.Fatal("stdout write error not observed")
		}
		select {
		case <-h.done:
		case <-time.After(testbudget.Wait(t)):
			t.Fatal("serveRPCMode hung with a held post-ack call after stdout write error")
		}
		select {
		case err := <-barrierErr:
			t.Fatalf("required barrier failed: %v", err)
		default:
		}
		h.close()
	})
}

// TestRPCPostAckSecondHandlerAfterSynchronousFirstHandlerRunsAheadOfReply verifies review Finding #2
// end-to-end with two Node extensions: ext1 completes synchronously (no await), ext2 calls ui.notify
// synchronously in its handler. Ext2's notify MUST NOT be held behind the reply (matches Pi 1.6.0).
func TestRPCPostAckSecondHandlerAfterSynchronousFirstHandlerRunsAheadOfReply(t *testing.T) {
	extDir := t.TempDir()
	ext1Path := filepath.Join(extDir, "ext1-sync.mjs")
	ext2Path := filepath.Join(extDir, "ext2-sync-notify.mjs")
	if err := os.WriteFile(ext1Path, []byte(`export default function (pi) { pi.on("session_info_changed", () => {}); }`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ext2Path, []byte(`export default function (pi) { pi.on("session_info_changed", (_e, ctx) => { ctx.ui.notify("ext2-sync", "info"); }); }`), 0o600); err != nil {
		t.Fatal(err)
	}

	ext2NotifyArrived := make(chan struct{})
	var ext2NotifyOnce sync.Once
	barrierErr := make(chan error, 1)
	h := startRPCInProcessHarness(t, rpcHarnessOptions{
		extensions: []subprocess.ExtConfig{
			{Name: "ext1-sync", Source: ext1Path, Enabled: true},
			{Name: "ext2-sync-notify", Source: ext2Path, Enabled: true},
		},
		inboundObs: func(extName string, env *subprocess.Envelope) {
			if extName == "ext2-sync-notify" && env != nil && env.Type == subprocess.MsgCall && env.Call != nil && env.Call.Method == "ui.notify" {
				ext2NotifyOnce.Do(func() { close(ext2NotifyArrived) })
			}
		},
		wrapWrite: func(orig func(any)) func(any) {
			return func(v any) {
				if resp, ok := v.(RPCResponse); ok && resp.ID.String() == `"s1"` {
					// Wait for ext2's synchronous ui.notify to arrive and apply while s1's gate is STILL CLOSED.
					// If ext2 were wrongly sealed (the bug in Finding #2), ui.notify would wait for s1's gate and land AFTER reply!
					select {
					case <-ext2NotifyArrived:
					case <-time.After(testbudget.Wait(t)):
						select {
						case barrierErr <- errors.New("timed out in wrapWrite waiting for ext2 synchronous ui.notify arrival"):
						default:
						}
					}
					time.Sleep(30 * time.Millisecond)
				}
				orig(v)
			}
		},
	})
	defer h.close()

	if _, err := io.WriteString(h.inW, `{"id":"s1","type":"set_session_name","name":"sync2"}`+"\n"); err != nil {
		t.Fatal(err)
	}

	var order []string
	deadline := time.After(testbudget.Wait(t))
	for len(order) < 2 {
		select {
		case err := <-barrierErr:
			t.Fatalf("required barrier failed: %v", err)
		case rec := <-h.records:
			switch rec["type"] {
			case "response":
				if rec["id"] == "s1" {
					order = append(order, "reply")
				}
			case "extension_ui_request":
				if rec["method"] == "notify" {
					order = append(order, "effect")
				}
			}
		case <-deadline:
			t.Fatalf("timed out waiting for [effect reply], got %v", order)
		}
	}
	select {
	case err := <-barrierErr:
		t.Fatalf("required barrier failed: %v", err)
	default:
	}
	requireClosedSignal(t, ext2NotifyArrived, "ext2NotifyArrived (ui.notify)")
	want := []string{"effect", "reply"}
	if !slices.Equal(order, want) {
		t.Fatalf("order = %v, want %v (second handler after synchronous first handler must not be sealed)", order, want)
	}
}

type funcWriter func([]byte) (int, error)

func (f funcWriter) Write(p []byte) (int, error) { return f(p) }

// TestRPCPostAckNotDeterminedCases verifies the three "not determined" questions from
// REVIEW-batch-second-rpc-reply-order-2026-11-26.md:
//   - plain `prompt` with an `input` handler in the same batch after set_session_name + setStatus + getContextUsage;
//   - `set_thinking_level` (which runs FlushEvents) + `prompt "/cmd"` in the same batch with setStatus + getContextUsage.
func TestRPCPostAckNotDeterminedCases(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_, _ = conn.Write([]byte("ok"))
			_ = conn.Close()
		}
	}()
	t.Setenv("RPC_ND_ADDR", ln.Addr().String())

	t.Run("plain-prompt-with-input-handler-after-set-session-name", func(t *testing.T) {
		extPath := filepath.Join(t.TempDir(), "nd-input.mjs")
		extSrc := `import net from "node:net";
function waitPeer() {
  const [host, port] = process.env.RPC_ND_ADDR.split(":");
  return new Promise((resolve, reject) => {
    const sock = net.connect(Number(port), host);
    sock.on("data", () => {});
    sock.on("end", () => resolve());
    sock.on("error", reject);
  });
}
export default function (pi) {
  pi.on("input", async () => ({ action: "handled" }));
  pi.on("session_info_changed", async (_event, ctx) => {
    await waitPeer();
    ctx.ui.setStatus("nd-key", "after-await");
    ctx.getContextUsage();
  });
}
`
		if err := os.WriteFile(extPath, []byte(extSrc), 0o600); err != nil {
			t.Fatal(err)
		}
		syncCallArrived := make(chan struct{})
		var syncOnce sync.Once
		barrierErr := make(chan error, 1)
		holdExt, err := factoryload.LoadExtensionFromFactory(func(api extension.API) error {
			api.OnThinkingLevelSelect(func(ctx context.Context, _ extension.ThinkingLevelSelectEvent) error {
				select {
				case <-syncCallArrived:
					return nil
				case <-time.After(testbudget.Wait(t)):
					err := errors.New("timed out waiting for getContextUsage sync call arrival barrier")
					select {
					case barrierErr <- err:
					default:
					}
					return err
				case <-ctx.Done():
					err := fmt.Errorf("context cancelled waiting for syncCallArrived barrier: %w", ctx.Err())
					select {
					case barrierErr <- err:
					default:
					}
					return err
				}
			})
			return nil
		}, ".", extension.CreateEventBus(), extension.CreateExtensionRuntime(), "test:hold-nd-input")
		if err != nil {
			t.Fatal(err)
		}
		h := startRPCInProcessHarness(t, rpcHarnessOptions{
			extensions:  []subprocess.ExtConfig{{Name: "nd-input", Source: extPath, Enabled: true}},
			extraInproc: []extension.Extension{holdExt},
			inboundObs: func(_ string, env *subprocess.Envelope) {
				if env != nil && env.Type == subprocess.MsgCall && env.Call != nil && env.Call.Method == "getContextUsage" {
					syncOnce.Do(func() { close(syncCallArrived) })
				}
			},
		})
		defer h.close()

		batch := `{"id":"r1","type":"set_session_name","name":"nd1"}` + "\n" +
			`{"id":"r2","type":"set_thinking_level","level":"high"}` + "\n" +
			`{"id":"r3","type":"prompt","message":"hello"}` + "\n"
		if _, err := io.WriteString(h.inW, batch); err != nil {
			t.Fatal(err)
		}
		deadline := time.After(testbudget.Wait(t))
		seen := map[string]bool{}
		for !seen["r1"] || !seen["r2"] || !seen["r3"] {
			select {
			case err := <-barrierErr:
				t.Fatalf("required barrier failed: %v", err)
			case rec := <-h.records:
				if rec["type"] == "response" {
					if id, _ := rec["id"].(string); id != "" {
						seen[id] = true
					}
				}
			case <-deadline:
				t.Fatalf("timed out waiting for plain-prompt input-handler batch replies (seen=%v)", seen)
			}
		}
		select {
		case err := <-barrierErr:
			t.Fatalf("required barrier failed: %v", err)
		default:
		}
		requireClosedSignal(t, syncCallArrived, "syncCallArrived (getContextUsage)")
	})

	t.Run("set-thinking-level-flush-events-with-held-status-and-sync-call", func(t *testing.T) {
		extPath := filepath.Join(t.TempDir(), "nd-thinking.mjs")
		extSrc := `import net from "node:net";
function waitPeer() {
  const [host, port] = process.env.RPC_ND_ADDR.split(":");
  return new Promise((resolve, reject) => {
    const sock = net.connect(Number(port), host);
    sock.on("data", () => {});
    sock.on("end", () => resolve());
    sock.on("error", reject);
  });
}
export default function (pi) {
  pi.registerCommand("cmd", { description: "cmd", handler: async () => {} });
  pi.on("thinking_level_select", async (_event, ctx) => {
    await waitPeer();
    ctx.ui.setStatus("nd-think", "after-await");
    ctx.getContextUsage();
  });
}
`
		if err := os.WriteFile(extPath, []byte(extSrc), 0o600); err != nil {
			t.Fatal(err)
		}
		syncCallArrived := make(chan struct{})
		var syncOnce sync.Once
		barrierErr := make(chan error, 1)
		holdExt, err := factoryload.LoadExtensionFromFactory(func(api extension.API) error {
			api.OnSessionInfoChanged(func(ctx context.Context, _ extension.SessionInfoChangedEvent) error {
				select {
				case <-syncCallArrived:
					return nil
				case <-time.After(testbudget.Wait(t)):
					err := errors.New("timed out waiting for getContextUsage sync call arrival barrier")
					select {
					case barrierErr <- err:
					default:
					}
					return err
				case <-ctx.Done():
					err := fmt.Errorf("context cancelled waiting for syncCallArrived barrier: %w", ctx.Err())
					select {
					case barrierErr <- err:
					default:
					}
					return err
				}
			})
			return nil
		}, ".", extension.CreateEventBus(), extension.CreateExtensionRuntime(), "test:hold-nd-think")
		if err != nil {
			t.Fatal(err)
		}
		h := startRPCInProcessHarness(t, rpcHarnessOptions{
			extensions:  []subprocess.ExtConfig{{Name: "nd-thinking", Source: extPath, Enabled: true}},
			extraInproc: []extension.Extension{holdExt},
			inboundObs: func(_ string, env *subprocess.Envelope) {
				if env != nil && env.Type == subprocess.MsgCall && env.Call != nil && env.Call.Method == "getContextUsage" {
					syncOnce.Do(func() { close(syncCallArrived) })
				}
			},
		})
		defer h.close()

		batch := `{"id":"r1","type":"set_thinking_level","level":"high"}` + "\n" +
			`{"id":"r2","type":"set_session_name","name":"hold-window"}` + "\n" +
			`{"id":"r3","type":"prompt","message":"/cmd"}` + "\n"
		if _, err := io.WriteString(h.inW, batch); err != nil {
			t.Fatal(err)
		}
		deadline := time.After(testbudget.Wait(t))
		seen := map[string]bool{}
		for !seen["r1"] || !seen["r2"] || !seen["r3"] {
			select {
			case err := <-barrierErr:
				t.Fatalf("required barrier failed: %v", err)
			case rec := <-h.records:
				if rec["type"] == "response" {
					if id, _ := rec["id"].(string); id != "" {
						seen[id] = true
					}
				}
			case <-deadline:
				t.Fatalf("timed out waiting for set_thinking_level batch replies (seen=%v)", seen)
			}
		}
		select {
		case err := <-barrierErr:
			t.Fatalf("required barrier failed: %v", err)
		default:
		}
		requireClosedSignal(t, syncCallArrived, "syncCallArrived (getContextUsage)")
	})
}
