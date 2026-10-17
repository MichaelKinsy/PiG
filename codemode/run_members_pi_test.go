package codemode_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/codemode"
)

// A run through Sandbox.Start with every member of CodemodeRunOptions, CodemodeDeclaredTool and CodemodeDeclaredGlobal,
// its calls answered through CodemodeRun.deliver and its outcome read from CodemodeRun.finished. Pi:
// CodemodeDeclaredTool name packages/codemode/src/types.ts:163, jsName packages/codemode/src/types.ts:168, description
// packages/codemode/src/types.ts:169; CodemodeDeclaredGlobal name packages/codemode/src/types.ts:174, spread
// packages/codemode/src/types.ts:175; CodemodeRunOptions startedAt packages/codemode/src/types.ts:194, seed
// packages/codemode/src/types.ts:196, tools packages/codemode/src/types.ts:197, globals packages/codemode/src/types.ts:198,
// store packages/codemode/src/types.ts:199, timeoutMs packages/codemode/src/types.ts:202; CodemodeRun finished
// packages/codemode/src/types.ts:221 and deliver packages/codemode/src/types.ts:228; CodemodeSandboxBase.start
// packages/codemode/src/runtime/execution.ts:455.
func TestRunOptionsMembersAndRunDeliverFinishedMatchPi(t *testing.T) {
	forEachBackend(t, func(t *testing.T, _ string, open func(codemode.SandboxOptions) *codemode.Sandbox) {
		sandbox := open(codemode.SandboxOptions{})
		code := `const start = Date.now();
			const a = await tools.renamed(1);
			const b = await search("q", 2);
			return { a, b, stored: load("k"), tool: ALL_TOOLS.map((tool) => [tool.name, tool.description]), random: Math.random(), start, now: Date.now() };`
		start := func(seed uint32) codemode.Result {
			timeout := 20_000.0
			var run *codemode.Run
			calls := make(chan codemode.HostCall, 4)
			done := make(chan struct{})
			// The answering goroutine reads run, which Start returns after the first call can already be queued; it waits
			// until the test goroutine has stored it.
			haveRun := make(chan struct{})
			go func() {
				defer close(done)
				<-haveRun
				seq := 0
				for call := range calls {
					seq++
					if call.Name != "my-tool" && call.Name != "search" {
						t.Errorf("call %+v", call)
					}
					value := call.Args
					if call.Name == "search" && string(call.Args) != `["q",2]` {
						t.Errorf("a spread global got %s, want all its arguments", call.Args)
					}
					if call.Name == "search" {
						value = json.RawMessage(`["found"]`)
					}
					if err := run.Deliver(seq, startedAt+float64(seq), call.ID, codemode.Outcome{OK: true, Value: value}); err != nil {
						t.Errorf("deliver %d: %v", seq, err)
					}
				}
			}()
			started := make(chan struct{})
			var err error
			run, err = sandbox.Start(t.Context(), code, codemode.RunOptions{
				StartedAt: startedAt, Seed: seed, TimeoutMs: &timeout,
				Tools:     []codemode.DeclaredTool{{Name: "my-tool", JSName: "renamed", Description: "adds"}},
				Globals:   []codemode.DeclaredGlobal{{Name: "search", Spread: true}},
				Store:     map[string]json.RawMessage{"k": json.RawMessage(`"v"`)},
				OnStarted: func(string) { close(started) },
				OnCall: func(call codemode.HostCall) {
					calls <- call
				},
			})
			close(haveRun)
			if err != nil {
				t.Fatal(err)
			}
			<-started
			result := run.Result()
			close(calls)
			<-done
			if !run.Finished() {
				t.Error("the run is not finished after its result")
			}
			return result
		}
		first, same, other := start(7), start(7), start(8)
		var value struct {
			A      json.RawMessage `json:"a"`
			B      json.RawMessage `json:"b"`
			Stored string          `json:"stored"`
			Tool   [][]string      `json:"tool"`
			Random float64         `json:"random"`
			Now    float64         `json:"now"`
			Start  float64         `json:"start"`
		}
		if !first.OK {
			t.Fatalf("result %+v", first)
		}
		if err := json.Unmarshal(first.Value, &value); err != nil {
			t.Fatal(err)
		}
		// The script calls the tool by its jsName; the host sees the tool's own name.
		if string(value.A) != "[1]" && string(value.A) != "1" || string(value.B) != `["found"]` || value.Stored != "v" {
			t.Errorf("value %s", first.Value)
		}
		if len(value.Tool) != 1 || value.Tool[0][0] != "renamed" || value.Tool[0][1] != "adds" {
			t.Errorf("ALL_TOOLS = %v", value.Tool)
		}
		if string(first.Value) != string(same.Value) || string(first.Value) == string(other.Value) {
			t.Errorf("the seed does not decide the run: %s / %s / %s", first.Value, same.Value, other.Value)
		}
		// The VM clock starts at StartedAt and then reads the time each answer is delivered with.
		if value.Start != startedAt || value.Now < startedAt+2 {
			t.Errorf("Date.now() = %v at the start and %v at the end, want %v and at least %v", value.Start, value.Now, startedAt, startedAt+2)
		}

		timeout := 100.0
		hung, err := sandbox.Start(t.Context(), "while (true) {}", codemode.RunOptions{StartedAt: startedAt, TimeoutMs: &timeout})
		if err != nil {
			t.Fatal(err)
		}
		if result := hung.Result(); result.OK || !strings.Contains(result.Error.Message, "timed out") || !hung.Finished() {
			t.Errorf("timeout result %+v finished %v", result, hung.Finished())
		}
	})
}
