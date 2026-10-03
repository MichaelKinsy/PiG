// Ports packages/durable/src/harness/usage.ts.

package harness

import (
	"fmt"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
)

// UsageBucket names one bucket of the usage ledger.
type UsageBucket string

const (
	// UsageModels holds assistant entries and summarization attempts, keyed `provider/modelId`.
	UsageModels UsageBucket = "models"
	// UsageTools holds tool results, keyed by tool name; their usage has no model identity.
	UsageTools UsageBucket = "tools"
)

// UsageState is the ledger of one conversation's own spend: its entries, and compaction summarization attempts,
// which have none.
type UsageState struct {
	// Models holds assistant entries and summarization attempts, keyed `provider/modelId`.
	Models map[string]ai.Usage `json:"models"`
	// Tools holds tool results, keyed by tool name.
	Tools map[string]ai.Usage `json:"tools"`
}

// NewUsageState returns an empty ledger.
func NewUsageState() UsageState {
	return UsageState{Models: map[string]ai.Usage{}, Tools: map[string]ai.Usage{}}
}

func (state *UsageState) bucket(name UsageBucket) map[string]ai.Usage {
	if name == UsageTools {
		if state.Tools == nil {
			state.Tools = map[string]ai.Usage{}
		}
		return state.Tools
	}
	if state.Models == nil {
		state.Models = map[string]ai.Usage{}
	}
	return state.Models
}

// UsageDoc is the built-in per-conversation usage ledger. Every change stores a complete base (usage.ts:21).
var UsageDoc = durable.DefineDoc(durable.DocDefinition[UsageState]{
	CommonDocDefinition: durable.CommonDocDefinition[UsageState]{
		Kind:    "pi.usage",
		Version: 1,
		CheckpointWhen: func(UsageState, []durable.Op, durable.CheckpointInfo) bool {
			return true
		},
	},
	DocumentSemantics: durable.DocumentSemantics{
		Scope:   durable.ScopeConversation,
		History: durable.HistoryLatest,
		Fork:    durable.ForkInitial,
	},
	Initial: NewUsageState,
})

// RecordUsage adds usage to one bucket of the conversation's pi.usage, in the commit that records the response
// (usage.ts:25-40).
func RecordUsage(tx durable.Tx, conversationId durable.ConversationId, bucket UsageBucket, key string, usage ai.Usage) error {
	ledger, err := docDraft(tx, UsageDoc, conversationId)
	if err != nil {
		return err
	}
	totals := ledger.Object(string(bucket))
	if totals == nil {
		return fmt.Errorf("pi.usage of conversation %d has no %s bucket", conversationId, bucket)
	}
	total := totals.Object(key)
	if total == nil {
		return totals.Set(key, usageJSON(usage))
	}
	return addUsageDraft(total, usage)
}

// usageJSON is the strict JSON of usage with undefined optional counters omitted, totals kept as reported.
func usageJSON(usage ai.Usage) map[string]any {
	value := map[string]any{
		"input":       float64(usage.Input),
		"output":      float64(usage.Output),
		"cacheRead":   float64(usage.CacheRead),
		"cacheWrite":  float64(usage.CacheWrite),
		"totalTokens": float64(usage.TotalTokens),
		"cost": map[string]any{
			"input":      usage.Cost.Input,
			"output":     usage.Cost.Output,
			"cacheRead":  usage.Cost.CacheRead,
			"cacheWrite": usage.Cost.CacheWrite,
			"total":      usage.Cost.Total,
		},
	}
	if usage.CacheWrite1h != nil {
		value["cacheWrite1h"] = float64(*usage.CacheWrite1h)
	}
	if usage.Reasoning != nil {
		value["reasoning"] = float64(*usage.Reasoning)
	}
	return value
}

// addUsageDraft is AddUsage over a pi.usage draft: every counter is reassigned, so only changed ones emit a set.
func addUsageDraft(total *delta.Object, usage ai.Usage) error {
	add := func(object *delta.Object, key string, amount float64) error {
		return object.Set(key, numberAt(object, key)+amount)
	}
	for _, field := range []struct {
		key    string
		amount int
	}{{"input", usage.Input}, {"output", usage.Output}, {"cacheRead", usage.CacheRead}, {"cacheWrite", usage.CacheWrite}, {"totalTokens", usage.TotalTokens}} {
		if err := add(total, field.key, float64(field.amount)); err != nil {
			return err
		}
	}
	if usage.CacheWrite1h != nil {
		if err := add(total, "cacheWrite1h", float64(*usage.CacheWrite1h)); err != nil {
			return err
		}
	}
	if usage.Reasoning != nil {
		if err := add(total, "reasoning", float64(*usage.Reasoning)); err != nil {
			return err
		}
	}
	cost := total.Object("cost")
	if cost == nil {
		return fmt.Errorf("pi.usage total has no cost")
	}
	for _, field := range []struct {
		key    string
		amount float64
	}{{"input", usage.Cost.Input}, {"output", usage.Cost.Output}, {"cacheRead", usage.Cost.CacheRead}, {"cacheWrite", usage.Cost.CacheWrite}, {"total", usage.Cost.Total}} {
		if err := add(cost, field.key, field.amount); err != nil {
			return err
		}
	}
	return nil
}

// AddUsage adds every counter of usage to total; optional counters are added once either side reports them
// (usage.ts:43-57).
func AddUsage(total *ai.Usage, usage ai.Usage) {
	total.Input += usage.Input
	total.Output += usage.Output
	total.CacheRead += usage.CacheRead
	total.CacheWrite += usage.CacheWrite
	total.TotalTokens += usage.TotalTokens
	if usage.CacheWrite1h != nil {
		sum := *usage.CacheWrite1h
		if total.CacheWrite1h != nil {
			sum += *total.CacheWrite1h
		}
		total.CacheWrite1h = &sum
	}
	if usage.Reasoning != nil {
		sum := *usage.Reasoning
		if total.Reasoning != nil {
			sum += *total.Reasoning
		}
		total.Reasoning = &sum
	}
	total.Cost.Input += usage.Cost.Input
	total.Cost.Output += usage.Cost.Output
	total.Cost.CacheRead += usage.Cost.CacheRead
	total.Cost.CacheWrite += usage.Cost.CacheWrite
	total.Cost.Total += usage.Cost.Total
}

// AddUsageState adds every bucket of state into sum (usage.ts:60-74).
func AddUsageState(sum *UsageState, state UsageState) {
	for _, name := range []UsageBucket{UsageModels, UsageTools} {
		from := state.Models
		if name == UsageTools {
			from = state.Tools
		}
		into := sum.bucket(name)
		for key, usage := range from {
			total, found := into[key]
			if !found {
				into[key] = copyUsage(usage)
				continue
			}
			AddUsage(&total, usage)
			into[key] = total
		}
	}
}

func copyUsage(usage ai.Usage) ai.Usage {
	if usage.CacheWrite1h != nil {
		value := *usage.CacheWrite1h
		usage.CacheWrite1h = &value
	}
	if usage.Reasoning != nil {
		value := *usage.Reasoning
		usage.Reasoning = &value
	}
	return usage
}
