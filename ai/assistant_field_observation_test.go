package ai

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func TestAssistantFieldProjectionsDoNotTraverseOtherValues(t *testing.T) {
	t.Parallel()
	visits := 0
	probe := observationJSONMarshaler{calls: &visits}
	message := &AssistantMessage{
		Content:     []AssistantContentBlock{ToolCall{ID: "call", Name: "lookup", Arguments: JsonObject{"probe": probe}}},
		Usage:       Usage{Input: 7, Reasoning: new(11), CacheWrite1h: new(13)},
		Diagnostics: []AssistantMessageDiagnostic{{Details: map[string]any{"probe": probe}}},
		Deferred:    &DeferredHandle{Data: probe},
	}
	usage := message.ObserveUsage()
	if !reflect.DeepEqual(usage, message.Usage) {
		t.Fatalf("usage=%#v, want %#v", usage, message.Usage)
	}
	id, name, tool, valid := message.ObserveToolCallIdentity(0)
	if id != "call" || name != "lookup" || !tool || !valid || visits != 0 {
		t.Fatalf("identity=%q/%q tool=%t valid=%t visits=%d", id, name, tool, valid, visits)
	}
	*usage.Reasoning, *usage.CacheWrite1h = 101, 103
	if *message.Usage.Reasoning != 11 || *message.Usage.CacheWrite1h != 13 {
		t.Fatal("usage projection aliases optional pointees")
	}
}

func TestAssistantUsageProjectionOwnsPublishedPointees(t *testing.T) {
	t.Parallel()
	message := &AssistantMessage{Usage: Usage{Reasoning: new(7), CacheWrite1h: new(11)}}
	cell := newAssistantMessageCell(message)
	full := cell.view()
	for _, view := range []*AssistantMessage{full, full.ShallowCopy()} {
		usage := view.ObserveUsage()
		*usage.Reasoning, *usage.CacheWrite1h = 99, 101
		again := view.ObserveUsage()
		if *again.Reasoning != 7 || *again.CacheWrite1h != 11 {
			t.Fatal("usage projection exposed a mutable published pointee")
		}
	}
}

func TestAssistantFieldProjectionsFollowRetainedReferences(t *testing.T) {
	t.Parallel()
	message := &AssistantMessage{Content: []AssistantContentBlock{ToolCall{ID: "first", Name: "first", Arguments: JsonObject{}}}, Usage: Usage{Input: 1}}
	cell := newAssistantMessageCell(message)
	full := cell.view()
	shallow := full.ShallowCopy()
	message.Content[0] = ToolCall{ID: "mutated", Name: "mutated", Arguments: JsonObject{}}
	message.Usage.Input = 2
	cell.publish(message, assistantMessageReplacements{})
	for _, view := range []*AssistantMessage{full, shallow} {
		id, name, tool, valid := view.ObserveToolCallIdentity(0)
		if id != "mutated" || name != "mutated" || !tool || !valid || view.ObserveUsage().Input != 2 {
			t.Fatalf("retained mutation lost: %q/%q usage=%#v", id, name, view.ObserveUsage())
		}
	}
	message.Content = []AssistantContentBlock{ToolCall{ID: "replacement", Name: "replacement", Arguments: JsonObject{}}}
	message.Usage = Usage{Input: 3}
	cell.publish(message, assistantMessageReplacements{Content: true, Usage: true})
	message.Usage.Input = 4
	cell.publish(message, assistantMessageReplacements{})
	for _, row := range []struct {
		view  *AssistantMessage
		id    string
		input int
	}{{full, "replacement", 4}, {full.ShallowCopy(), "replacement", 4}, {shallow, "mutated", 2}} {
		id, name, tool, valid := row.view.ObserveToolCallIdentity(0)
		if id != row.id || name != row.id || !tool || !valid || row.view.ObserveUsage().Input != row.input {
			t.Fatalf("replacement ownership: id=%q name=%q usage=%#v", id, name, row.view.ObserveUsage())
		}
	}
}

func TestAssistantFieldProjectionNilIndexAndType(t *testing.T) {
	t.Parallel()
	var absent *AssistantMessage
	if usage := absent.ObserveUsage(); !reflect.DeepEqual(usage, Usage{}) {
		t.Fatalf("nil usage=%#v", usage)
	}
	if id, name, tool, valid := absent.ObserveToolCallIdentity(0); id != "" || name != "" || tool || valid {
		t.Fatal("nil message has a tool identity")
	}
	message := &AssistantMessage{Content: []AssistantContentBlock{TextContent{Text: "text"}, ToolCall{ID: "id", Name: "name"}}}
	for _, row := range []struct {
		index       int
		tool, valid bool
	}{{-1, false, false}, {0, false, true}, {1, true, true}, {2, false, false}} {
		id, name, tool, valid := message.ObserveToolCallIdentity(row.index)
		if tool != row.tool || valid != row.valid || tool && (id != "id" || name != "name") || !tool && (id != "" || name != "") {
			t.Fatalf("index %d: %q/%q tool=%t valid=%t", row.index, id, name, tool, valid)
		}
	}
}

func TestAssistantFieldProjectionsRaceWithPublication(t *testing.T) {
	t.Parallel()
	message := &AssistantMessage{Content: []AssistantContentBlock{ToolCall{ID: "0", Name: "0", Arguments: JsonObject{}}}, Usage: Usage{Reasoning: new(0)}}
	cell := newAssistantMessageCell(message)
	full := cell.view()
	shallow := full.ShallowCopy()
	var work sync.WaitGroup
	work.Go(func() {
		for i := range 500 {
			id := fmt.Sprint(i)
			message.Content[0] = ToolCall{ID: id, Name: id, Arguments: JsonObject{}}
			message.Usage.Input, *message.Usage.Reasoning = i, i
			cell.publish(message, assistantMessageReplacements{Content: i > 0 && i%50 == 0, Usage: i > 0 && i%50 == 0})
		}
	})
	for _, view := range []*AssistantMessage{full, shallow} {
		work.Go(func() {
			for range 500 {
				usage := view.ObserveUsage()
				if usage.Reasoning == nil || usage.Input != *usage.Reasoning {
					t.Errorf("torn usage: %#v", usage)
				}
				id, name, tool, valid := view.ObserveToolCallIdentity(0)
				if !tool || !valid || id != name {
					t.Errorf("torn tool identity: %q/%q", id, name)
				}
			}
		})
	}
	work.Wait()
}
