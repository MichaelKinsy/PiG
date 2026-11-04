package schema

// Ports packages/schema/test/shared.test.ts: values shared across compiled schemas hold nothing alive: a cached pattern goes once its schemas are collected.

import (
	"fmt"
	"reflect"
	"runtime"
	"testing"
	"time"
	"weak"
)

// collectUntil collects garbage until done reports true. The collector and the cleanup goroutines run asynchronously, so each round yields after a collection.
func collectUntil(t *testing.T, done func() bool) {
	t.Helper()
	for range 200 {
		runtime.GC()
		runtime.Gosched()
		time.Sleep(time.Millisecond)
		if done() {
			return
		}
	}
	t.Fatal("garbage was not collected")
}

func mustCompileMap(t *testing.T, s map[string]any) *Validator {
	t.Helper()
	v, err := Compile(s, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// pi: packages/schema/src/shared.ts

func TestSharedChecks(t *testing.T) {
	// The check of a bare type is a module-level function, so equal bare types share it.
	t.Run("share the module-level check of a bare type", func(t *testing.T) {
		a := mustCompileMap(t, map[string]any{"type": "string", "description": "a"})
		b := mustCompileMap(t, map[string]any{"type": "string", "title": "b"})
		pointer := func(v *Validator) uintptr { return reflect.ValueOf(v.check).Pointer() }
		if pointer(a) != pointer(b) || pointer(a) != reflect.ValueOf(isString).Pointer() {
			t.Fatal("equal bare types must share one check")
		}
		if pointer(mustCompileMap(t, map[string]any{"type": "integer"})) == pointer(a) {
			t.Fatal("different bare types must not share a check")
		}
	})

	t.Run("share a pattern's regular expression and drop it with the last schema that uses it", func(t *testing.T) {
		collectUntil(t, func() bool { return true })
		before := sharedRegexCount()
		validators := make([]*Validator, 50)
		for i := range validators {
			validators[i] = mustCompileMap(t, map[string]any{"type": "string", "pattern": fmt.Sprintf("^unique-%d$", i)})
		}
		again := mustCompileMap(t, map[string]any{"type": "string", "pattern": "^unique-0$"})
		if got := sharedRegexCount(); got != before+50 {
			t.Fatalf("cached patterns = %d, want %d", got, before+50)
		}
		if ok, err := again.Check("unique-0"); err != nil || !ok {
			t.Fatal("again rejects unique-0")
		}
		check := weak.Make(validators[0])
		validators = nil
		runtime.KeepAlive(validators)
		// again still uses ^unique-0$; the other 49 patterns are gone.
		collectUntil(t, func() bool { return check.Value() == nil && sharedRegexCount() == before+1 })
		runtime.KeepAlive(again)
	})
}
