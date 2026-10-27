package engine

import "testing"

func benchObject() any {
	return tObject(NewObj("id", tString(nil), "name", Update(tString(nil), NewObj("~optional", true), NewObj()), "n", tInteger(nil), "tags", tArray(tString(nil), nil)), nil)
}

// BenchmarkEngine is the engine alone, with no wire: one Pick, one KeyOf, one Mapped and one Script of an object type.
func BenchmarkEngine(b *testing.B) {
	object := benchObject()
	b.Run("pick", func(b *testing.B) {
		b.ReportAllocs()
		keys := []any{"id", "name"}
		for range b.N {
			if _, err := Call("Pick", []any{object, keys}); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("keyof", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if _, err := Call("KeyOf", []any{object}); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("mapped", func(b *testing.B) {
		b.ReportAllocs()
		args := []any{tIdentifier("K"), tUnion([]any{tLiteral("a", nil), tLiteral("b", nil)}, nil), tRef("K", nil), tString(nil)}
		for range b.N {
			if _, err := Call("Mapped", args); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("script", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if _, err := Call("Script", []any{"{ a: string; b?: number[]; c: 'x' | 'y' }"}); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("wire-pick", func(b *testing.B) {
		b.ReportAllocs()
		request := []byte(`{"member":"Pick","args":[` + Encode(object) + `,["id","name"]]}`)
		for range b.N {
			if out := Serve(request); len(out) == 0 {
				b.Fatal("empty reply")
			}
		}
	})
}
