// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

func decodeOps(t *testing.T, text string) []durable.Op {
	t.Helper()
	var ops delta.Ops
	if err := json.Unmarshal([]byte(text), &ops); err != nil {
		t.Fatal(err)
	}
	return ops
}

func decodedValue(t *testing.T, text string) any {
	t.Helper()
	v, err := delta.DecodeJson([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// step applies ops by the reducer and by the full conversion and requires the same typed value (compared by its JSON form,
// which is what the full conversion reads).
func step[T any](t *testing.T, label string, prev T, ops []durable.Op, reduce reducer[T]) T {
	t.Helper()
	want, ok := fullConvert(prev, ops, typed[T])
	if !ok {
		t.Fatalf("%s: full conversion failed for %s", label, mustJSON(t, delta.Ops(ops)))
	}
	got, ok := reduce(prev, ops)
	if !ok {
		t.Fatalf("%s: reducer refused %s", label, mustJSON(t, delta.Ops(ops)))
	}
	if g, w := mustJSON(t, got), mustJSON(t, want); g != w {
		t.Fatalf("%s: ops %s\n got  %s\n want %s", label, mustJSON(t, delta.Ops(ops)), g, w)
	}
	return got
}

// Random view frames (entries appended, head markers, documents set, changed inside and deleted) give the typed view the full
// conversion gives, and a value the reducer returned earlier is never changed by a later frame.
func TestViewReducerMatchesFullConversion(t *testing.T) {
	for seed := int64(1); seed <= 30; seed++ {
		rng := rand.New(rand.NewSource(seed))
		view := typedOf[harness.ConversationView](t, `{"conversation":{"id":1,"createdAt":1},"entries":[],"docs":{"pi.live":{"run":null}}}`)
		reduce := viewReducer()
		next := 1
		kinds := []string{"pi.live", "pi.agent", "pi.inbox"}
		var past []harness.ConversationView
		var pastJSON []string
		for i := range 150 {
			var ops []string
			for range 1 + rng.Intn(3) {
				switch rng.Intn(6) {
				case 0, 1, 2:
					ops = append(ops, fmt.Sprintf(`["p",["entries"],%d,0,[{"id":%d,"conversationId":1,"kind":"m"}]]`, len(view.Entries), next))
					next++
				case 3:
					keep := 0
					if len(view.Entries) > 0 {
						keep = rng.Intn(len(view.Entries) + 1)
					}
					ops = append(ops, fmt.Sprintf(`["p",["entries"],0,%d,[{"id":%d,"conversationId":1,"kind":"pi.head","head":%d}]]`, keep, next, 1+rng.Intn(next)))
					next++
				case 4:
					kind := kinds[rng.Intn(len(kinds))]
					if rng.Intn(3) == 0 {
						if _, present := view.Docs.Get(kind); present {
							ops = append(ops, fmt.Sprintf(`["d",["docs",%q]]`, kind))
						}
					} else {
						ops = append(ops, fmt.Sprintf(`["s",["docs",%q],{"n":%d,"inner":{"x":1}}]`, kind, i))
					}
				default:
					for _, kind := range kinds {
						if _, present := view.Docs.Get(kind); present {
							ops = append(ops, fmt.Sprintf(`["s",["docs",%q,"n"],%d]`, kind, i), fmt.Sprintf(`["s",["docs",%q,"fresh"],"v"]`, kind))
							break
						}
					}
				}
			}
			// Ops read the value after the earlier ops of the same frame; keep a frame to what one value supports.
			frame := decodeOps(t, "["+strings.Join(ops, ",")+"]")
			if _, ok := fullConvert(view, frame, typed[harness.ConversationView]); !ok {
				continue
			}
			past = append(past, view)
			pastJSON = append(pastJSON, mustJSON(t, view))
			view = step(t, fmt.Sprintf("seed %d frame %d", seed, i), view, frame, reduce)
		}
		for i, v := range past {
			if mustJSON(t, v) != pastJSON[i] {
				t.Fatalf("seed %d: a value delivered earlier changed", seed)
			}
		}
	}
}

func typedOf[T any](t *testing.T, text string) T {
	t.Helper()
	v, err := typed[T](decodedValue(t, text))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// Random graph frames give the typed graph the full conversion gives.
func TestTaskGraphReducerMatchesFullConversion(t *testing.T) {
	node := func(id int, status string) string {
		return fmt.Sprintf(`{"id":%d,"kind":"k","conversationId":1,"background":false,"abortRequested":false,"state":{"status":%q,"phase":"start"},"conversations":[]}`, id, status)
	}
	for seed := int64(1); seed <= 30; seed++ {
		rng := rand.New(rand.NewSource(seed))
		graph := typedOf[harness.TaskGraph](t, `{"tasks":{}}`)
		var live []int
		next := 1
		var past []harness.TaskGraph
		var pastJSON []string
		for i := range 200 {
			var ops []string
			switch r := rng.Intn(5); {
			case r < 2 || len(live) == 0:
				ops = append(ops, fmt.Sprintf(`["s",["tasks","%d"],%s]`, next, node(next, []string{"pending", "running"}[rng.Intn(2)])))
				live = append(live, next)
				next++
			case r == 2:
				k := rng.Intn(len(live))
				ops = append(ops, fmt.Sprintf(`["d",["tasks","%d"]]`, live[k]))
				live = append(live[:k], live[k+1:]...)
			case r == 3:
				ops = append(ops, fmt.Sprintf(`["s",["tasks","%d","conversations"],[%d,%d]]`, live[rng.Intn(len(live))], 2*i, 2*i+1))
			default:
				id := live[rng.Intn(len(live))]
				if rng.Intn(2) == 0 {
					ops = append(ops, fmt.Sprintf(`["s",["tasks","%d","blocked"],"missing_task"]`, id))
				} else {
					ops = append(ops, fmt.Sprintf(`["s",["tasks","%d","blocked"],"restart_limit"]`, id), fmt.Sprintf(`["d",["tasks","%d","blocked"]]`, id))
				}
			}
			past = append(past, graph)
			pastJSON = append(pastJSON, mustJSON(t, graph))
			graph = step(t, fmt.Sprintf("seed %d frame %d", seed, i), graph, decodeOps(t, "["+strings.Join(ops, ",")+"]"), reduceTaskGraph)
		}
		for i, v := range past {
			if mustJSON(t, v) != pastJSON[i] {
				t.Fatalf("seed %d: a value delivered earlier changed", seed)
			}
		}
	}
}

// An op the reducer does not know is reported, so the mount converts the whole value.
func TestReducersRefuseUnknownOps(t *testing.T) {
	if _, ok := reduceTaskGraph(harness.TaskGraph{}, decodeOps(t, `[["s",["other"],1]]`)); ok {
		t.Fatal("the graph reducer took an unknown op")
	}
	if _, ok := viewReducer()(harness.ConversationView{}, decodeOps(t, `[["s",["conversation","x"],1]]`)); ok {
		t.Fatal("the view reducer took an unknown op")
	}
}

func allocatedBytes(fn func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	fn()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// A view frame that appends an entry allocates the same at 2,000 entries as at 8,000: the typed value is not encoded and decoded
// whole.
func TestViewFrameCostDoesNotGrowWithEntries(t *testing.T) {
	cost := func(n int) uint64 {
		var entries []string
		for i := range n {
			entries = append(entries, fmt.Sprintf(`{"id":%d,"conversationId":1,"kind":"m"}`, i+1))
		}
		view := typedOf[harness.ConversationView](t, `{"conversation":{"id":1},"entries":[`+strings.Join(entries, ",")+`],"docs":{}}`)
		reduce := viewReducer()
		frames := make([][]durable.Op, 21)
		for i := range frames {
			frames[i] = decodeOps(t, fmt.Sprintf(`[["p",["entries"],%d,0,[{"id":%d,"conversationId":1,"kind":"m"}]]]`, n+i, n+i+1))
		}
		view, _ = reduce(view, frames[0]) // the first append copies the entries into the slice the reducer owns
		return allocatedBytes(func() {
			for _, ops := range frames[1:] {
				view, _ = reduce(view, ops)
			}
		})
	}
	small, large := cost(2000), cost(8000)
	t.Logf("20 view frames: 2000 entries %d B, 8000 entries %d B", small, large)
	if float64(large) > 1.5*float64(small) {
		t.Fatalf("4x the entries allocates %.1fx the bytes per frame", float64(large)/float64(small))
	}
}

// A graph frame costs one copy of the task map (Pi copies the graph's record too; the map holds node values, so Go allocates one
// object per task for it) plus the node it changes: a few allocations per live task, where encoding and decoding the whole graph
// cost dozens per task.
func TestGraphFrameCostsOneMapCopy(t *testing.T) {
	cost := func(n int) float64 {
		tasks := make([]string, n)
		for i := range n {
			tasks[i] = fmt.Sprintf(`"%d":{"id":%d,"kind":"k","conversationId":1,"background":false,"abortRequested":false,"state":{"status":"pending","phase":"start"},"conversations":[]}`, i+1, i+1)
		}
		graph := typedOf[harness.TaskGraph](t, `{"tasks":{`+strings.Join(tasks, ",")+`}}`)
		ops := decodeOps(t, `[["s",["tasks","1","conversations"],[2]]]`)
		return testing.AllocsPerRun(20, func() { graph, _ = reduceTaskGraph(graph, ops) })
	}
	for _, n := range []int{1000, 8000} {
		got := cost(n)
		t.Logf("allocations per graph frame at %d live tasks: %.0f", n, got)
		if got > 1.2*float64(n)+100 {
			t.Fatalf("%d live tasks: %.0f allocations per frame, more than one map copy", n, got)
		}
	}
}
