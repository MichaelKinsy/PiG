package sync

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/chord/src/sync/batch.ts
//
// Bodies on the remote, encoded and decoded as streams, so neither side holds a whole batch or snapshot in memory:
// gzip-compressed JSON lines, one outbox commit (a batch) or one archive part (a snapshot) per line. gzip, because it is
// what a browser viewer can read too.
//
// Async contract: Pi's bodies are `AsyncIterable<Uint8Array>` and its encoders async generators over a
// `CompressionStream`. Here a body is a store.ByteSource (a pull iterator: Next blocks for the next chunk, Close is the
// generator's `return()`, idempotent) and the encoders compress on the caller's goroutine as the body is read, with no
// goroutine of their own: a body holds no more than one chunk of lines and the compressor's window. A chunk is valid until
// the next call of Next.
//
// Sizes (a chunk of lines, a line's Bytes) count UTF-16 units, as Pi's `.length` does.

// SyncBody is a body on the remote: its bytes in chunks, read once.
type SyncBody = store.ByteSource

// CommitSource is a stream of outbox commits: Next blocks for the next commit (ok false at the end), Close ends the stream
// early (Pi's `return()`; idempotent).
type CommitSource interface {
	Next(ctx context.Context) (commit store.OutboxCommit, ok bool, err error)
	Close() error
}

// SliceCommits is commits as a CommitSource.
func SliceCommits(commits []store.OutboxCommit) CommitSource { return &sliceCommits{commits: commits} }

type sliceCommits struct{ commits []store.OutboxCommit }

func (source *sliceCommits) Next(context.Context) (store.OutboxCommit, bool, error) {
	if len(source.commits) == 0 {
		return store.OutboxCommit{}, false, nil
	}
	commit := source.commits[0]
	source.commits = source.commits[1:]
	return commit, true, nil
}

func (source *sliceCommits) Close() error { return nil }

// chunkBytes is the bytes a chunk of encoded lines collects before it is compressed.
const chunkBytes = 64 * 1024

// MaxStringLength is V8's longest string, in UTF-16 units: the most a line of a batch body may hold (see RangeError).
const MaxStringLength = 0x1fffffe8

// stringLimit is MaxStringLength; a variable so a test can reach it with small bodies.
var stringLimit = MaxStringLength

// RangeError is what Pi's runtime throws when a string it builds passes MaxStringLength: `RangeError: Invalid string length`.
// Pi's `lines()` raises it out of `rest += decode(chunk)` for a line that long, and its encoder out of `pending += line`; a
// line this port writes is bounded the same, so every line an upload writes pulls back.
type RangeError struct{}

func (*RangeError) Error() string { return "Invalid string length" }

// EncodeBatch is a batch body: commits as gzip-compressed JSON lines, read from commits only as the body is read.
func EncodeBatch(commits CommitSource) SyncBody { return gzipSource(&lineChunks{commits: commits}) }

// BatchLine is a commit of a batch body with the size of its line.
type BatchLine struct {
	Commit store.OutboxCommit
	// Bytes is the line's size with its newline, for callers that bound what they hold.
	Bytes int
}

// BatchLines is the lines of a batch body, in order, as they arrive.
type BatchLines interface {
	Next(ctx context.Context) (line BatchLine, ok bool, err error)
	Close() error
}

// DecodeBatch is the commits of a batch body, in order, as they arrive.
func DecodeBatch(body SyncBody) CommitSource { return &batchCommits{lines: DecodeBatchLines(body)} }

type batchCommits struct{ lines BatchLines }

func (source *batchCommits) Next(ctx context.Context) (store.OutboxCommit, bool, error) {
	line, ok, err := source.lines.Next(ctx)
	return line.Commit, ok, err
}

func (source *batchCommits) Close() error { return source.lines.Close() }

// DecodeBatchLines is the commits of a batch body with the size of each line, for callers that bound what they hold. A
// body that is no gzip, ends inside a line, has a line that is not a commit, or whose sequences do not increase fails with
// the Next that reaches it.
func DecodeBatchLines(body SyncBody) BatchLines { return &batchLines{chunks: gunzipSource(body)} }

type batchLines struct {
	chunks   SyncBody
	buffer   []byte
	consumed int
	// units is the buffer's unfinished text in UTF-16 units, up to whole characters (Pi's `rest.length` before a chunk).
	units int
	// held is the bytes of the buffer past `consumed` that units counts; the rest is a character still arriving.
	held     int
	number   int
	previous int64
	started  bool
	finished bool
}

func (lines *batchLines) Next(ctx context.Context) (BatchLine, bool, error) {
	for !lines.finished {
		if end := bytes.IndexByte(lines.buffer[lines.consumed:], '\n'); end >= 0 {
			text := lines.buffer[lines.consumed : lines.consumed+end]
			lines.consumed += end + 1
			lines.number++
			return lines.emit(text)
		}
		// Keep the unfinished line, and take the next chunk.
		if lines.consumed > 0 {
			lines.buffer = lines.buffer[:copy(lines.buffer, lines.buffer[lines.consumed:])]
			lines.consumed = 0
			// The lines taken left the unfinished text: count it again (it is no longer than a chunk).
			lines.held = 0
			lines.units = 0
			lines.count()
		}
		chunk, ok, err := lines.chunks.Next(ctx)
		if err != nil {
			lines.finished = true
			return BatchLine{}, false, err
		}
		if !ok {
			lines.finished = true
			if len(lines.buffer) > 0 {
				return BatchLine{}, false, logFormatError(nil, "The body ends inside log line %d", lines.number+1)
			}
			return BatchLine{}, false, nil
		}
		if !lines.started && len(chunk) > 0 {
			lines.started = true
			// A fatal TextDecoder drops the byte order mark at the start of its input.
			chunk = bytes.TrimPrefix(chunk, []byte("\uFEFF"))
		}
		lines.buffer = append(lines.buffer, chunk...)
		// Pi's `rest += decode(chunk)` throws once the text it holds passes V8's string length: all of the chunk's lines
		// are in `rest` until they are sliced off.
		lines.count()
		if lines.units > stringLimit {
			lines.finished = true
			return BatchLine{}, false, &RangeError{}
		}
	}
	return BatchLine{}, false, nil
}

// count adds the buffer's whole characters not counted yet to units: a character cut by the chunk's end is counted with
// the next chunk, as a streaming TextDecoder holds its bytes back.
func (lines *batchLines) count() {
	rest := lines.buffer[lines.held:]
	end := len(rest)
	// At most three bytes of an unfinished character: a continuation run behind a lead byte that is not whole yet.
	for back := 1; back <= min(3, len(rest)); back++ {
		if utf8.RuneStart(rest[len(rest)-back]) {
			if !utf8.FullRune(rest[len(rest)-back:]) {
				end = len(rest) - back
			}
			break
		}
	}
	lines.units += jsstring.LengthBytes(rest[:end])
	lines.held += end
}

// emit is the commit of one line, checked.
func (lines *batchLines) emit(text []byte) (BatchLine, bool, error) {
	fail := func(err error) (BatchLine, bool, error) {
		lines.finished = true
		return BatchLine{}, false, err
	}
	if !utf8.Valid(text) {
		return fail(logFormatError(nil, "Log line %d is not UTF-8", lines.number))
	}
	commit, err := DecodeLogLine(string(text), lines.number)
	if err != nil {
		return fail(err)
	}
	if commit.Seq <= lines.previous {
		return fail(logFormatError(nil, "Log line %d does not increase the sequence", lines.number))
	}
	lines.previous = commit.Seq
	return BatchLine{Commit: commit, Bytes: jsstring.LengthBytes(text) + 1}, true, nil
}

func (lines *batchLines) Close() error {
	lines.finished = true
	return lines.chunks.Close()
}

// EncodeSnapshot is a snapshot body: a store archive (`Store.Export`), one part per JSON line, gzip-compressed.
func EncodeSnapshot(parts store.PartIterator) SyncBody { return gzipSource(store.EncodeJsonl(parts)) }

// DecodeSnapshot is the parts of a snapshot body as they arrive, unchecked: ImportStore checks them (a trust boundary).
func DecodeSnapshot(body SyncBody) store.PartSource { return store.DecodeJsonl(gunzipSource(body)) }

// lineChunks is commits as chunks of whole encoded lines.
type lineChunks struct {
	commits CommitSource
	pending []byte
	done    bool
}

func (chunks *lineChunks) Next(ctx context.Context) ([]byte, bool, error) {
	if chunks.done {
		return nil, false, nil
	}
	chunks.pending = chunks.pending[:0]
	for len(chunks.pending) < chunkBytes {
		commit, ok, err := chunks.commits.Next(ctx)
		if err != nil {
			chunks.done = true
			_ = chunks.commits.Close()
			return nil, false, err
		}
		if !ok {
			chunks.done = true
			break
		}
		line, err := EncodeLogLine(commit)
		if err != nil {
			chunks.done = true
			_ = chunks.commits.Close()
			return nil, false, err
		}
		// Pi's `pending += line` throws past V8's string length; a line this port writes must pull back, so it is refused alike.
		if len(line)+len(chunks.pending) > stringLimit && jsstring.LengthBytes(line)+jsstring.LengthBytes(chunks.pending) > stringLimit {
			chunks.done = true
			_ = chunks.commits.Close()
			return nil, false, &RangeError{}
		}
		chunks.pending = append(chunks.pending, line...)
	}
	if len(chunks.pending) == 0 {
		return nil, false, nil
	}
	return chunks.pending, true, nil
}

func (chunks *lineChunks) Close() error {
	chunks.done = true
	return chunks.commits.Close()
}

// gzipBody is src compressed as it is read: a chunk of src in, a chunk of gzip out.
type gzipBody struct {
	src    store.ByteSource
	out    bytes.Buffer
	writer *gzip.Writer
	done   bool
}

func gzipSource(src store.ByteSource) SyncBody { return &gzipBody{src: src} }

func (body *gzipBody) Next(ctx context.Context) ([]byte, bool, error) {
	if body.writer == nil {
		body.writer = gzip.NewWriter(&body.out)
	}
	for !body.done {
		body.out.Reset()
		chunk, ok, err := body.src.Next(ctx)
		if err != nil {
			body.done = true
			_ = body.src.Close()
			return nil, false, err
		}
		if ok {
			if _, err := body.writer.Write(chunk); err != nil {
				body.done = true
				_ = body.src.Close()
				return nil, false, err
			}
			err = body.writer.Flush()
		} else {
			body.done = true
			err = body.writer.Close()
		}
		if err != nil {
			body.done = true
			_ = body.src.Close()
			return nil, false, err
		}
		if body.out.Len() > 0 {
			return body.out.Bytes(), true, nil
		}
	}
	return nil, false, nil
}

func (body *gzipBody) Close() error {
	body.done = true
	return body.src.Close()
}

// gunzipBody is src decompressed as it is read, a chunk of at most chunkBytes at a time.
type gunzipBody struct {
	src    store.ByteSource
	input  chunkReader
	reader *gzip.Reader
	buffer []byte
	done   bool
}

func gunzipSource(src store.ByteSource) SyncBody {
	return &gunzipBody{src: src, input: chunkReader{src: src}}
}

func (body *gunzipBody) Next(ctx context.Context) ([]byte, bool, error) {
	if body.done {
		return nil, false, nil
	}
	body.input.ctx = ctx
	if body.reader == nil {
		reader, err := gzip.NewReader(&body.input)
		if err != nil {
			return nil, false, body.fail(err)
		}
		body.reader = reader
		body.buffer = make([]byte, chunkBytes)
	}
	n := 0
	for n < len(body.buffer) {
		read, err := body.reader.Read(body.buffer[n:])
		n += read
		if errors.Is(err, io.EOF) {
			body.done = true
			break
		}
		if err != nil {
			return nil, false, body.fail(err)
		}
	}
	if n == 0 {
		return nil, false, nil
	}
	return body.buffer[:n], true, nil
}

// fail ends the body with err: a source's error as it is, a malformed gzip stream as a LogFormatError.
func (body *gunzipBody) fail(err error) error {
	body.done = true
	_ = body.src.Close()
	if body.input.err != nil {
		return body.input.err
	}
	return logFormatError(err, "The body is not a gzip stream: %v", err)
}

func (body *gunzipBody) Close() error {
	body.done = true
	if body.reader != nil {
		_ = body.reader.Close()
	}
	return body.src.Close()
}

// chunkReader is a byte source as an io.Reader, taking its context from the Next that reads.
type chunkReader struct {
	src   store.ByteSource
	ctx   context.Context
	rest  []byte
	ended bool
	err   error
}

func (reader *chunkReader) Read(p []byte) (int, error) {
	for len(reader.rest) == 0 {
		if reader.ended {
			return 0, io.EOF
		}
		chunk, ok, err := reader.src.Next(reader.ctx)
		if err != nil {
			reader.err = err
			return 0, err
		}
		if !ok {
			reader.ended = true
			return 0, io.EOF
		}
		reader.rest = chunk
	}
	n := copy(p, reader.rest)
	reader.rest = reader.rest[n:]
	return n, nil
}
