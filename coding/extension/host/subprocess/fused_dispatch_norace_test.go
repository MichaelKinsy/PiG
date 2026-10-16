//go:build !race

package subprocess

import (
	"context"
	"fmt"
	"math"
	"runtime"
	"runtime/debug"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/boundarywire"
	"github.com/MichaelKinsy/PiG/internal/perfgate"
)

// fusedDispatchHistory is the session history of the fused dispatch gate, in entries (each one user message).
const fusedDispatchHistory = 40

// fusedDispatchRig is K fused Go extensions, each with a before_agent_start and a turn_end handler that read their event, as pi-arena's rpc-ext10 extensions do, and the boundary preview of a session of fusedDispatchHistory entries.
type fusedDispatchRig struct {
	runner    *inproc.Runner
	entries   []extension.ProjectedSessionEntry
	sources   []json.RawMessage
	messages  []extension.AgentMessage
	encodings boundarywire.Encodings
}

func newFusedDispatchRig(t testing.TB, k int) *fusedDispatchRig {
	t.Helper()
	host := NewHost(t.TempDir())
	t.Cleanup(func() { host.Shutdown("test complete") })
	loaded := make([]extension.Extension, k)
	for i := range k {
		ext := sdk.New(fmt.Sprintf("fused-%02d", i))
		events := 0
		for _, name := range []string{"before_agent_start", "turn_end"} {
			ext.OnEvent(name, func(_ sdk.Context, data map[string]any) (any, error) {
				if data["type"] == name {
					events++
				}
				return nil, nil
			})
		}
		member, err := host.LoadInProcess(t.Context(), ExtConfig{Name: fmt.Sprintf("fused-%02d", i), Enabled: true}, ext.RunWithConn)
		if err != nil {
			t.Fatal(err)
		}
		loaded[i] = *member
	}
	rig := &fusedDispatchRig{runner: inproc.NewRunner(loaded, t.TempDir())}
	for i, entry := range historyEntries(fusedDispatchHistory) {
		message := extension.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserText(fmt.Sprintf("message %d with some text <&>", i)), Timestamp: int64(i)}}
		rig.entries = append(rig.entries, extension.ProjectedSessionEntry{Messages: []extension.AgentMessage{message}})
		rig.sources = append(rig.sources, entry.Raw())
		rig.messages = append(rig.messages, message)
	}
	return rig
}

// turn runs the two dispatches of an RPC turn with extensions: before_agent_start, then the turn_end boundary, whose preview the session builds from unchanged state for each handler (Session.recordedBoundaryContext).
func (r *fusedDispatchRig) turn(t testing.TB) {
	options := extension.BuildSystemPromptOptions{Cwd: "/work", SelectedTools: []string{"read", "bash"}}
	if _, err := r.runner.EmitBeforeAgentStart(context.Background(), extension.UserPromptTrigger("What is 20+22?", nil), options); err != nil {
		t.Fatal(err)
	}
	ctx, wire := boundarywire.WithSessionCache(context.Background(), &r.encodings)
	key := boundarywire.Key{Session: r, Revision: 1}
	build := func([]extension.SessionBoundaryDraft) (extension.BoundaryContextPreview, error) {
		preview := extension.BoundaryContextPreview{ContextEntries: r.entries, ContextMessages: r.messages, LLMMessages: []ai.Message{}, PendingMessages: []extension.AgentMessage{}}
		wire.Built(preview, key, true, r.sources)
		wire.Shared()
		return preview, nil
	}
	answer := extension.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: "42"}}, StopReason: ai.StopReasonStop}}
	event := extension.TurnEndEvent{Type: "turn_end", Message: answer, ToolResults: []extension.ToolResultMessage{}, ToolResultEntryIds: []string{}, MessageEntryID: "answer", BoundaryState: &extension.BoundaryState{Entries: []extension.SessionBoundaryDraft{}, Outcome: extension.AgentActivityCompleted}}
	if _, err := r.runner.EmitBoundary(ctx, event, build); err != nil {
		t.Fatal(err)
	}
}

// A fused Piglet's extensions run in the pig process (D31). The allocations of one RPC turn's extension dispatches (before_agent_start and turn_end over a history of fusedDispatchHistory entries) at K = 1, 3 and 10 fused extensions, and what each extension adds: the cost a fused member pays to receive its events. Pi hands its in-process handlers the event objects themselves.
func TestFusedDispatchAllocations(t *testing.T) {
	allocs, bytes := map[int]float64{}, map[int]float64{}
	for _, k := range []int{1, 3, 10} {
		rig := newFusedDispatchRig(t, k)
		rig.turn(t)
		allocs[k], bytes[k] = turnCost(func() { rig.turn(t) })
		perfgate.Check(t, fmt.Sprintf("fused.dispatch_k%d.allocs", k), allocs[k])
		perfgate.Check(t, fmt.Sprintf("fused.dispatch_k%d.bytes", k), bytes[k])
	}
	perfgate.Check(t, "fused.dispatch.per_extension.allocs", (allocs[10]-allocs[1])/9)
	perfgate.Check(t, "fused.dispatch.per_extension.bytes", (bytes[10]-bytes[1])/9)
}

// turnCost is the allocations and allocated bytes of one run, over every goroutine (the fused extensions run on their own),
// as testing.AllocsPerRun measures allocations: the minimum over turnCostSamples samples of turnCostRuns runs each. Another
// goroutine allocating during a sample (a runtime timer, a leftover of an earlier test) only raises that sample, so the
// minimum is the dispatch's own floor; a mean charged such a transient to the dispatch (Windows CI measured k3 at 3424
// against a 3010 ceiling while k1 and k10 sat under theirs).
func turnCost(run func()) (allocs, bytes float64) {
	const turnCostSamples, turnCostRuns = 5, 5
	defer debug.SetGCPercent(debug.SetGCPercent(-1))
	allocs, bytes = math.Inf(1), math.Inf(1)
	for range turnCostSamples {
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		for range turnCostRuns {
			run()
		}
		runtime.ReadMemStats(&after)
		allocs = min(allocs, float64(after.Mallocs-before.Mallocs)/turnCostRuns)
		bytes = min(bytes, float64(after.TotalAlloc-before.TotalAlloc)/turnCostRuns)
	}
	return allocs, bytes
}

// BenchmarkFusedDispatch is TestFusedDispatchAllocations' turn at K = 10, for profiles.
func BenchmarkFusedDispatch(b *testing.B) {
	rig := newFusedDispatchRig(b, 10)
	b.ReportAllocs()
	for b.Loop() {
		rig.turn(b)
	}
}
