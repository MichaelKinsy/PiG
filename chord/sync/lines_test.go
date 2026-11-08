package sync

import (
	"bytes"
	"compress/gzip"
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/chord/test/sync/lines.test.ts: the sync wire format, as streams: outbox commits as versioned,
// gzip-compressed JSON lines, snapshots as store archives, and decoding that holds one line at a time.

// seeded is a store with its outbox on and two commits: two documents created one at a time.
func seeded(t *testing.T) store.Store {
	t.Helper()
	s := sessionStore(t)
	for _, key := range []string{"a", "b"} {
		address := store.DocumentAddress{Namespace: "chord", Owner: "app.sync", Kind: "note", Key: &key}
		base := store.DocumentBase{Version: 1, Fingerprint: "f1"}
		base.Value = deltaObject("key", key)
		_, err := chordOf(s).Commit([]store.StagedWrite{store.CreateDocument(address, base, `{"key":"`+key+`"}`, nil)}, openFence, background)
		noError(t, err)
	}
	return s
}

// gzipped is plain text gzip-compressed, as a body: for bodies the encoders would never write.
func gzipped(t *testing.T, text string) SyncBody {
	t.Helper()
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	_, err := writer.Write([]byte(text))
	noError(t, err)
	noError(t, writer.Close())
	return chunked(buffer.Bytes(), buffer.Len())
}

// collect is every commit of a source.
func collect(t *testing.T, commits CommitSource) ([]store.OutboxCommit, error) {
	t.Helper()
	defer func() { _ = commits.Close() }()
	var out []store.OutboxCommit
	for {
		commit, ok, err := commits.Next(background)
		if err != nil || !ok {
			return out, err
		}
		out = append(out, commit)
	}
}

// line is the log line of a commit with no writes.
func line(t *testing.T, seq, nextID int64) string {
	t.Helper()
	encoded, err := EncodeLogLine(store.OutboxCommit{Seq: seq, NextID: nextID, Writes: []store.StoreWrite{}})
	noError(t, err)
	return string(encoded)
}

func TestBatchBodies(t *testing.T) {
	t.Run("round-trip outbox commits, whatever the chunks", func(t *testing.T) {
		s := seeded(t)
		commits, err := ReadOutbox(background, s, LogStart, 100)
		noError(t, err)
		if len(commits) != 2 {
			t.Fatalf("commits = %d, want 2", len(commits))
		}
		encoded := bytesOf(t, EncodeBatch(SliceCommits(commits)))
		if encoded[0] != 0x1f {
			t.Fatalf("first byte = %#x, want the gzip magic", encoded[0])
		}
		for _, size := range []int{1, 7, len(encoded)} {
			decoded, err := collect(t, DecodeBatch(chunked(encoded, size)))
			noError(t, err)
			if !reflect.DeepEqual(decoded, commits) {
				t.Fatalf("chunks of %d: decoded %+v, want %+v", size, decoded, commits)
			}
		}
	})

	t.Run("refuse a body that ends inside a line", func(t *testing.T) {
		text := line(t, 1, 2)
		_, err := collect(t, DecodeBatch(gzipped(t, text+text[:5])))
		if err == nil || !strings.Contains(err.Error(), "ends inside log line 2") {
			t.Fatalf("err = %v, want it to end inside log line 2", err)
		}
		_ = isError[*LogFormatError](t, err)
	})

	t.Run("refuse sequences that do not increase", func(t *testing.T) {
		text := line(t, 3, 4)
		_, err := collect(t, DecodeBatch(gzipped(t, text+text)))
		if err == nil || !strings.Contains(err.Error(), "does not increase") {
			t.Fatalf("err = %v, want a sequence that does not increase", err)
		}
	})

	t.Run("decode a commit only when it is read", func(t *testing.T) {
		text := line(t, 1, 2) + line(t, 2, 3) + line(t, 3, 4)
		commits := DecodeBatch(gzipped(t, text+"not json\n"))
		defer func() { _ = commits.Close() }()
		first, ok, err := commits.Next(background)
		noError(t, err)
		if !ok || first.Seq != 1 {
			t.Fatalf("first = %+v, %v, want seq 1", first, ok)
		}
	})

	t.Run("refuse a body that is no gzip", func(t *testing.T) {
		_, err := collect(t, DecodeBatch(chunked([]byte("plain text\n"), 4)))
		if err == nil {
			t.Fatal("a body that is no gzip decoded")
		}
	})
}

func TestLogLines(t *testing.T) {
	t.Run("refuse unknown versions and a missing next id", func(t *testing.T) {
		for _, text := range []string{`{"v":1,"seq":1,"nextId":2,"writes":[]}`, `{"v":2,"seq":1,"writes":[]}`} {
			_, err := DecodeLogLine(text, 1)
			_ = isError[*LogFormatError](t, err)
		}
		commit, err := DecodeLogLine(`{"v":2,"seq":1,"nextId":2,"writes":[]}`, 1)
		noError(t, err)
		if commit.Seq != 1 || commit.NextID != 2 || len(commit.Writes) != 0 {
			t.Fatalf("commit = %+v, want {1, 2, []}", commit)
		}
	})

	t.Run("refuse what is no line of an outbox commit", func(t *testing.T) {
		for _, text := range []string{"not json", "[]", "null", `{"v":2,"seq":0,"nextId":2,"writes":[]}`, `{"v":2,"seq":1.5,"nextId":2,"writes":[]}`, `{"v":2,"seq":1,"nextId":2,"writes":{}}`} {
			_, err := DecodeLogLine(text, 7)
			if err == nil || !strings.Contains(err.Error(), "Log line 7") {
				t.Fatalf("%s: err = %v, want a log line 7 error", text, err)
			}
		}
	})

	t.Run("encode a line as the JSON of its envelope", func(t *testing.T) {
		encoded, err := EncodeLogLine(store.OutboxCommit{Seq: 3, NextID: 9, Writes: []store.StoreWrite{}})
		noError(t, err)
		if string(encoded) != `{"v":2,"seq":3,"nextId":9,"writes":[]}`+"\n" {
			t.Fatalf("line = %q", encoded)
		}
	})
}

func TestSnapshotBodies(t *testing.T) {
	t.Run("round-trip a store archive", func(t *testing.T) {
		s := seeded(t)
		want := reads(t, s)
		export, err := s.Export(background)
		noError(t, err)
		decoded := DecodeSnapshot(EncodeSnapshot(export))
		defer func() { _ = decoded.Close() }()
		var got []string
		for {
			part, ok, err := decoded.Next(background)
			noError(t, err)
			if !ok {
				break
			}
			text, err := jsstring.MarshalJSON(part)
			noError(t, err)
			got = append(got, string(text))
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("decoded %q, want %q", got, want)
		}
		if !strings.Contains(got[0], `"kind":"manifest"`) {
			t.Fatalf("first part = %s, want the manifest", got[0])
		}
	})

	t.Run("give the store's commit queue back when the body is not read to the end", func(t *testing.T) {
		s := seeded(t)
		export, err := s.Export(background)
		noError(t, err)
		body := EncodeSnapshot(export)
		_, _, err = body.Next(background)
		noError(t, err)
		noError(t, body.Close())
		key := "c"
		address := store.DocumentAddress{Namespace: "chord", Owner: "app.sync", Kind: "note", Key: &key}
		base := store.DocumentBase{Version: 1, Fingerprint: "f1", Value: deltaObject("key", "c")}
		committed := make(chan error, 1)
		go func() {
			_, err := chordOf(s).Commit([]store.StagedWrite{store.CreateDocument(address, base, "{}", nil)}, openFence, background)
			committed <- err
		}()
		noError(t, <-committed)
	})
}

var _ context.Context = background
