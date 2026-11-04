package data_test

import (
	"context"
	"math"
	"runtime"
	"testing"
	"weak"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
)

// Ports packages/chord/test/next/data/principal.test.ts.

func user(id string) data.Principal { return delta.JsonObjectOf("kind", "user", "id", id) }

func TestCanonicalJson(t *testing.T) {
	t.Run("sorts object keys and writes -0 as 0", func(t *testing.T) {
		value := delta.JsonObjectOf("b", 1.0, "a", []any{delta.JsonObjectOf("d", math.Copysign(0, -1), "c", nil)})
		if got, want := data.CanonicalJson(value, chordctx.Background()), `{"a":[{"c":null,"d":0}],"b":1}`; got != want {
			t.Fatalf("CanonicalJson = %s, want %s", got, want)
		}
		if got := data.CanonicalJson("x", chordctx.Background()); got != `"x"` {
			t.Fatalf("CanonicalJson(x) = %s", got)
		}
	})

	t.Run("orders keys by UTF-16 code units, as Array.prototype.sort does", func(t *testing.T) {
		// U+FFFF (one unit, 0xFFFF) sorts after U+10000 (units 0xD800 0xDC00), unlike byte order.
		value := delta.JsonObjectOf("\uffff", 1.0, "\U00010000", 2.0)
		if got, want := data.CanonicalJson(value, chordctx.Background()), "{\"\U00010000\":2,\"\uffff\":1}"; got != want {
			t.Fatalf("CanonicalJson = %s, want %s", got, want)
		}
	})

	t.Run("counts one walk", func(t *testing.T) {
		got := nexttest.Counted(t, func(ctx context.Context) {
			data.CanonicalJson(delta.JsonObjectOf("a", []any{1.0, 2.0}), ctx)
		})
		if want := (nexttest.Counts{Walks: 1}); got != want {
			t.Fatalf("counts = %+v, want %+v", got, want)
		}
	})
}

func TestPrincipalKey(t *testing.T) {
	bg := chordctx.Background()

	t.Run("defaults to the canonical JSON, whatever the property order", func(t *testing.T) {
		if got, want := data.PrincipalKey(user("u1"), bg, nil), `{"id":"u1","kind":"user"}`; got != want {
			t.Fatalf("PrincipalKey = %s, want %s", got, want)
		}
		swapped := delta.JsonObjectOf("id", "u1", "kind", "user")
		if data.PrincipalKey(swapped, bg, nil) != data.PrincipalKey(user("u1"), bg, nil) {
			t.Fatal("property order changed the key")
		}
	})

	t.Run("uses a custom key function when given", func(t *testing.T) {
		byID := data.NewPrincipalKeyFunction(func(principal data.Principal) string {
			kind, _ := principal.Get("kind")
			id, _ := principal.Get("id")
			return kind.(string) + ":" + id.(string)
		})
		if got := data.PrincipalKey(user("u1"), bg, byID); got != "user:u1" {
			t.Fatalf("PrincipalKey = %s", got)
		}
	})

	t.Run("caches per principal object: no second walk", func(t *testing.T) {
		principal := user("u1")
		first := nexttest.Counted(t, func(ctx context.Context) { data.PrincipalKey(principal, ctx, nil) })
		second := nexttest.Counted(t, func(ctx context.Context) { data.PrincipalKey(principal, ctx, nil) })
		if first.Walks != 1 || second.Walks != 0 {
			t.Fatalf("walks = %d, %d, want 1, 0", first.Walks, second.Walks)
		}
	})

	t.Run("caches custom keys per function", func(t *testing.T) {
		principal := user("u1")
		calls := 0
		custom := data.NewPrincipalKeyFunction(func(data.Principal) string {
			calls++
			return "key" + string(rune('0'+calls))
		})
		if first, second := data.PrincipalKey(principal, bg, custom), data.PrincipalKey(principal, bg, custom); first != "key1" || second != "key1" {
			t.Fatalf("keys = %s, %s, want key1, key1", first, second)
		}
		if got := data.PrincipalKey(principal, bg, data.NewPrincipalKeyFunction(func(data.Principal) string { return "other" })); got != "other" {
			t.Fatalf("other = %s", got)
		}
		if got := data.PrincipalKey(principal, bg, nil); got != `{"id":"u1","kind":"user"}` {
			t.Fatalf("default = %s", got)
		}
	})

	t.Run("two closures of one literal with different state are different functions", func(t *testing.T) {
		principal := user("u1")
		keyed := func(key string) *data.PrincipalKeyFunction {
			return data.NewPrincipalKeyFunction(func(data.Principal) string { return key })
		}
		if first, second := data.PrincipalKey(principal, bg, keyed("a")), data.PrincipalKey(principal, bg, keyed("b")); first != "a" || second != "b" {
			t.Fatalf("keys = %s, %s, want a, b", first, second)
		}
	})

	t.Run("never retains a dropped custom key function while its principal lives", func(t *testing.T) {
		principal := user("u1")
		ref := func() weak.Pointer[data.PrincipalKeyFunction] {
			custom := data.NewPrincipalKeyFunction(func(data.Principal) string { return "k" })
			data.PrincipalKey(principal, bg, custom)
			return weak.Make(custom)
		}()
		nexttest.ExpectCollected(t, ref)
		if got := data.PrincipalKey(principal, bg, nil); got == "" {
			t.Fatal("empty key")
		}
		runtime.KeepAlive(principal)
	})

	t.Run("does not retain dropped principals", func(t *testing.T) {
		custom := data.NewPrincipalKeyFunction(func(data.Principal) string { return "k" })
		ref := func() weak.Pointer[delta.JsonObject] {
			principal := user("gone")
			data.PrincipalKey(principal, bg, nil)
			data.PrincipalKey(principal, bg, custom)
			return weak.Make(principal)
		}()
		nexttest.ExpectCollected(t, ref)
		runtime.KeepAlive(custom)
	})
}
