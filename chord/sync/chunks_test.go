package sync

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// How long a body's chunk lasts (store.ByteSource: a chunk is valid until the next Next or Close). Go forced difference:
// Pi's bodies are streams whose chunks are arrays of their own; the gzip body, the gunzip body and the chunks of lines
// here fill one buffer again, so a consumer that keeps a chunk past the next Next reads the bytes of a later one (an
// upload that kept its chunks wrote a batch that no longer decoded). Both sides are held here: a body does fill the
// chunk it handed out again, and nothing in this package that reads a body depends on a chunk past the next Next.

// numbered is n commits with no writes, seq 1 to n. The first noisy ones have NextIDs that compress badly, so a body's
// first compressed chunk is its largest and every later one fits the buffer that held it.
func numbered(n, noisy int) []store.OutboxCommit {
	commits := make([]store.OutboxCommit, n)
	state := uint64(88172645463325252)
	for i := range commits {
		id := int64(7)
		if i < noisy {
			// xorshift64: the same sequence in every run; 52 bits, a JSON number keeps them.
			state ^= state << 13
			state ^= state >> 7
			state ^= state << 17
			id = int64(state >> 12)
		}
		commits[i] = store.OutboxCommit{Seq: int64(i + 1), NextID: id, Writes: []store.StoreWrite{}}
	}
	return commits
}

// linesOf is commits as their log lines.
func linesOf(t *testing.T, commits []store.OutboxCommit) string {
	t.Helper()
	var text strings.Builder
	for _, commit := range commits {
		line, err := EncodeLogLine(commit)
		noError(t, err)
		text.WriteString(line)
		text.WriteByte('\n')
	}
	return text.String()
}

// spoiled is body with each chunk ended where store.ByteSource ends it: the chunk it handed out is overwritten at the
// next Next and at Close, so a consumer that keeps one reads other bytes. chunks counts the chunks handed out.
type spoiled struct {
	body   SyncBody
	last   []byte
	chunks int
}

func spoil(body SyncBody) *spoiled { return &spoiled{body: body} }

func (body *spoiled) Next(ctx context.Context) ([]byte, bool, error) {
	body.end()
	chunk, ok, err := body.body.Next(ctx)
	if err != nil || !ok {
		return chunk, ok, err
	}
	body.last = bytes.Clone(chunk)
	body.chunks++
	return body.last, true, nil
}

func (body *spoiled) Close() error {
	body.end()
	return body.body.Close()
}

func (body *spoiled) end() {
	for i := range body.last {
		body.last[i] = 0xff
	}
	body.last = nil
}

func TestAGzipBodyFillsTheChunkItHandedOutAgainAtTheNextNext(t *testing.T) {
	commits := numbered(6000, 1500)
	body := EncodeBatch(SliceCommits(commits))
	defer func() { _ = body.Close() }()
	first, ok, err := body.Next(background)
	noError(t, err)
	if !ok || len(first) == 0 {
		t.Fatal("the body has no first chunk")
	}
	copied := bytes.Clone(first)
	all := bytes.Clone(first)
	chunks := 1
	for {
		chunk, ok, err := body.Next(background)
		noError(t, err)
		if !ok {
			break
		}
		if chunks == 1 && bytes.Equal(first, copied) {
			t.Fatal("the first chunk still holds its bytes after the next Next: the gzip body hands out chunks of their own now, so store.ByteSource's comment (a chunk is valid until the next Next or Close) and this test say less than the bodies give")
		}
		chunks++
		all = append(all, chunk...)
	}
	if chunks < 2 {
		t.Fatalf("the body had %d chunk: the test needs two", chunks)
	}
	// Copied before the next Next, the chunks are the body.
	decoded, err := collect(t, DecodeBatch(chunked(all, len(all))))
	noError(t, err)
	if len(decoded) != len(commits) || linesOf(t, decoded) != linesOf(t, commits) {
		t.Fatalf("the chunks copied as they came decode to %d commits that are not the %d encoded", len(decoded), len(commits))
	}
}

func TestNothingThatReadsABodyKeepsAChunkPastTheNextNext(t *testing.T) {
	commits := numbered(6000, 1500)
	plain, err := collect(t, DecodeBatch(EncodeBatch(SliceCommits(commits))))
	noError(t, err)
	if len(plain) != len(commits) {
		t.Fatalf("decoded %d commits of %d", len(plain), len(commits))
	}
	want := linesOf(t, plain)
	// check has source give the commits, read from bodies whose chunks ended at each next Next (two at least, each).
	check := func(t *testing.T, source CommitSource, bodies ...*spoiled) {
		t.Helper()
		decoded, err := collect(t, source)
		if err != nil {
			t.Fatalf("a chunk was read after the Next that ended it: %v", err)
		}
		if linesOf(t, decoded) != want {
			t.Fatalf("a chunk was read after the Next that ended it: %d commits that are not the %d encoded", len(decoded), len(commits))
		}
		for _, body := range bodies {
			if body.chunks < 2 {
				t.Fatalf("a body handed out %d chunk: the test needs two", body.chunks)
			}
		}
	}
	t.Run("the gzip body, of the chunks of lines", func(t *testing.T) {
		lines := spoil(&lineChunks{commits: SliceCommits(commits)})
		check(t, DecodeBatch(gzipSource(lines)), lines)
	})
	t.Run("the gunzip body, of the compressed chunks", func(t *testing.T) {
		compressed := spoil(EncodeBatch(SliceCommits(commits)))
		check(t, DecodeBatch(compressed), compressed)
	})
	t.Run("a batch's lines, of the decompressed chunks", func(t *testing.T) {
		text := spoil(gunzipSource(EncodeBatch(SliceCommits(commits))))
		check(t, &batchCommits{lines: &batchLines{chunks: text}}, text)
	})
	t.Run("JSON lines, of the decompressed chunks", func(t *testing.T) {
		values := func(body SyncBody) []string {
			source := store.DecodeJsonl(body)
			defer func() { _ = source.Close() }()
			var out []string
			for {
				value, ok, err := source.Next(background)
				if err != nil {
					t.Fatalf("a chunk was read after the Next that ended it: %v", err)
				}
				if !ok {
					return out
				}
				text, err := jsstring.MarshalJSON(value)
				noError(t, err)
				out = append(out, string(text))
			}
		}
		text := spoil(gunzipSource(EncodeBatch(SliceCommits(commits))))
		got, unspoiled := values(text), values(chunked([]byte(want), len(want)))
		if len(unspoiled) != len(commits) || !slices.Equal(got, unspoiled) {
			t.Fatalf("a chunk was read after the Next that ended it: %d values that are not the %d encoded", len(got), len(unspoiled))
		}
		if text.chunks < 2 {
			t.Fatalf("the body handed out %d chunk: the test needs two", text.chunks)
		}
	})
	t.Run("the file system remote, of the body it writes", func(t *testing.T) {
		remote := newRemote(t)
		body := spoil(EncodeBatch(SliceCommits(commits)))
		noError(t, remote.PutSnapshot(background, session, 1, body))
		stored, err := remote.GetSnapshot(background, session, 1)
		noError(t, err)
		if stored == nil {
			t.Fatal("the remote has no body")
		}
		check(t, DecodeBatch(stored), body)
	})
}
