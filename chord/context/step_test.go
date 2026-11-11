package chordctx

import (
	"context"
	"reflect"
	"testing"
)

func TestStepBatchGroupsItemsOfOneKeyIntoTheFirstFlushAtEnd(t *testing.T) {
	ctx, end := WithStep(context.Background())
	var ran []string
	var flushed [][]string
	flushFor := func(name string) func([]func()) {
		return func(items []func()) {
			flushed = append(flushed, []string{name})
			for _, item := range items {
				item()
			}
		}
	}
	item := func(name string) func() { return func() { ran = append(ran, name) } }
	for _, call := range []struct{ key, name string }{{"a", "a1"}, {"b", "b1"}, {"a", "a2"}} {
		if !StepBatch(ctx, call.key, item(call.name), flushFor("flush-"+call.name)) {
			t.Fatalf("%s refused by a live step", call.name)
		}
	}
	if len(ran) != 0 {
		t.Fatalf("items ran before the step ended: %v", ran)
	}
	end()
	end()
	if want := []string{"a1", "a2", "b1"}; !reflect.DeepEqual(ran, want) {
		t.Fatalf("ran %v, want %v", ran, want)
	}
	if want := [][]string{{"flush-a1"}, {"flush-b1"}}; !reflect.DeepEqual(flushed, want) {
		t.Fatalf("flushed %v, want %v", flushed, want)
	}
	if StepBatch(ctx, "a", item("late"), flushFor("late")) {
		t.Fatal("an ended step took an item")
	}
}

func TestStepBatchRefusesAContextWithoutAStep(t *testing.T) {
	if StepBatch(context.Background(), "a", func() {}, func([]func()) {}) {
		t.Fatal("a context without a step took an item")
	}
}
