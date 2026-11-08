package link_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// pi: packages/chord/src/next/link/values.ts
// Ports packages/chord/test/link/values.test.ts.

func TestPartsAreJSONText(t *testing.T) {
	t.Run("are JSON text at their origin, parsed from text or from UTF-8 bytes where they are owned", func(t *testing.T) {
		text, err := link.JsonPart(object("a", []any{1.0, "x"}))
		requireNoError(t, err)
		if text != link.Text(`{"a":[1,"x"]}`) {
			t.Fatalf("text = %q", text)
		}
		if null, err := link.JsonPart(nil); err != nil || null != link.Text("null") {
			t.Fatalf("nil = %q, %v", null, err)
		}
		parsed, err := link.ParsePart(link.Text(`{"a":1}`))
		requireNoError(t, err)
		nexttest.RequireJSONEqual(t, parsed, object("a", 1.0))
		parsed, err = link.ParsePart(link.Bytes(`{"a":"é"}`))
		requireNoError(t, err)
		nexttest.RequireJSONEqual(t, parsed, object("a", "é"))
	})

	t.Run("that are no strict UTF-8 JSON throw (a protocol error where they are parsed)", func(t *testing.T) {
		if _, err := link.ParsePart(link.Text("{")); err == nil {
			t.Fatal("no error for {")
		}
		if _, err := link.ParsePart(link.Bytes{0x22, 0xff, 0x22}); err == nil {
			t.Fatal("no error for invalid UTF-8")
		}
	})
}

func TestPayloads(t *testing.T) {
	t.Run("marked as carrying bytes write each byte leaf as a segment referenced by {$bytes: i}", func(t *testing.T) {
		payload := bytes.Repeat([]byte{7}, 1024)
		thumbs := [][]byte{{1, 2}, {3}}
		value := object("name", "chart.png", "data", payload, "thumbs", []any{thumbs[0], thumbs[1]})
		part, err := link.PayloadPart(value, true)
		requireNoError(t, err)
		if want := `{"name":"chart.png","data":{"$bytes":0},"thumbs":[{"$bytes":1},{"$bytes":2}]}`; part.Json != want {
			t.Fatalf("json = %s", part.Json)
		}
		if len(part.Segments) != 3 || &part.Segments[0][0] != &payload[0] {
			t.Fatal("the first segment is not the value's own bytes")
		}
		read, err := link.ParsePayload(link.Text(part.Json), part.Segments)
		requireNoError(t, err)
		root := read.(*delta.JsonObject)
		if got := root.Value("data").([]byte); &got[0] != &payload[0] {
			t.Fatal("data is not the segment")
		}
		nexttest.RequireJSONEqual(t, root.Value("thumbs").([]any)[1], []byte{3})
	})

	// Pi's "carry any Uint8Array subclass as bytes: a Node Buffer, whose toJSON the replacer never sees (slice 11)" guards a
	// JavaScript mechanic: a Buffer reaches JSON.stringify's replacer as what its toJSON made, so the replacer reads the holder's
	// own value. Go has no subclass and no toJSON the encoder could reach first: a byte leaf is the []byte itself, wherever it
	// sits (an object member, an array element or the whole value), and its segment is that slice, not a copy.
	t.Run("carry a byte leaf as its own segment wherever it sits: a member, an element and the whole value (slice 11)", func(t *testing.T) {
		buffer := []byte{1, 2, 3}
		part, err := link.PayloadPart(object("bytes", buffer, "at", []any{buffer}), true)
		requireNoError(t, err)
		if want := `{"bytes":{"$bytes":0},"at":[{"$bytes":1}]}`; part.Json != want {
			t.Fatalf("json = %s, want %s", part.Json, want)
		}
		if len(part.Segments) != 2 || &part.Segments[0][0] != &buffer[0] || &part.Segments[1][0] != &buffer[0] {
			t.Fatal("a segment is not the value's own bytes")
		}
		whole, err := link.PayloadPart(buffer, true)
		requireNoError(t, err)
		if whole.Json != `{"$bytes":0}` || len(whole.Segments) != 1 || &whole.Segments[0][0] != &buffer[0] {
			t.Fatalf("whole = %+v", whole)
		}
	})

	t.Run("marked as carrying bytes refuse the reserved key $bytes with invalid/args", func(t *testing.T) {
		_, err := link.PayloadPart(object("meta", object("$bytes", 0.0), "data", []byte{0}), true)
		chordErr, ok := data.IsChordErrorCode(err, data.CodeInvalid)
		if !ok || chordErr.Reason != data.ReasonArgs {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("not marked are plain JSON, $bytes included; undefined is null", func(t *testing.T) {
		part, err := link.PayloadPart(object("$bytes", 0.0), false)
		requireNoError(t, err)
		if part.Json != `{"$bytes":0}` || part.Segments != nil {
			t.Fatalf("part = %+v", part)
		}
		if null, _ := link.PayloadPart(nil, false); null.Json != "null" {
			t.Fatalf("nil = %q", null.Json)
		}
		parsed, err := link.ParsePayload(link.Text(`{"$bytes":0}`), nil)
		requireNoError(t, err)
		nexttest.RequireJSONEqual(t, parsed, object("$bytes", 0.0))
	})

	for name, reference := range map[string]any{
		"an index past the segments": object("$bytes", 1.0),
		"a non-integer index":        object("$bytes", "0"),
		"other keys next to $bytes":  object("$bytes", 0.0, "x", 1.0),
		"a fractional index":         object("$bytes", 0.5),
		"a negative index":           object("$bytes", -1.0),
	} {
		t.Run("refuse a malformed reference: "+name, func(t *testing.T) {
			_, err := link.ResolveSegments(object("a", []any{reference}), [][]byte{{0}})
			requireErrorContains(t, err, "payload: a malformed byte segment reference")
		})
	}

	t.Run("define a resolved key __proto__ as an own property", func(t *testing.T) {
		parsed, err := link.ParsePart(link.Text(`{"__proto__":{"$bytes":0}}`))
		requireNoError(t, err)
		resolved, err := link.ResolveSegments(parsed, [][]byte{{9}})
		requireNoError(t, err)
		if got := resolved.(*delta.JsonObject).Value("__proto__"); !bytes.Equal(got.([]byte), []byte{9}) {
			t.Fatalf("__proto__ = %v", got)
		}
	})
}

func TestPartsKeepALeadingBOM(t *testing.T) {
	// A U+FEFF is data, not a byte order mark to strip: inside a JSON string it survives, before the JSON it is an error.
	parsed, err := link.ParsePart(link.Bytes("\"\uFEFFx\""))
	requireNoError(t, err)
	if parsed != "\uFEFFx" {
		t.Fatalf("parsed = %q", parsed)
	}
	if _, err := link.ParsePart(link.Bytes("\uFEFF" + strings.Repeat("1", 1))); err == nil {
		t.Fatal("a BOM before the JSON parsed")
	}
}
