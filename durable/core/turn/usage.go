// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package turn

import (
	"github.com/MichaelKinsy/PiG/durable/core/internal/delta"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
)

// The usage ledger of src/harness/usage.ts. A usage and a ledger are ordered JSON objects: the document value is the
// ledger, and a bucket entry is a pi-ai Usage.

// Ledger buckets of the pi.usage document: assistant entries and summarization attempts by provider/modelId, and tool
// results by tool name.
const (
	UsageModels = "models"
	UsageTools  = "tools"
)

// UsageInitial is the initial value of the pi.usage document: {models: {}, tools: {}}.
func UsageInitial() *jv.Object {
	v := jv.NewObject()
	v.Set(UsageModels, jv.NewObject())
	v.Set(UsageTools, jv.NewObject())
	return v
}

// RecordUsage adds usage to the bucket entry key of ledger, in the commit that records the response: the first usage of a
// key is copied, with absent optional counters left out; later ones are added. Own keys only (a tool may be called
// `toString`), which an ordered object is by construction.
func RecordUsage(ledger *jv.Object, bucket, key string, usage *jv.Object) {
	totals, _ := ledger.Get(bucket)
	into, ok := totals.(*jv.Object)
	if !ok {
		return
	}
	if total, has := into.Get(key); has {
		if t, isObject := total.(*jv.Object); isObject {
			AddUsage(t, usage)
			return
		}
	}
	into.Set(key, jv.Clone(usage))
}

// AddUsage adds every counter of usage to total; optional counters are added once either side reports them.
func AddUsage(total, usage *jv.Object) {
	for _, counter := range [...]string{"input", "output", "cacheRead", "cacheWrite", "totalTokens"} {
		addCounter(total, counter, usage, true)
	}
	addCounter(total, "cacheWrite1h", usage, false)
	addCounter(total, "reasoning", usage, false)

	totalCost, _ := total.Get("cost")
	usageCost, _ := usage.Get("cost")
	if tc, ok := totalCost.(*jv.Object); ok {
		if uc, ok := usageCost.(*jv.Object); ok {
			for _, counter := range [...]string{"input", "output", "cacheRead", "cacheWrite", "total"} {
				addCounter(tc, counter, uc, true)
			}
		}
	}
}

// addCounter is total[name] += usage[name]; an optional counter is skipped when usage lacks it and starts at 0 when total does.
func addCounter(total *jv.Object, name string, usage *jv.Object, required bool) {
	v, has := usage.Get(name)
	if !has && !required {
		return
	}
	add, _ := v.(float64)
	var base float64
	if cur, ok := total.Get(name); ok {
		base, _ = cur.(float64)
	}
	total.Set(name, base+add)
}

// AddUsageState adds every bucket of state into sum.
func AddUsageState(sum, state *jv.Object) {
	for _, bucket := range [...]string{UsageModels, UsageTools} {
		from, _ := state.Get(bucket)
		fromBucket, ok := from.(*jv.Object)
		if !ok {
			continue
		}
		to, _ := sum.Get(bucket)
		toBucket, ok := to.(*jv.Object)
		if !ok {
			continue
		}
		fromBucket.Entries(func(key string, value any) bool {
			usage, _ := value.(*jv.Object)
			if total, has := toBucket.Get(key); has {
				if t, isObject := total.(*jv.Object); isObject && usage != nil {
					AddUsage(t, usage)
					return true
				}
			}
			toBucket.Set(key, jv.Clone(value))
			return true
		})
	}
}

// UsageDef is UsageDoc: the conversation's own spend, kept as a complete base on every change.
// Read-only: assigned once at initialization.
var UsageDef = &session.DocDef{
	Kind:           "pi.usage",
	Version:        1,
	Scope:          session.ScopeConversation,
	History:        session.HistoryLatest,
	Fork:           session.ForkInitial,
	Initial:        func(any) *jv.Object { return UsageInitial() },
	CheckpointWhen: func(*jv.Object, []delta.Op, int) bool { return true },
}

// addUsageDraft is AddUsage on a document draft: each counter is one set operation, in AddUsage's order.
func addUsageDraft(total *delta.Obj, usage *jv.Object) error {
	for _, counter := range [...]string{"input", "output", "cacheRead", "cacheWrite", "totalTokens"} {
		if err := addDraftCounter(total, counter, usage, true); err != nil {
			return err
		}
	}
	if err := addDraftCounter(total, "cacheWrite1h", usage, false); err != nil {
		return err
	}
	if err := addDraftCounter(total, "reasoning", usage, false); err != nil {
		return err
	}
	cost, _ := usage.Get("cost")
	if uc, ok := cost.(*jv.Object); ok {
		if tc := total.Obj("cost"); tc != nil {
			for _, counter := range [...]string{"input", "output", "cacheRead", "cacheWrite", "total"} {
				if err := addDraftCounter(tc, counter, uc, true); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func addDraftCounter(total *delta.Obj, name string, usage *jv.Object, required bool) error {
	v, has := usage.Get(name)
	if !has && !required {
		return nil
	}
	add, _ := v.(float64)
	base, _ := total.Get(name).(float64)
	return total.Set(name, base+add)
}
