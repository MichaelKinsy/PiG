package extensionconformance

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Pi awaits runner.emit({ type: "session_compact" }) after the compaction entry is saved (agent-session.ts:2943-2951 manual, 3269-3277 auto, Pi 1.5.0), and emit() takes no signal and has no abort check (runner.ts:1096-1126): every extension receives the event, in order, even when abortCompaction() is called while an earlier handler runs, and the compaction still completes.
// The Node rows use a first extension that blocks inside its handler until the test aborts the compaction; strict runs each extension in its own process, shared-ok packs them into one cell.
func TestNodeSessionCompactReachesTheNextExtensionWhenAbortedDuringDispatch(t *testing.T) {
	eachNodeAPIIsolation(t, func(t *testing.T, isolation string) {
		release := filepath.Join(t.TempDir(), "release")
		rig := newNodeSessionRig(t, isolation, `"compaction":{"keepRecentTokens":1}`, []ai.FauxModelDefinition{{ID: "faux-1"}},
			nodeAPIFixture{"compact-first", `import { existsSync } from "node:fs";
export default function (pi) {
  pi.on("session_compact", async (event, ctx) => {
    ctx.ui.notify("first-entered", "info");
    while (!existsSync(` + strconv.Quote(release) + `)) await new Promise((resolve) => setTimeout(resolve, 5));
    ctx.ui.notify("first-done", "info");
  });
}
`},
			nodeAPIFixture{"compact-second", `export default function (pi) {
  pi.on("session_compact", async (event, ctx) => ctx.ui.notify("second:" + event.reason + ":" + event.fromExtension, "info"));
  pi.on("session_compact_failed", async (event, ctx) => ctx.ui.notify("failed:" + event.reason, "info"));
}
`})
		rig.faux.SetResponses([]ai.FauxResponseStep{fauxTextStep("one"), fauxTextStep("two"), fauxTextStep("three"), fauxTextStep("summary"), fauxTextStep("prefix summary")})
		for _, text := range []string{"a", "b", "c"} {
			if err := rig.session.Prompt(t.Context(), text); err != nil {
				t.Fatal(err)
			}
		}

		type outcome struct {
			result *coding.CompactionResult
			err    error
		}
		done := make(chan outcome, 1)
		go func() {
			result, err := rig.session.Compact(t.Context(), "")
			done <- outcome{result, err}
		}()
		deadline := time.After(testbudget.Wait(t))
		for !slices.Contains(rig.notifications(), "first-entered") {
			select {
			case early := <-done:
				t.Fatalf("Compact returned before the first handler ran: %+v, %v", early.result, early.err)
			case <-deadline:
				t.Fatalf("the first extension's session_compact handler never ran; notifications = %v", rig.notifications())
			case <-time.After(10 * time.Millisecond):
			}
		}
		rig.session.AbortCompaction()
		if err := os.WriteFile(release, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		var got outcome
		select {
		case got = <-done:
		case <-time.After(testbudget.Wait(t)):
			t.Fatalf("compaction did not finish after the first handler was released; notifications = %v", rig.notifications())
		}
		if got.err != nil || got.result == nil || got.result.Summary == "" {
			t.Fatalf("Compact = %+v, %v; want the saved compaction", got.result, got.err)
		}
		waitFor(t, func() bool { return slices.Contains(rig.notifications(), "second:manual:false") })
		if notes := rig.notifications(); slices.Contains(notes, "failed:manual") {
			t.Fatalf("session_compact_failed after a saved compaction: %v", notes)
		}
		saved := false
		for _, entry := range rig.session.Entries() {
			saved = saved || entry.Base().Type == "compaction"
		}
		if !saved {
			t.Fatal("the compaction entry was not saved")
		}
	})
}

// The same delivery through every SDK and both placements: the first extension is in-process and aborts the compaction from inside its session_compact handler, the second is the SDK's own fixture, which reports the event it received.
func TestSessionCompactIsDeliveredToEverySDKWhenAbortedDuringDispatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	cases := sdkHarnessCases()
	for _, language := range []string{"go", "python", "rust"} {
		cases = append(cases, harnessCase{name: "packed-" + language, make: func(t *testing.T) *harness { return makePackedUIHarness(t, language) }})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			home := t.TempDir()
			t.Setenv("PIG_HOME", home)
			agentDir := filepath.Join(home, "agent")
			if err := os.MkdirAll(agentDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"compaction":{"keepRecentTokens":1}}`), 0o644); err != nil {
				t.Fatal(err)
			}
			services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(services.Close)
			if err := services.Auth().Set("faux", ai.Credential{Type: ai.CredentialAPIKey, Key: "faux-key"}); err != nil {
				t.Fatal(err)
			}

			var session *coding.Session
			var aborted atomic.Bool
			first := extension.Extension{Path: "compact-first", Handlers: map[string][]extension.HandlerFn{
				"session_before_compact": {func(args ...any) (any, error) {
					prep := args[0].(extension.SessionBeforeCompactEvent).Preparation
					return extension.SessionBeforeCompactResult{Compaction: &extension.CompactionResult{
						Summary: "extension summary", FirstKeptEntryID: prep.FirstKeptEntryID, TokensBefore: prep.TokensBefore,
					}}, nil
				}},
				"session_compact": {func(...any) (any, error) {
					session.AbortCompaction()
					aborted.Store(true)
					return nil, nil
				}},
			}}
			runner := inproc.NewRunner(append([]extension.Extension{first}, h.host.Extensions()...), t.TempDir(), h.host.Runtime())
			provider := ai.NewFauxProvider(ai.FauxConfig{ProviderID: "faux", Models: []ai.FauxModelDefinition{{ID: "faux-1"}}})
			if err := services.ModelRuntime().RegisterNativeProvider(provider.Provider()); err != nil {
				t.Fatal(err)
			}
			provider.SetResponses([]ai.FauxResponseStep{fauxTextStep("one"), fauxTextStep("two"), fauxTextStep("three")})
			session, err = coding.NewSession(services, coding.SessionOptions{Model: services.ModelRuntime().GetModel("faux", "faux-1"), Runner: runner, SkipBuiltinTools: true})
			if err != nil {
				t.Fatal(err)
			}
			events := make(chan struct{})
			go func() {
				defer close(events)
				for event := range session.Events() {
					coding.AcknowledgeEvent(event)
				}
			}()
			t.Cleanup(func() { _ = session.Close(); <-events })
			ctx := testbudget.Context(t)
			for _, text := range []string{"a", "b", "c"} {
				if err := session.Prompt(ctx, text); err != nil {
					t.Fatal(err)
				}
			}

			h.ui.ClearRecorded()
			result, err := session.Compact(ctx, "")
			if err != nil || result == nil || result.Summary != "extension summary" {
				t.Fatalf("Compact = %+v, %v; want the saved compaction", result, err)
			}
			if !aborted.Load() {
				t.Fatal("the first extension did not abort during dispatch")
			}
			waitFor(t, func() bool { return slices.Contains(h.ui.Recorded(), "session_compact:manual:false:true:info") })
			for _, line := range h.ui.Recorded() {
				if strings.HasPrefix(line, "session_compact_failed:") {
					t.Fatalf("session_compact_failed after a saved compaction: %v", h.ui.Recorded())
				}
			}
		})
	}
}
