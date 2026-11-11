package store

import (
	"context"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
)

// Ports packages/chord/test/next/store/jsonl.test.ts: tests of the JSONL archive encoding (slice 6a §13): parts as
// lines, chunks split anywhere (inside a line, inside a UTF-8 sequence), blank lines, and the refusal of a line that
// is no JSON.

// jsonlParts is Pi's `from(values)` over parts.
type jsonlParts struct{ parts []ArchivePart }

func (source *jsonlParts) Next(context.Context) (ArchivePart, bool, error) {
	if len(source.parts) == 0 {
		return nil, false, nil
	}
	part := source.parts[0]
	source.parts = source.parts[1:]
	return part, true, nil
}

func (source *jsonlParts) Close() error { return nil }

// jsonlChunks is Pi's `from(values)` over byte chunks.
type jsonlChunks struct{ chunks [][]byte }

func (source *jsonlChunks) Next(context.Context) ([]byte, bool, error) {
	if len(source.chunks) == 0 {
		return nil, false, nil
	}
	chunk := source.chunks[0]
	source.chunks = source.chunks[1:]
	return chunk, true, nil
}

func (source *jsonlChunks) Close() error { return nil }

func jsonlCollectBytes(t *testing.T, source ByteSource) []byte {
	t.Helper()
	var all []byte
	for {
		chunk, ok, err := source.Next(background)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		if !ok {
			return all
		}
		all = append(all, chunk...)
	}
}

func jsonlCollect(source PartSource) ([]any, error) {
	var all []any
	for {
		value, ok, err := source.Next(background)
		if err != nil || !ok {
			return all, err
		}
		all = append(all, value)
	}
}

func jsonlPartsOf() []ArchivePart {
	return []ArchivePart{
		&ManifestPart{Format: 1, Schema: 1, LastSeq: 3, NextID: 2},
		&PutWrite{
			Table:   "app.store:edits",
			Row:     delta.JsonObjectOf("id", "ü€😀", "n", float64(1)),
			Version: 1,
			Columns: delta.NewJsonObject(0),
		},
		&EndPart{Writes: 1},
	}
}

func jsonlWant(t *testing.T, parts ...ArchivePart) []any {
	t.Helper()
	want := make([]any, len(parts))
	for index, part := range parts {
		want[index] = nexttest.ParseJSON(t, nexttest.JSONText(t, part))
	}
	return want
}

func jsonlSame(t *testing.T, got, want []any) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("decoded %d values, want %d", len(got), len(want))
	}
	for index := range got {
		nexttest.RequireJSONEqual(t, got[index], want[index])
	}
}

func TestJsonl(t *testing.T) {
	parts := jsonlPartsOf()

	t.Run("encodes one part per line and decodes it back, from chunks split anywhere", func(t *testing.T) {
		encoded := jsonlCollectBytes(t, EncodeJsonl(&jsonlParts{parts: parts}))
		if lines := strings.Split(string(encoded), "\n"); len(lines) != len(parts)+1 {
			t.Fatalf("lines = %d, want %d", len(lines), len(parts)+1)
		}
		for _, size := range []int{1, 2, 3, 7, len(encoded)} {
			var chunks [][]byte
			for at := 0; at < len(encoded); at += size {
				chunks = append(chunks, encoded[at:min(at+size, len(encoded))])
			}
			got, err := jsonlCollect(DecodeJsonl(&jsonlChunks{chunks: chunks}))
			if err != nil {
				t.Fatalf("size %d: %v", size, err)
			}
			jsonlSame(t, got, jsonlWant(t, parts...))
		}
	})

	t.Run("skips blank lines and takes a last line without a newline", func(t *testing.T) {
		first := nexttest.JSONText(t, parts[0])
		last := nexttest.JSONText(t, parts[2])
		text := "\n" + first + "\n\n" + last
		got, err := jsonlCollect(DecodeJsonl(&jsonlChunks{chunks: [][]byte{[]byte(text)}}))
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		jsonlSame(t, got, jsonlWant(t, parts[0], parts[2]))
	})

	t.Run("refuses a line that is no JSON, naming it", func(t *testing.T) {
		text := nexttest.JSONText(t, parts[0]) + "\n{nope\n"
		_, err := jsonlCollect(DecodeJsonl(&jsonlChunks{chunks: [][]byte{[]byte(text)}}))
		chordErr := rejected(t, err, data.CodeInvalid, data.ReasonArgs)
		if chordErr.Message != "jsonl: line 2 is not JSON" {
			t.Fatalf("message = %q", chordErr.Message)
		}
	})

	t.Run("refuses bytes that are no UTF-8", func(t *testing.T) {
		_, err := jsonlCollect(DecodeJsonl(&jsonlChunks{chunks: [][]byte{{0x7b, 0xff, 0x0a}}}))
		chordErr := rejected(t, err, data.CodeInvalid, "")
		if chordErr.Message != "jsonl: line 1 is not UTF-8" {
			t.Fatalf("message = %q", chordErr.Message)
		}
	})
}
