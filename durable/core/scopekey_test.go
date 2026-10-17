// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package core

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

// scopeKey and recordAddrKey are the stringify of the arrays Pi's documents.ts keys a record by; they are built in place, and
// the text must be the one the arrays make, nested escaping of the scope key included.
func TestRecordAddrKeyIsTheStringifyOfItsArrays(t *testing.T) {
	scope := func(kind string, id float64) *jv.Object {
		o := jv.NewObject()
		o.Set("kind", kind)
		switch kind {
		case "conversation":
			o.Set("conversationId", id)
		case "task":
			o.Set("taskId", id)
		}
		return o
	}
	scopes := []*jv.Object{scope("session", 0), scope("conversation", 7), scope("task", 1<<40), scope("other", 3), scope("conversation", 0)}
	keys := []any{nil, "k", "a\"b", "\xed\xa0\x80", 1.5, true, []any{"x", 2.0}}
	for _, sc := range scopes {
		want := `["session"]`
		switch str(sc, "kind") {
		case "conversation":
			want = jv.Stringify([]any{"conversation", f(num(sc, "conversationId"))})
		case "task":
			want = jv.Stringify([]any{"task", f(num(sc, "taskId"))})
		}
		if got := scopeKey(sc); got != want {
			t.Fatalf("scopeKey %s: %s, want %s", jv.Stringify(sc), got, want)
		}
		for _, kind := range []string{"", "note", "a\"b"} {
			for i, key := range keys {
				rec := jv.NewObject()
				rec.Set("kind", kind)
				rec.Set("scope", sc)
				var tail any = []any{"singleton"}
				if i > 0 {
					rec.Set("key", key)
					tail = []any{"family", key}
				}
				if got, want := recordAddrKey(rec), jv.Stringify([]any{kind, want, tail}); got != want {
					t.Fatalf("recordAddrKey %s: %s, want %s", jv.Stringify(rec), got, want)
				}
			}
		}
	}
}
