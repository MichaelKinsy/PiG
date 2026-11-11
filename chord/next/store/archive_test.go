package store

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
)

// Ports packages/chord/test/next/store/archive.test.ts: tests of the archive part schema (slice 6a §13, rule 25):
// every part an export yields passes; a part with an unknown kind, a missing or extra field or a value of another
// type is refused, naming its position.

func TestArchivePartSchema(t *testing.T) {
	t.Run("takes every part an export yields", func(t *testing.T) {
		store := archiveStore(t)
		parts := exported(t, store, background)
		for position, part := range parts {
			checked, err := CheckPart(part, position, background)
			if err != nil {
				t.Fatalf("CheckPart(part %d): %v", position, err)
			}
			if checked != part {
				t.Fatalf("CheckPart(part %d) returned another value than its argument", position)
			}
		}
	})

	for _, c := range []struct{ name, part string }{
		{"an unknown kind", `{"kind":"blob"}`},
		{"a missing field", `{"kind":"end"}`},
		{"an extra field", `{"kind":"end","writes":0,"more":1}`},
		{"a negative count", `{"kind":"end","writes":-1}`},
		{
			"a column of an unknown type",
			`{"kind":"table","table":"a:b:c","version":1,"state":"ready","columns":{"a":{"type":"date"}},"key":["a"],"fingerprint":"f"}`,
		},
		{"a put at version 0", `{"kind":"put","table":"a:b:c","row":{},"version":0,"columns":{}}`},
		{
			"a record without an id",
			`{"kind":"document.create","record":{"namespace":"n"},"base":{"version":1,"fingerprint":"f","value":{}}}`,
		},
	} {
		t.Run("refuses "+c.name, func(t *testing.T) {
			_, err := CheckPart(nexttest.ParseJSON(t, c.part), 4, background)
			if err == nil || err.Error() != "import: part 4: not an archive part" {
				t.Fatalf("CheckPart = %v, want import: part 4: not an archive part", err)
			}
		})
	}
}
