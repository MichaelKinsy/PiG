package harness

import (
	"context"
	"encoding/json"
	"math"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// Ports the Harness cases of packages/durable/test/session-document-retention.test.ts and the documentRetentionMs
// resolution of src/harness/agent.ts resolveSettings: the Session's document cache follows the Harness settings.

type retentionCounter struct {
	Count int `json:"count"`
}

var retentionTestDoc = durable.DefineDoc(durable.DocDefinition[retentionCounter]{
	CommonDocDefinition: durable.CommonDocDefinition[retentionCounter]{
		Kind: "test.retention.harness", Version: 1,
		Initial:        func() retentionCounter { return retentionCounter{} },
		CheckpointWhen: func(retentionCounter, []durable.Op, durable.CheckpointInfo) bool { return false },
	},
	DocumentSemantics: durable.DocumentSemantics{Scope: durable.ScopeConversation, History: durable.HistoryLatest, Fork: durable.ForkInitial},
})

func TestResolveSettingsDocumentRetention(t *testing.T) {
	// upstream: packages/durable/src/harness/agent.ts:resolveSettings
	for _, test := range []struct {
		name string
		ms   *float64
		want float64
	}{
		{"unset", nil, 600_000},
		{"zero drops right after use", new(0.0), 0},
		{"milliseconds", new(1500.0), 1500},
		{"infinity keeps documents", new(math.Inf(1)), math.Inf(1)},
		{"negative means the default", new(-5.0), 600_000},
		{"NaN means the default", new(math.NaN()), 600_000},
	} {
		if got := ResolveSettings(&HarnessSettings{DocumentRetentionMs: test.ms}).DocumentRetentionMs; got != test.want {
			t.Errorf("%s: DocumentRetentionMs = %v, want %v", test.name, got, test.want)
		}
	}
}

// Pi 1.2.0 packages/durable/test/session-document-retention.test.ts:477 "follows a Harness settings change at the next use"
func TestHarnessDocumentRetentionFollowsSettings(t *testing.T) {
	setup := chatSetup(t)
	// The case starts from the default retention, whatever PI_DURABLE_DOCUMENT_RETENTION_MS forces on the suite.
	setup.SetSettings(func(settings *HarnessSettings) { settings.DocumentRetentionMs = nil })
	harness, root := openChat(t, storage.NewMemoryStorage(), setup)
	t.Cleanup(func() {
		if err := harness.Close(testContext); err != nil {
			t.Error(err)
		}
	})
	kernel := harness.(*harnessImpl).SessionImpl
	if _, err := durable.Commit(testContext, root, func(tx durable.Tx) (struct{}, error) {
		draft, err := tx.Doc(retentionTestDoc, root.Id())
		if err != nil {
			return struct{}{}, err
		}
		return struct{}{}, draft.Set("count", 1)
	}); err != nil {
		t.Fatal(err)
	}
	if kernel.DocumentCacheSizes().Cached == 0 {
		t.Fatal("the document is cached under the ten-minute default")
	}
	setup.SetSettings(func(settings *HarnessSettings) { settings.DocumentRetentionMs = new(0.0) })
	value, err := durable.Snapshot[retentionCounter](testContext, harness, retentionTestDoc, root.Id())
	if err != nil || value == nil || value.Count != 1 {
		t.Fatalf("snapshot = %v, %v", value, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := kernel.ReadOnLine(func() (any, error) { return nil, nil }); err != nil {
			t.Fatal(err)
		}
		if kernel.DocumentCacheSizes().Cached == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("sizes %+v: the shortened retention evicts at the next use", kernel.DocumentCacheSizes())
		}
		time.Sleep(time.Millisecond)
	}
}

// packages/durable/test/session-document-retention.test.ts "gives the same views with documents evicted right after use as with
// documents kept": a run that streams a tool's output yields the same sequence of entries, run states, and tool outputs whether
// the Session drops every unattached document at once or keeps them ten minutes.
func TestTheViewsAreTheSameWithDocumentsEvictedRightAfterUseAsWithDocumentsKept(t *testing.T) {
	run := func(retentionMs float64) []string {
		setup := chatSetup(t)
		setup.SetSettings(func(settings *HarnessSettings) {
			settings.DocumentRetentionMs = &retentionMs
			settings.Progress = &ProgressPolicyPatch{OutputIntervalMs: new(0.0)}
		})
		// Each step waits for the update of the one before it. Upstream paces the steps with a 5 ms timer, which only keeps two
		// steps out of one commit while a commit takes less than 5 ms; under load here it does not, and the steps coalesce into
		// fewer updates in one run than the other. Waiting for the update delivered puts every step in a commit of its own.
		var history *viewRecording
		addTool(t, setup.Registry, tlTool("steps", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			shown := ""
			for _, step := range []string{"a\n", "b\n", "c\n"} {
				api.Output(step)
				shown += step
				for !slices.ContainsFunc(history.Live(), func(state LiveState) bool { return toolOutputIs(state, shown) }) {
					select {
					case <-ctx.Done():
						return durable.ToolExecutionResult{}, context.Cause(ctx)
					default:
						flush()
					}
				}
			}
			return durable.ToolExecutionResult{}, nil
		}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{
			tlCallsStep(tlCall{"steps", map[string]any{}, "s1"}),
			ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("done")}}),
		})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		history = viewHistory(t, harness, root.Id())
		must(submitInput(t, root, "go").Wait(testContext))
		if err := harness.WaitForIdle(testContext); err != nil {
			t.Fatal(err)
		}
		history.Detach()
		if err := harness.Close(testContext); err != nil {
			t.Fatal(err)
		}
		// Ids and times differ between runs; the sequence of entries, run states, and tool outputs must not.
		var out []string
		for _, view := range history.All() {
			live := liveValue(t, view)
			var tools []string
			for _, slot := range live.Tools {
				output := ""
				if slot.Output != nil {
					output = *slot.Output
				}
				tools = append(tools, fmt.Sprintf("%s:%s:%s", slot.CallId, slot.Status, output))
			}
			var kinds []string
			for _, entry := range view.Entries {
				kinds = append(kinds, entry.Kind)
			}
			out = append(out, fmt.Sprintf("%s run:%t generation:%t %s", strings.Join(kinds, ","), live.Run != nil, live.Generation != nil, strings.Join(tools, " ")))
		}
		return out
	}
	evicted, kept := run(0), run(600_000)
	if !slices.Equal(evicted, kept) {
		t.Fatalf("views with retention 0 = %v\nwith retention 600000 = %v", evicted, kept)
	}
	if !slices.ContainsFunc(evicted, func(view string) bool { return strings.Contains(view, "s1:running:a\nb\n") }) {
		t.Fatalf("views = %v, want the streamed tool output among them", evicted)
	}
}

// toolOutputIs reports whether a tool slot of state holds exactly output so far.
func toolOutputIs(state LiveState, output string) bool {
	return slices.ContainsFunc(state.Tools, func(slot ToolSlot) bool { return slot.Output != nil && *slot.Output == output })
}
