package rpcclient

import (
	"io"
	"reflect"
	"testing"
)

type jsonlChunks struct{ chunks [][]byte }

func (r *jsonlChunks) Read(p []byte) (int, error) {
	if len(r.chunks) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.chunks[0])
	r.chunks[0] = r.chunks[0][n:]
	if len(r.chunks[0]) == 0 {
		r.chunks = r.chunks[1:]
	}
	return n, nil
}

// Pi jsonl.ts:onData dispatches all complete records in the current data chunk before its callback returns; onEnd separately emits the final unterminated record.
func TestReadJSONLBatchesPreservesInputTurns(t *testing.T) {
	r := &jsonlChunks{chunks: [][]byte{[]byte("{\"n\":1}\r\n{\"text\":\"a\u2028b\u2029c\"}\n{\"par"), []byte("tial\":true}\n\n"), []byte("{\"last\":true}")}}
	var got [][]string
	if err := ReadJSONLBatches(r, func(batch [][]byte) bool {
		var lines []string
		for _, line := range batch {
			lines = append(lines, string(line))
		}
		got = append(got, lines)
		return true
	}); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{`{"n":1}`, "{\"text\":\"a\u2028b\u2029c\"}"}, {`{"partial":true}`, ""}, {`{"last":true}`}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("batches=%q want=%q", got, want)
	}
}

func TestReadJSONLBatchesStopsWithoutReadingNextTurn(t *testing.T) {
	r := &jsonlChunks{chunks: [][]byte{[]byte("one\ntwo\n"), []byte("unread\n")}}
	if err := ReadJSONLBatches(r, func(batch [][]byte) bool {
		if len(batch) != 2 {
			t.Fatal(batch)
		}
		return false
	}); err != nil {
		t.Fatal(err)
	}
	if len(r.chunks) != 1 || string(r.chunks[0]) != "unread\n" {
		t.Fatalf("reader continued after detach: %q", r.chunks)
	}
}
