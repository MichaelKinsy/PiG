package status

import (
	"context"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/status/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// Ports apps/p2/plugins/status/worker.ts: the session worker's part of status: its Durable task graph, followed, and
// pushed to the server's index of statuses (the sidebar's working dot). The worker pushes; the server never asks.

// summarize is the live tasks, and the conversations with foreground work with their tasks' kinds: a background reporter
// does not make a conversation busy.
func summarize(graph *delta.JsonObject) (tasks int, busy map[float64]map[string]bool) {
	busy = map[float64]map[string]bool{}
	nodes, _ := graph.Value("tasks").(*delta.JsonObject)
	if nodes == nil {
		return 0, busy
	}
	for _, value := range nodes.All() {
		node, _ := value.(*delta.JsonObject)
		tasks++
		if background, _ := node.Value("background").(bool); background {
			continue
		}
		conversation, _ := node.Value("conversationId").(float64)
		if busy[conversation] == nil {
			busy[conversation] = map[string]bool{}
		}
		kind, _ := node.Value("kind").(string)
		busy[conversation][kind] = true
	}
	return tasks, busy
}

// busyList is the busy conversations, ascending.
func busyList(busy map[float64]map[string]bool) []any {
	ids := make([]float64, 0, len(busy))
	for conversation := range busy {
		ids = append(ids, conversation)
	}
	slices.Sort(ids)
	list := make([]any, len(ids))
	for at, id := range ids {
		list[at] = id
	}
	return list
}

// workOf is what each busy conversation does, by its ID: its live document's tool round (the calls not done yet) and
// run, else its tasks' kinds, which change in the commits that wake the report (a generation's presentation may follow
// later). `since` keeps when each became busy, across reports.
func workOf(ctx context.Context, opened harness.Harness, busy map[float64]map[string]bool, since map[float64]float64) (*delta.JsonObject, error) {
	for id := range since {
		if _, still := busy[id]; !still {
			delete(since, id)
		}
	}
	now := float64(time.Now().UnixMilli())
	work := delta.NewJsonObject(len(busy))
	for _, id := range busyList(busy) {
		conversation := id.(float64)
		started, known := since[conversation]
		if !known {
			started = now
		}
		since[conversation] = started
		live, err := durable.Snapshot(ctx, opened, harness.LiveDoc, durable.ConversationId(conversation))
		if err != nil {
			return nil, err
		}
		rounds := 0
		if live != nil && live.Run != nil {
			rounds = live.Run.Turns
		}
		work.Set(strconv.FormatFloat(conversation, 'f', -1, 64), delta.JsonObjectOf("since", started, "rounds", float64(rounds), "step", StepOf(live, busy[conversation])))
	}
	return work, nil
}

// StepOf is a busy conversation's step: its running tools first, then a compaction, the model's answer, other work.
func StepOf(live *harness.LiveState, kinds map[string]bool) *delta.JsonObject {
	if live != nil {
		var names []any
		for _, slot := range live.Tools {
			if slot.Status != harness.ToolSlotDone {
				names = append(names, slot.Name)
			}
		}
		if len(names) > 0 {
			return delta.JsonObjectOf("kind", "tools", "names", names)
		}
	}
	switch {
	case kinds["pi.compaction"]:
		return delta.JsonObjectOf("kind", "compacting")
	case kinds["pi.generation"]:
		return delta.JsonObjectOf("kind", "thinking")
	}
	return delta.JsonObjectOf("kind", "working")
}

// totalUsage is the session's spend: every model and tool bucket summed, or nil before the first one.
func totalUsage(totals harness.UsageState) *ai.Usage {
	if len(totals.Models)+len(totals.Tools) == 0 {
		return nil
	}
	var sum ai.Usage
	add := func(bucket ai.Usage) {
		sum.Input += bucket.Input
		sum.Output += bucket.Output
		sum.CacheRead += bucket.CacheRead
		sum.CacheWrite += bucket.CacheWrite
		sum.TotalTokens += bucket.TotalTokens
		sum.Cost.Input += bucket.Cost.Input
		sum.Cost.Output += bucket.Cost.Output
		sum.Cost.CacheRead += bucket.Cost.CacheRead
		sum.Cost.CacheWrite += bucket.Cost.CacheWrite
		sum.Cost.Total += bucket.Cost.Total
	}
	for _, bucket := range totals.Models {
		add(bucket)
	}
	for _, bucket := range totals.Tools {
		add(bucket)
	}
	return &sum
}

// Worker is the status plugin's worker facet.
var Worker = p2runtime.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	return facet.Serve(contract.StatusReport, host.LocalFactory(func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
		session := p2runtime.HarnessOf(self)
		worker := p2runtime.WorkerOf(self)
		status := self.Get(contract.SessionStatus, delta.NewJsonObject(0))
		graph := self.Follow(p2runtime.FollowDurable(func(ctx context.Context, observer durable.Observer[harness.TaskGraph]) (durable.Observation[harness.TaskGraph], error) {
			parts, err := session()
			if err != nil {
				return nil, err
			}
			opened, err := parts.Durable()
			if err != nil {
				return nil, err
			}
			return opened.ObserveTaskGraph(ctx, observer)
		}))
		// Pushes run one after another, each reading the graph when it starts.
		// When each busy conversation became busy, as the reports saw it.
		since := map[float64]float64{}
		var pushing sync.Mutex
		push := func(ctx context.Context) error {
			pushing.Lock()
			defer pushing.Unlock()
			// The graph is read in a section of the host's loop, as Pi's synchronous read of `graph.value`: its Durable
			// publications apply there, and a push runs from its own goroutine.
			var tasks int
			var busy map[float64]map[string]bool
			if err := host.InSection(ctx, func() { tasks, busy = summarize(graph.Value().(*delta.JsonObject)) }); err != nil {
				return err
			}
			parts, err := worker()
			if err != nil {
				return err
			}
			parts.Liveness.SetDurableTasks(tasks)
			held, err := session()
			if err != nil {
				return err
			}
			opened, err := held.Durable()
			if err != nil {
				return err
			}
			totals, err := opened.Usage(ctx)
			if err != nil {
				return err
			}
			work, err := workOf(ctx, opened, busy, since)
			if err != nil {
				return err
			}
			report := delta.JsonObjectOf("tasks", float64(tasks), "busy", busyList(busy), "work", work)
			if usage := totalUsage(totals); usage != nil {
				encoded, err := durable.ToJsonValue(usage)
				if err != nil {
					return err
				}
				report.Set("usage", encoded)
			}
			_, err = status.Call("report", report, ctx)
			return err
		}
		// Work the agent starts on its own (a turn continuing, a subagent's report) is pushed as the graph changes.
		self.On(graph, func(_ chord.WatchCall[delta.JsonValue], ctx host.Ctx) *host.Handled {
			return host.AwaitingCtx(ctx, func() error { return push(ctx) })
		})
		return contracts.Implementation{Members: contracts.VariantImplementation{
			Methods: map[string]contracts.MethodFunction{
				"push": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					// This waits for the followed Durable session: the graph's calls must not wait for this method's section.
					chordctx.Suspend(ctx)
					return nil, push(ctx)
				},
			},
		}}, nil
	}), host.ServeOptions{})
})
