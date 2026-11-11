package status

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

func graph(tasks ...*delta.JsonObject) *delta.JsonObject {
	nodes := delta.NewJsonObject(len(tasks))
	for i, task := range tasks {
		nodes.Set(string(rune('a'+i)), task)
	}
	return delta.JsonObjectOf("tasks", nodes)
}

func node(conversation int, background bool) *delta.JsonObject {
	return delta.JsonObjectOf("conversationId", float64(conversation), "background", background, "kind", "pi.generation")
}

func TestSummarizeCountsEveryLiveTaskAndListsConversationsWithForegroundWork(t *testing.T) {
	tasks, busy := summarize(graph(node(3, false), node(1, false), node(3, false), node(2, true)))
	if tasks != 4 {
		t.Fatalf("tasks = %d", tasks)
	}
	// A background reporter does not make a conversation busy; each busy conversation once, ascending.
	encoded, _ := chordjson.Marshal(busyList(busy))
	if string(encoded) != "[1,3]" || !busy[3]["pi.generation"] {
		t.Fatalf("busy = %s", encoded)
	}
	if tasks, busy := summarize(delta.JsonObjectOf("tasks", delta.NewJsonObject(0))); tasks != 0 || len(busy) != 0 {
		t.Fatalf("an empty graph: %d %v", tasks, busy)
	}
}

func usage(input, output int, cost float64) ai.Usage {
	return ai.Usage{Input: input, Output: output, CacheRead: 1, CacheWrite: 2, TotalTokens: input + output + 3,
		Cost: ai.UsageCost{Input: cost, Output: cost, CacheRead: cost, CacheWrite: cost, Total: 4 * cost}}
}

func TestTotalUsageSumsEveryModelAndToolBucket(t *testing.T) {
	if got := totalUsage(harness.NewUsageState()); got != nil {
		t.Fatalf("no spend yet: %+v", got)
	}
	totals := harness.UsageState{
		Models: map[string]ai.Usage{"faux/a": usage(10, 5, 0.5), "faux/b": usage(1, 1, 0.25)},
		Tools:  map[string]ai.Usage{"read": usage(2, 0, 0.125)},
	}
	got := totalUsage(totals)
	if got == nil || got.Input != 13 || got.Output != 6 || got.CacheRead != 3 || got.CacheWrite != 6 || got.TotalTokens != 13+6+9 {
		t.Fatalf("usage = %+v", got)
	}
	if got.Cost.Input != 0.875 || got.Cost.Total != 3.5 {
		t.Fatalf("cost = %+v", got.Cost)
	}
}
