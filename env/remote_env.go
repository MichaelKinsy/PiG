package env

// Ports packages/env/src/remote-env.ts

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
	"sync"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"

	"github.com/MichaelKinsy/PiG/internal/jsnumber"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

const (
	maxTimeoutMs      = 2_147_483_647
	maxTimeoutSeconds = maxTimeoutMs / 1000.0
	// readChunk is the bytes per `pread` request of a binary reader; several are in flight at once.
	readChunk = 256 * 1024
	readDepth = 8
	// writeChunk is what Node's writeFile writes at most per call, checking for an abort before each.
	writeChunk = 512 * 1024
	writeDepth = 8
	// Node's readFile: chunk of known-size reads, chunk of unknown-size reads, and the largest file it reads.
	readFileChunk        = 512 * 1024
	readFileUnknownChunk = 64 * 1024
	readFileMax          = 1<<31 - 1
	// lineChunk is NodeTextLineReader's read size.
	lineChunk = 64 * 1024
	// maxSafeInteger is JavaScript's Number.MAX_SAFE_INTEGER.
	maxSafeInteger = 1<<53 - 1
)

// RemoteExecutionEnvOptions configure a RemoteExecutionEnv.
type RemoteExecutionEnvOptions struct {
	Connection *Connection
	// ID is the file namespace: equal ids see the same files (FileSystem.Id), e.g. `pi-env:<env name>`.
	ID  string
	Cwd string
	// ShellPath is an explicit shell on the remote machine.
	ShellPath string
	// ShellEnv is added to the remote environment of every command that inherits it, like NodeExecutionEnv's.
	ShellEnv map[string]string
	// Watch is how the daemon watches, like NodeExecutionEnv's watch option.
	Watch RemoteWatchOptions
}

// remoteFileInfo is an info record from the daemon.
type remoteFileInfo struct {
	kind      string
	size      float64
	mtimeSec  float64
	mtimeNsec float64
}

func parseFileInfo(value any) remoteFileInfo {
	fields, _ := value.(Json)
	kind, _ := fields["kind"].(string)
	number := func(key string) float64 {
		result, _ := fields[key].(float64)
		return result
	}
	return remoteFileInfo{kind: kind, size: number("size"), mtimeSec: number("mtimeSec"), mtimeNsec: number("mtimeNsec")}
}

func toInfo(path string, remote remoteFileInfo, paths remotePaths) (durableenv.FileInfo, error) {
	if remote.kind == "other" {
		return durableenv.FileInfo{}, durableenv.NewFileError(durableenv.FileErrorInvalid, "Unsupported file type", path, nil)
	}
	return durableenv.FileInfo{
		Name:    paths.basename(path),
		Path:    path,
		Kind:    durableenv.FileKind(remote.kind),
		Size:    int64(remote.size),
		MtimeMs: remote.mtimeSec*1000 + remote.mtimeNsec/1e6,
	}, nil
}

func concat(chunks [][]byte, total int) []byte {
	if len(chunks) == 1 {
		return chunks[0]
	}
	joined := make([]byte, 0, total)
	for _, chunk := range chunks {
		joined = append(joined, chunk...)
	}
	return joined
}

type inFlightRead struct {
	length int
	call   *Call
}

// readRange reads length bytes from offset as consecutive reads of at most chunk bytes, several in flight. It ends
// early at the end of the file. A short read continues from where it ended, as sequential reads would. aborted is
// checked after each read; ok is false when it was.
func readRange(read func(offset, length int) *Call, offset, length, chunk int, aborted func() bool) (chunks [][]byte, total int, ok bool, err error) {
	var inFlight []inFlightRead
	next := offset
	for total < length {
		for len(inFlight) < readDepth && next < offset+length {
			size := min(offset+length-next, chunk)
			inFlight = append(inFlight, inFlightRead{size, read(next, size)})
			next += size
		}
		head := inFlight[0]
		inFlight = inFlight[1:]
		reply, err := head.call.Wait()
		if err != nil {
			return nil, 0, false, err
		}
		if aborted() {
			return nil, 0, false, nil
		}
		if len(reply.Payload) == 0 {
			break
		}
		chunks = append(chunks, reply.Payload)
		total += len(reply.Payload)
		if len(reply.Payload) < head.length {
			// Reads already in flight assumed a full read; continue right after this one.
			inFlight = nil
			next = offset + total
		}
	}
	return chunks, total, true, nil
}

// handle is a daemon handle: valid only in the session that opened it.
type handle struct {
	env     *RemoteExecutionEnv
	id      float64
	session int
	path    string
}

func (h *handle) start(ctx context.Context, op string, request Json, payload []byte) *Call {
	body := make(Json, len(request)+1)
	maps.Copy(body, request)
	body["handle"] = h.id
	return h.env.Connection.Start(ctx, op, body, RequestOptions{Payload: payload, Session: h.session})
}

func (h *handle) request(ctx context.Context, op string, request Json, payload []byte) (Reply, error) {
	return h.start(ctx, op, request, payload).Wait()
}

func (h *handle) preadStart(offset, length int) *Call {
	request := Json{"length": length}
	if offset >= 0 {
		request["offset"] = offset
	}
	return h.start(context.Background(), "pread", request, nil)
}

// pread reads at offset; a negative offset reads at the current position.
func (h *handle) pread(offset, length int) ([]byte, error) {
	reply, err := h.preadStart(offset, length).Wait()
	return reply.Payload, err
}

func (h *handle) close() {
	_, _ = h.request(context.Background(), "close", nil, nil)
}

func isSafeInteger(value int64) bool { return value >= -maxSafeInteger && value <= maxSafeInteger }

type remoteBinaryReader struct {
	handle *handle
	mu     sync.Mutex
	closed bool
}

func (r *remoteBinaryReader) isClosed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closed
}

func (r *remoteBinaryReader) early(ctx context.Context) error {
	if err := abortError(ctx, r.handle.path); err != nil {
		return err
	}
	if r.isClosed() {
		return durableenv.NewFileError(durableenv.FileErrorInvalid, "Binary reader is closed", r.handle.path, nil)
	}
	return nil
}

func (r *remoteBinaryReader) Info(ctx context.Context) (durableenv.FileInfo, error) {
	if err := r.early(ctx); err != nil {
		return durableenv.FileInfo{}, err
	}
	reply, err := r.handle.request(ctx, "fstat", nil, nil)
	if err != nil {
		return durableenv.FileInfo{}, toFileError(err, r.handle.path)
	}
	paths, err := remotePath(ctx, r.handle.env.Connection)
	if err != nil {
		return durableenv.FileInfo{}, toFileError(err, r.handle.path)
	}
	info, err := toInfo(r.handle.path, parseFileInfo(reply.JSON), paths)
	if err != nil {
		return durableenv.FileInfo{}, err
	}
	return info, nil
}

func (r *remoteBinaryReader) Read(ctx context.Context, offset, length int64) ([]byte, error) {
	if err := r.early(ctx); err != nil {
		return nil, err
	}
	if !isSafeInteger(offset) || offset < 0 || !isSafeInteger(length) || length < 0 {
		return nil, durableenv.NewFileError(durableenv.FileErrorInvalid, "Offset and length must be non-negative safe integers", r.handle.path, nil)
	}
	chunks, total, ok, err := readRange(
		func(at, size int) *Call { return r.handle.preadStart(at, size) },
		int(offset), int(length), readChunk,
		func() bool { return ctx.Err() != nil },
	)
	if err != nil {
		return nil, toFileError(err, r.handle.path)
	}
	if !ok {
		return nil, durableenv.NewFileError(durableenv.FileErrorAborted, "aborted", r.handle.path, nil)
	}
	if total == 0 {
		return []byte{}, nil
	}
	return concat(chunks, total), nil
}

func (r *remoteBinaryReader) ScanLines(ctx context.Context, options durableenv.ScanLinesOptions) (durableenv.LineScan, error) {
	if err := r.early(ctx); err != nil {
		return durableenv.LineScan{}, err
	}
	start, end := options.StartLine, options.EndLine
	if start < 0 || start > maxSafeInteger || (end != nil && (*end > maxSafeInteger || *end <= start)) {
		return durableenv.LineScan{}, durableenv.NewFileError(durableenv.FileErrorInvalid, "Invalid line range", r.handle.path, nil)
	}
	request := Json{"startLine": start}
	if end != nil {
		request["endLine"] = *end
	}
	reply, err := r.handle.request(ctx, "scanLines", request, nil)
	if err != nil {
		return durableenv.LineScan{}, toFileError(err, r.handle.path)
	}
	number := func(key string) int64 {
		value, _ := reply.JSON[key].(float64)
		return int64(value)
	}
	return durableenv.LineScan{
		Newlines: number("newlines"), Start: number("start"), End: number("end"), FirstLineEnd: number("firstLineEnd"),
		LastLineStart: number("lastLineStart"), SelectedBytes: number("selectedBytes"), FirstLineBytes: number("firstLineBytes"),
	}, nil
}

func (r *remoteBinaryReader) Close(context.Context) error {
	r.mu.Lock()
	already := r.closed
	r.closed = true
	r.mu.Unlock()
	if !already {
		r.handle.close()
	}
	return nil
}

type remoteDirReader struct {
	handle *handle
	mu     sync.Mutex
	done   bool
	closed bool
}

func (r *remoteDirReader) Next(ctx context.Context, maxEntries int) (durableenv.DirPage, error) {
	path := r.handle.path
	if err := abortError(ctx, path); err != nil {
		return durableenv.DirPage{}, err
	}
	r.mu.Lock()
	closed := r.closed
	r.mu.Unlock()
	if closed {
		return durableenv.DirPage{}, durableenv.NewFileError(durableenv.FileErrorInvalid, "Directory reader is closed", path, nil)
	}
	if maxEntries <= 0 {
		return durableenv.DirPage{}, durableenv.NewFileError(durableenv.FileErrorInvalid, "maxEntries must be a positive safe integer", path, nil)
	}
	entries := []durableenv.FileInfo{}
	paths, err := remotePath(ctx, r.handle.env.Connection)
	if err != nil {
		return durableenv.DirPage{}, toFileError(err, path)
	}
	// Like NodeDirReader, count only entries that are part of the listing.
	for !r.done && len(entries) < maxEntries {
		reply, err := r.handle.request(ctx, "readdir", Json{"max": maxEntries - len(entries)}, nil)
		if err != nil {
			return durableenv.DirPage{}, toFileError(err, path)
		}
		if abortErr := abortError(ctx, path); abortErr != nil {
			return durableenv.DirPage{}, abortErr
		}
		listed, _ := reply.JSON["entries"].([]any)
		for _, item := range listed {
			entry, _ := item.(Json)
			name, _ := entry["name"].(string)
			entryPath, resolveErr := paths.resolve(path, name)
			if resolveErr != nil {
				return durableenv.DirPage{}, toFileError(resolveErr, path)
			}
			if failure, failed := entry["error"].(Json); failed {
				// Removed between enumeration and lstat: not part of the listing any more.
				if failure["code"] == "ENOENT" {
					continue
				}
				return durableenv.DirPage{}, toFileError(NewRemoteError(failure), entryPath)
			}
			if info, infoErr := toInfo(entryPath, parseFileInfo(entry["info"]), paths); infoErr == nil {
				entries = append(entries, info)
			}
		}
		r.done = reply.JSON["done"] == true
	}
	return durableenv.DirPage{Entries: entries, Done: r.done}, nil
}

func (r *remoteDirReader) Close(context.Context) error {
	r.mu.Lock()
	already := r.closed
	r.closed = true
	r.mu.Unlock()
	if !already {
		r.handle.close()
	}
	return nil
}

// remoteTextLineReader is NodeTextLineReader: positional reads of a file opened like open(path, "r").
type remoteTextLineReader struct {
	handle   *handle
	decoder  *durableenv.StreamDecoder
	offset   int
	buffered string
	ended    bool
	closed   bool
	mu       sync.Mutex
}

func (r *remoteTextLineReader) ReadLine(ctx context.Context) (*durableenv.TextLine, error) {
	path := r.handle.path
	if err := abortError(ctx, path); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, durableenv.NewFileError(durableenv.FileErrorInvalid, "Text line reader is closed", path, nil)
	}
	for {
		if newline := strings.IndexByte(r.buffered, '\n'); newline != -1 {
			text := r.buffered[:newline]
			r.buffered = r.buffered[newline+1:]
			return &durableenv.TextLine{Text: text, Terminated: true}, nil
		}
		if r.ended {
			if r.buffered == "" {
				return nil, nil //nolint:nilnil // nil marks the end of the file
			}
			text := r.buffered
			r.buffered = ""
			return &durableenv.TextLine{Text: text}, nil
		}
		bytes, err := r.handle.pread(r.offset, lineChunk)
		if err != nil {
			return nil, toFileError(err, path)
		}
		if abortErr := abortError(ctx, path); abortErr != nil {
			return nil, abortErr
		}
		r.offset += len(bytes)
		if len(bytes) == 0 {
			r.buffered += r.decoder.End()
			r.ended = true
		} else {
			r.buffered += r.decoder.Decode(bytes)
		}
	}
}

func (r *remoteTextLineReader) Close(context.Context) error {
	r.mu.Lock()
	already := r.closed
	r.closed = true
	r.buffered = ""
	r.mu.Unlock()
	if !already {
		r.handle.close()
	}
	return nil
}

// RemoteExecutionEnv is an ExecutionEnv on another machine, reached through a pi-env daemon. Results match
// NodeExecutionEnv running on that machine: the daemon performs the system calls, and this type applies Node's path,
// error, and result rules.
type RemoteExecutionEnv struct {
	// Connection is the connection to the daemon.
	Connection *Connection

	id           string
	shellPath    string
	shellEnv     map[string]string
	watchOptions RemoteWatchOptions

	mu  sync.Mutex
	cwd string
	// running maps the commands this environment started to their sessions, for Cleanup.
	running map[uint32]int
}

var _ durableenv.ExecutionEnv = (*RemoteExecutionEnv)(nil)

// NewRemoteExecutionEnv returns an environment that works on the machine options.Connection reaches.
func NewRemoteExecutionEnv(options RemoteExecutionEnvOptions) *RemoteExecutionEnv {
	return &RemoteExecutionEnv{
		Connection: options.Connection, id: options.ID, cwd: options.Cwd, shellPath: options.ShellPath,
		shellEnv: options.ShellEnv, watchOptions: options.Watch, running: map[uint32]int{},
	}
}

// Id is the file namespace: equal ids see the same files.
func (e *RemoteExecutionEnv) Id() string { return e.id }

// Cwd is the directory relative paths resolve against.
func (e *RemoteExecutionEnv) Cwd() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cwd
}

// SetCwd changes the directory relative paths resolve against.
func (e *RemoteExecutionEnv) SetCwd(cwd string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cwd = cwd
}

// fileOp resolves path, checks for an abort, and runs run; with checkAfter, an abort during run also fails.
func fileOp[T any](ctx context.Context, e *RemoteExecutionEnv, path string, checkAfter bool, run func(resolved string) (T, error)) (T, error) {
	var zero T
	resolved, err := e.resolvePath(ctx, path)
	if err != nil {
		return zero, toFileError(err, path)
	}
	if abort := abortError(ctx, resolved); abort != nil {
		return zero, abort
	}
	value, err := run(resolved)
	if err != nil {
		return zero, toFileError(err, resolved)
	}
	if checkAfter {
		if abort := abortError(ctx, resolved); abort != nil {
			return zero, abort
		}
	}
	return value, nil
}

func (e *RemoteExecutionEnv) open(ctx context.Context, resolved string, request Json) (*handle, Json, error) {
	body := Json{"path": resolved}
	maps.Copy(body, request)
	reply, err := e.Connection.Request(ctx, "open", body, RequestOptions{})
	if err != nil {
		return nil, nil, err
	}
	id, _ := reply.JSON["handle"].(float64)
	return &handle{env: e, id: id, session: reply.Session, path: resolved}, reply.JSON, nil
}

// AbsolutePath resolves path without requiring it to exist.
func (e *RemoteExecutionEnv) AbsolutePath(ctx context.Context, path string) (string, error) {
	resolved, err := e.resolvePath(ctx, path)
	if err != nil {
		return "", toFileError(err, path)
	}
	return resolved, nil
}

// JoinPath joins and normalizes path segments like the remote system's path.join.
func (e *RemoteExecutionEnv) JoinPath(ctx context.Context, parts []string) (string, error) {
	paths, err := remotePath(ctx, e.Connection)
	if err != nil {
		return "", toFileError(err, "")
	}
	return paths.join(parts...), nil
}

// readFile is Node's readFile, with or without UTF-8 decoding, over a file opened like open(path, "r").
func (e *RemoteExecutionEnv) readFile(ctx context.Context, path string, encoding bool) ([]byte, string, error) {
	type read struct {
		bytes []byte
		text  string
	}
	result, err := fileOp(ctx, e, path, false, func(resolved string) (read, error) {
		file, reply, err := e.open(ctx, resolved, Json{"mode": "read"})
		if err != nil {
			return read{}, err
		}
		defer file.close()
		aborted := func() bool { return ctx.Err() != nil }
		abort := func() error {
			return durableenv.NewFileError(durableenv.FileErrorAborted, "The operation was aborted", resolved, nil)
		}
		if failure, failed := reply["statError"].(Json); failed {
			return read{}, NewRemoteError(failure)
		}
		if aborted() {
			return read{}, abort()
		}
		stat := parseFileInfo(reply["stat"])
		size := 0.0
		if stat.kind == "file" {
			size = stat.size
		}
		if size > readFileMax {
			return read{}, durableenv.NewFileError(durableenv.FileErrorUnknown, fmt.Sprintf("File size (%s) is greater than 2 GiB", jsnumber.String(size)), resolved, nil)
		}
		if size == 0 {
			// Unknown size: sequential reads until the end, as for FIFOs and devices. Node's readFile decodes with a
			// StringDecoder, which keeps a byte-order mark.
			decoder := durableenv.NewRangeDecoder()
			var chunks [][]byte
			total := 0
			var text strings.Builder
			for {
				if aborted() {
					return read{}, abort()
				}
				bytes, err := file.pread(-1, readFileUnknownChunk)
				if err != nil {
					return read{}, err
				}
				if len(bytes) == 0 {
					break
				}
				total += len(bytes)
				chunks = append(chunks, bytes)
				if encoding {
					text.WriteString(decoder.Decode(bytes))
				}
			}
			if !encoding {
				return read{bytes: concat(chunks, total)}, nil
			}
			if total == 0 {
				return read{}, nil
			}
			return read{text: text.String() + decoder.Flush()}, nil
		}
		fileSize := int(size)
		if !encoding {
			// A single read of the whole size, or reads of 512 KiB until the size or the end of the file.
			if fileSize <= readFileChunk {
				bytes, err := file.pread(0, fileSize)
				return read{bytes: bytes}, err
			}
			chunks, total, ok, err := readRange(func(at, length int) *Call { return file.preadStart(at, length) }, 0, fileSize, readFileChunk, aborted)
			if err != nil {
				return read{}, err
			}
			if !ok {
				return read{}, abort()
			}
			return read{bytes: concat(chunks, total)}, nil
		}
		// Decoding: reads of min(size, 512 KiB) until the size, a short read, or the end of the file.
		length := min(fileSize, readFileChunk)
		decoder := durableenv.NewRangeDecoder()
		var text strings.Builder
		total := 0
		first := true
		var inFlight []*Call
		next := 0
		for {
			for len(inFlight) < readDepth {
				inFlight = append(inFlight, file.preadStart(next, length))
				next += length
			}
			reply, err := inFlight[0].Wait()
			inFlight = inFlight[1:]
			if err != nil {
				return read{}, err
			}
			if aborted() {
				return read{}, abort()
			}
			bytes := reply.Payload
			total += len(bytes)
			if len(bytes) == 0 || total == fileSize || len(bytes) != length {
				if first {
					return read{text: jsstring.FromUTF8(bytes)}, nil
				}
				return read{text: text.String() + decoder.Decode(bytes) + decoder.Flush()}, nil
			}
			text.WriteString(decoder.Decode(bytes))
			first = false
		}
	})
	return result.bytes, result.text, err
}

// ReadTextFile reads path as UTF-8 text.
func (e *RemoteExecutionEnv) ReadTextFile(ctx context.Context, path string) (string, error) {
	_, text, err := e.readFile(ctx, path, true)
	return text, err
}

// ReadBinaryFile reads path as bytes.
func (e *RemoteExecutionEnv) ReadBinaryFile(ctx context.Context, path string) ([]byte, error) {
	bytes, _, err := e.readFile(ctx, path, false)
	if err != nil {
		return nil, err
	}
	if bytes == nil {
		bytes = []byte{}
	}
	return bytes, nil
}

// OpenBinaryReader opens a regular file for bounded positional reads.
func (e *RemoteExecutionEnv) OpenBinaryReader(ctx context.Context, path string, options *durableenv.OpenBinaryReaderOptions) (durableenv.BinaryReader, error) {
	noFollow := options != nil && options.NoFollow
	opened, err := fileOp(ctx, e, path, false, func(resolved string) (*handle, error) {
		file, _, err := e.open(ctx, resolved, Json{"noFollow": noFollow})
		return file, err
	})
	if err != nil {
		return nil, err
	}
	if abort := abortError(ctx, opened.path); abort != nil {
		opened.close()
		return nil, abort
	}
	return &remoteBinaryReader{handle: opened}, nil
}

// OpenTextLineReader opens path for pull-based line reading.
func (e *RemoteExecutionEnv) OpenTextLineReader(ctx context.Context, path string) (durableenv.TextLineReader, error) {
	opened, err := fileOp(ctx, e, path, false, func(resolved string) (*handle, error) {
		file, _, err := e.open(ctx, resolved, Json{"mode": "read"})
		return file, err
	})
	if err != nil {
		return nil, err
	}
	if abort := abortError(ctx, opened.path); abort != nil {
		opened.close()
		return nil, abort
	}
	return &remoteTextLineReader{handle: opened, decoder: durableenv.NewStreamDecoder()}, nil
}

// ReadTextLines reads the first lines of path, or all of them.
func (e *RemoteExecutionEnv) ReadTextLines(ctx context.Context, path string, options *durableenv.ReadTextLinesOptions) ([]string, error) {
	var maxLines *int
	if options != nil {
		maxLines = options.MaxLines
	}
	if maxLines != nil && *maxLines <= 0 {
		return []string{}, nil
	}
	reader, err := e.OpenTextLineReader(ctx, path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = reader.Close(ctx) }()
	lines := []string{}
	for maxLines == nil || len(lines) < *maxLines {
		line, err := reader.ReadLine(ctx)
		if err != nil {
			return nil, err
		}
		if line == nil {
			break
		}
		lines = append(lines, line.Text)
	}
	return lines, nil
}

// write is Node's writeFile (abort checked before each 512 KiB write) or appendFile (no checks, one after): the first
// request creates parents, opens, and writes the first chunk; later chunks go to the same open file.
func (e *RemoteExecutionEnv) write(ctx context.Context, path string, content any, appendMode bool) error {
	var bytes []byte
	switch typed := content.(type) {
	case string:
		bytes = []byte(typed)
	case []byte:
		bytes = typed
	default:
		return durableenv.NewFileError(durableenv.FileErrorInvalid, fmt.Sprintf("file content must be a string or []byte, not %T", content), "", nil)
	}
	signal := ctx
	if appendMode {
		signal = context.Background()
	}
	_, err := fileOp(ctx, e, path, appendMode, func(resolved string) (struct{}, error) {
		first := bytes[:min(len(bytes), writeChunk)]
		keep := len(bytes) > writeChunk
		reply, err := e.Connection.Request(ctx, "write", Json{"path": resolved, "append": appendMode, "keep": keep}, RequestOptions{Payload: first})
		if err != nil || !keep {
			return struct{}{}, err
		}
		id, _ := reply.JSON["handle"].(float64)
		file := &handle{env: e, id: id, session: reply.Session, path: resolved}
		var inFlight []*Call
		settleAll := func() error {
			var first error
			for _, call := range inFlight {
				if _, err := call.Wait(); err != nil && first == nil {
					first = err
				}
			}
			inFlight = nil
			return first
		}
		defer func() {
			_ = settleAll()
			file.close()
		}()
		for offset := writeChunk; offset < len(bytes); offset += writeChunk {
			if signal.Err() != nil {
				// A chunk that failed before this check fails the write, as it would have in sequence.
				if failed := settleAll(); failed != nil {
					return struct{}{}, failed
				}
				return struct{}{}, durableenv.NewFileError(durableenv.FileErrorAborted, "The operation was aborted", resolved, nil)
			}
			inFlight = append(inFlight, file.start(context.Background(), "writeChunk", nil, bytes[offset:min(len(bytes), offset+writeChunk)]))
			if len(inFlight) >= writeDepth {
				head := inFlight[0]
				inFlight = inFlight[1:]
				if _, err := head.Wait(); err != nil {
					return struct{}{}, err
				}
			}
		}
		// The first failure; later chunks are refused by the daemon, so the file has no gaps.
		for len(inFlight) > 0 {
			head := inFlight[0]
			inFlight = inFlight[1:]
			if _, err := head.Wait(); err != nil {
				return struct{}{}, err
			}
		}
		return struct{}{}, nil
	})
	return err
}

// WriteFile creates or overwrites path with content, a string or []byte, creating parent directories.
func (e *RemoteExecutionEnv) WriteFile(ctx context.Context, path string, content any) error {
	return e.write(ctx, path, content, false)
}

// AppendFile creates or appends to path with content, a string or []byte, creating parent directories.
func (e *RemoteExecutionEnv) AppendFile(ctx context.Context, path string, content any) error {
	return e.write(ctx, path, content, true)
}

// TruncateFile truncates or extends path to exactly size bytes.
func (e *RemoteExecutionEnv) TruncateFile(ctx context.Context, path string, size int64) error {
	_, err := fileOp(ctx, e, path, true, func(resolved string) (struct{}, error) {
		if size < 0 || size > maxSafeInteger {
			return struct{}{}, durableenv.NewFileError(durableenv.FileErrorInvalid, "File size must be a non-negative safe integer", resolved, nil)
		}
		_, err := e.Connection.Request(ctx, "truncate", Json{"path": resolved, "size": size}, RequestOptions{})
		return struct{}{}, err
	})
	return err
}

// FlushFile flushes the contents of path to stable storage.
func (e *RemoteExecutionEnv) FlushFile(ctx context.Context, path string) error {
	_, err := fileOp(ctx, e, path, true, func(resolved string) (struct{}, error) {
		_, err := e.Connection.Request(ctx, "fsync", Json{"path": resolved}, RequestOptions{})
		return struct{}{}, err
	})
	return err
}

// RenameFile atomically renames sourcePath over destinationPath.
func (e *RemoteExecutionEnv) RenameFile(ctx context.Context, sourcePath, destinationPath string) error {
	source, err := e.resolvePath(ctx, sourcePath)
	var destination string
	if err == nil {
		destination, err = e.resolvePath(ctx, destinationPath)
	}
	if err != nil {
		return toFileError(err, sourcePath)
	}
	if abort := abortError(ctx, destination); abort != nil {
		return abort
	}
	if _, err := e.Connection.Request(ctx, "rename", Json{"path": source, "to": destination}, RequestOptions{}); err != nil {
		return toFileError(err, source)
	}
	return nil
}

// FileInfo returns metadata without following symlinks.
func (e *RemoteExecutionEnv) FileInfo(ctx context.Context, path string) (durableenv.FileInfo, error) {
	return fileOp(ctx, e, path, false, func(resolved string) (durableenv.FileInfo, error) {
		reply, err := e.Connection.Request(ctx, "lstat", Json{"path": resolved}, RequestOptions{})
		if err != nil {
			return durableenv.FileInfo{}, err
		}
		paths, err := remotePath(ctx, e.Connection)
		if err != nil {
			return durableenv.FileInfo{}, err
		}
		return toInfo(resolved, parseFileInfo(reply.JSON), paths)
	})
}

// ListDir lists direct children without following symlinks; unsupported file types are omitted.
func (e *RemoteExecutionEnv) ListDir(ctx context.Context, path string) ([]durableenv.FileInfo, error) {
	return fileOp(ctx, e, path, false, func(resolved string) ([]durableenv.FileInfo, error) {
		file, err := e.openDir(ctx, resolved)
		if err != nil {
			return nil, err
		}
		var entries []Json
		func() {
			defer file.close()
			for done := false; !done; {
				var reply Reply
				reply, err = file.request(ctx, "readdir", Json{"max": 1000}, nil)
				if err != nil {
					return
				}
				listed, _ := reply.JSON["entries"].([]any)
				for _, item := range listed {
					entry, _ := item.(Json)
					entries = append(entries, entry)
				}
				done = reply.JSON["done"] == true
			}
		}()
		if err != nil {
			return nil, err
		}
		// Node's readdir fails on any entry it cannot lstat. libuv sorts names by their bytes on POSIX; on Windows it
		// keeps the file system's order, which the daemon reports.
		paths, err := remotePath(ctx, e.Connection)
		if err != nil {
			return nil, err
		}
		if !paths.windows {
			key := func(entry Json) []byte {
				if raw, ok := entry["raw"].([]any); ok {
					bytes := make([]byte, len(raw))
					for index, value := range raw {
						number, _ := value.(float64)
						bytes[index] = byte(number)
					}
					return bytes
				}
				name, _ := entry["name"].(string)
				return []byte(name)
			}
			slices.SortStableFunc(entries, func(a, b Json) int { return bytes.Compare(key(a), key(b)) })
		}
		infos := []durableenv.FileInfo{}
		for _, entry := range entries {
			if ctx.Err() != nil {
				return nil, durableenv.NewFileError(durableenv.FileErrorAborted, "aborted", resolved, nil)
			}
			name, _ := entry["name"].(string)
			entryPath, err := paths.resolve(resolved, name)
			if err != nil {
				return nil, err
			}
			if failure, failed := entry["error"].(Json); failed {
				return nil, toFileError(NewRemoteError(failure), entryPath)
			}
			if info, infoErr := toInfo(entryPath, parseFileInfo(entry["info"]), paths); infoErr == nil {
				infos = append(infos, info)
			}
		}
		return infos, nil
	})
}

func (e *RemoteExecutionEnv) openDir(ctx context.Context, resolved string) (*handle, error) {
	reply, err := e.Connection.Request(ctx, "opendir", Json{"path": resolved}, RequestOptions{})
	if err != nil {
		return nil, err
	}
	id, _ := reply.JSON["handle"].(float64)
	return &handle{env: e, id: id, session: reply.Session, path: resolved}, nil
}

// OpenDirReader opens path for paged directory listings.
func (e *RemoteExecutionEnv) OpenDirReader(ctx context.Context, path string) (durableenv.DirReader, error) {
	opened, err := fileOp(ctx, e, path, false, func(resolved string) (*handle, error) { return e.openDir(ctx, resolved) })
	if err != nil {
		return nil, err
	}
	if abort := abortError(ctx, opened.path); abort != nil {
		opened.close()
		return nil, abort
	}
	return &remoteDirReader{handle: opened}, nil
}

// CanonicalPath resolves every symlink in an existing path.
func (e *RemoteExecutionEnv) CanonicalPath(ctx context.Context, path string) (string, error) {
	return fileOp(ctx, e, path, false, func(resolved string) (string, error) {
		reply, err := e.Connection.Request(ctx, "realpath", Json{"path": resolved}, RequestOptions{})
		if err != nil {
			return "", err
		}
		canonical, _ := reply.JSON["path"].(string)
		return canonical, nil
	})
}

// Exists reports false for missing paths and other failures as errors.
func (e *RemoteExecutionEnv) Exists(ctx context.Context, path string) (bool, error) {
	_, err := e.FileInfo(ctx, path)
	if err == nil {
		return true, nil
	}
	if fileError, ok := errors.AsType[*durableenv.FileError](err); ok && fileError.Code == durableenv.FileErrorNotFound {
		return false, nil
	}
	return false, err
}

// CreateDir creates a directory; Recursive defaults to true.
func (e *RemoteExecutionEnv) CreateDir(ctx context.Context, path string, options *durableenv.CreateDirOptions) error {
	recursive := options == nil || options.Recursive == nil || *options.Recursive
	_, err := fileOp(ctx, e, path, false, func(resolved string) (struct{}, error) {
		_, err := e.Connection.Request(ctx, "mkdir", Json{"path": resolved, "recursive": recursive}, RequestOptions{})
		return struct{}{}, err
	})
	return err
}

// Remove deletes a file or directory; a directory requires Recursive and a missing path requires Force.
func (e *RemoteExecutionEnv) Remove(ctx context.Context, path string, options *durableenv.RemoveOptions) error {
	recursive, force := false, false
	if options != nil {
		recursive, force = options.Recursive, options.Force
	}
	_, err := fileOp(ctx, e, path, false, func(resolved string) (struct{}, error) {
		_, err := e.Connection.Request(ctx, "rm", Json{"path": resolved, "recursive": recursive, "force": force}, RequestOptions{})
		return struct{}{}, err
	})
	return err
}

// CreateTempDir creates a directory named prefix plus six random characters under the remote temporary directory;
// prefix defaults to "tmp-".
func (e *RemoteExecutionEnv) CreateTempDir(ctx context.Context, prefix *string) (string, error) {
	if abort := abortError(ctx, ""); abort != nil {
		return "", abort
	}
	info, err := e.Connection.Info(ctx)
	if err != nil {
		return "", toFileError(err, "")
	}
	paths := remotePaths{windows: info.OS == "windows"}
	name := "tmp-"
	if prefix != nil {
		name = *prefix
	}
	reply, err := e.Connection.Request(ctx, "mkdtemp", Json{"path": paths.join(info.Tmpdir, name)}, RequestOptions{})
	if err != nil {
		return "", toFileError(err, "")
	}
	created, _ := reply.JSON["path"].(string)
	return created, nil
}

// CreateTempFile creates an empty file named prefix + UUID + suffix inside a fresh temporary directory.
func (e *RemoteExecutionEnv) CreateTempFile(ctx context.Context, options *durableenv.CreateTempFileOptions) (string, error) {
	prefix := "tmp-"
	directory, err := e.CreateTempDir(ctx, &prefix)
	if err != nil {
		return "", err
	}
	if options == nil {
		options = &durableenv.CreateTempFileOptions{}
	}
	paths, err := remotePath(ctx, e.Connection)
	filePath := ""
	if err == nil {
		filePath = paths.join(directory, options.Prefix+randomUUID()+options.Suffix)
		// Node's writeFile(filePath, ""), which does not create parents.
		_, err = e.Connection.Request(ctx, "write", Json{"path": filePath, "append": false, "parents": false}, RequestOptions{Payload: []byte{}})
	}
	if err != nil {
		return "", toFileError(err, filePath)
	}
	return filePath, nil
}

// randomUUID returns an RFC 9562 version 4 UUID.
func randomUUID() string {
	var bytes [16]byte
	_, _ = rand.Read(bytes[:])
	bytes[6] = bytes[6]&0x0f | 0x40
	bytes[8] = bytes[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", bytes[0:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:16])
}

// Watch reports changes to files and directories; the daemon watches next to the files.
func (e *RemoteExecutionEnv) Watch(ctx context.Context, targets []durableenv.WatchTarget, onChange func(durableenv.WatchChange)) (durableenv.FileWatcher, error) {
	if abort := abortError(ctx, ""); abort != nil {
		return nil, abort
	}
	resolved := make([]durableenv.WatchTarget, len(targets))
	for index, target := range targets {
		path, err := e.resolvePath(ctx, target.Path)
		if err != nil {
			return nil, toFileError(err, "")
		}
		resolved[index] = target
		resolved[index].Path = path
	}
	if abort := abortError(ctx, ""); abort != nil {
		return nil, abort
	}
	watcher, err := OpenRemoteWatcher(ctx, e.Connection, resolved, onChange, e.watchOptions)
	if err != nil {
		return nil, toFileError(err, "")
	}
	if abort := abortError(ctx, ""); abort != nil {
		_ = watcher.Close(ctx)
		return nil, abort
	}
	return watcher, nil
}

// Exec runs a command on the remote machine. A string runs through its shell; a []string runs its first element
// directly with the rest as its arguments.
func (e *RemoteExecutionEnv) Exec(ctx context.Context, command any, options *durableenv.ShellExecOptions) (durableenv.ShellExecResult, error) {
	if ctx.Err() != nil {
		return durableenv.ShellExecResult{}, durableenv.NewExecutionError(durableenv.ExecutionErrorAborted, "aborted", nil)
	}
	if options == nil {
		options = &durableenv.ShellExecOptions{}
	}
	if timeout := options.Timeout; timeout != nil {
		if math.IsNaN(*timeout) || math.IsInf(*timeout, 0) || *timeout <= 0 {
			return durableenv.ShellExecResult{}, durableenv.NewExecutionError(durableenv.ExecutionErrorTimeout, "Invalid timeout: must be a finite number of seconds", nil)
		}
		if *timeout*1000 > maxTimeoutMs {
			return durableenv.ShellExecResult{}, durableenv.NewExecutionError(durableenv.ExecutionErrorTimeout, "Invalid timeout: maximum is "+jsnumber.String(maxTimeoutSeconds)+" seconds", nil)
		}
	}
	inheritEnv := options.InheritEnv == nil || *options.InheritEnv
	cwd := e.Cwd()
	if options.Cwd != "" {
		resolved, err := e.resolvePath(ctx, options.Cwd)
		if err != nil {
			return durableenv.ShellExecResult{}, durableenv.NewExecutionError(durableenv.ExecutionErrorUnknown, err.Error(), nil)
		}
		cwd = resolved
	}
	if ctx.Err() != nil {
		return durableenv.ShellExecResult{}, durableenv.NewExecutionError(durableenv.ExecutionErrorAborted, "aborted", nil)
	}
	environment := Json{}
	if inheritEnv {
		for key, value := range e.shellEnv {
			environment[key] = value
		}
	}
	for key, value := range options.Env {
		environment[key] = value
	}
	request := Json{"cwd": cwd, "env": environment, "inheritEnv": inheritEnv}
	switch typed := command.(type) {
	case string:
		request["command"] = typed
	case []string:
		request["argv"] = slices.Clone(typed)
	default:
		return durableenv.ShellExecResult{}, durableenv.NewExecutionError(durableenv.ExecutionErrorSpawnError, fmt.Sprintf("command must be a string or []string, not %T", command), nil)
	}
	if e.shellPath != "" {
		request["shellPath"] = e.shellPath
	}
	if options.Timeout != nil {
		request["timeoutMs"] = *options.Timeout * 1000
	}
	if options.Spill != nil {
		request["spill"] = Json{"afterBytes": options.Spill.AfterBytes, "afterLines": options.Spill.AfterLines}
	}
	if window := options.Window; window != nil {
		request["window"] = Json{"maxBytes": window.MaxBytes, "maxLines": window.MaxLines, "minIntervalMs": window.MinIntervalMs, "bytesPerSecond": window.BytesPerSecond}
	}

	var (
		stateMu       sync.Mutex
		callbackError *durableenv.ExecutionError
		settled       bool
		running       uint32
		hasRunning    bool
	)
	// The request's own context: cancelled by the caller's, or by a failing callback.
	requestContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopForwarding := context.AfterFunc(ctx, cancel)
	defer stopForwarding()
	onEvent := func(event Json, payload []byte) {
		stateMu.Lock()
		skip := settled || callbackError != nil
		stateMu.Unlock()
		if skip || event["kind"] != "output" {
			return
		}
		text := jsstring.FromUTF8(payload)
		if text == "" || options.OnOutput == nil {
			return
		}
		info := durableenv.ShellOutputInfo{Stream: durableenv.ShellStdout}
		if event["stream"] == "stderr" {
			info.Stream = durableenv.ShellStderr
		}
		if skipped, ok := event["skipped"].(Json); ok {
			bytes, _ := skipped["bytes"].(float64)
			newlines, _ := skipped["newlines"].(float64)
			info.Skipped = &durableenv.ShellOutputSkip{Bytes: int(bytes), Newlines: int(newlines), EndsWithNewline: skipped["endsWithNewline"] == true}
		}
		if err := callOnOutput(options.OnOutput, ctx, text, info); err != nil {
			stateMu.Lock()
			callbackError = durableenv.NewExecutionError(durableenv.ExecutionErrorCallbackError, err.Error(), err)
			stateMu.Unlock()
			cancel()
		}
	}
	defer func() {
		stateMu.Lock()
		settled = true
		current, started := running, hasRunning
		stateMu.Unlock()
		if started {
			e.mu.Lock()
			delete(e.running, current)
			e.mu.Unlock()
		}
	}()
	reply, err := e.Connection.Request(requestContext, "exec", request, RequestOptions{
		OnEvent: onEvent,
		OnStart: func(id uint32, session int) {
			stateMu.Lock()
			running, hasRunning = id, true
			stateMu.Unlock()
			e.mu.Lock()
			e.running[id] = session
			e.mu.Unlock()
		},
	})
	stateMu.Lock()
	failedCallback := callbackError
	stateMu.Unlock()
	if failedCallback != nil {
		return durableenv.ShellExecResult{}, failedCallback
	}
	if err != nil {
		return durableenv.ShellExecResult{}, execFailure(err, options.Timeout)
	}
	exitCode, _ := reply.JSON["exitCode"].(float64)
	result := durableenv.ShellExecResult{ExitCode: int(exitCode)}
	if spill, ok := reply.JSON["spillPath"].(string); ok {
		result.SpillPath = spill
	}
	return result, nil
}

// callOnOutput delivers text to the caller's callback; a panic is what a throw is upstream.
func callOnOutput(onOutput func(context.Context, string, durableenv.ShellOutputInfo), ctx context.Context, text string, info durableenv.ShellOutputInfo) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = durableenv.ToError(recovered)
		}
	}()
	onOutput(ctx, text, info)
	return nil
}

// execFailure maps the daemon's error of a command to an ExecutionError.
func execFailure(err error, timeout *float64) *durableenv.ExecutionError {
	remote, ok := errors.AsType[*RemoteError](err)
	if !ok {
		return durableenv.NewExecutionError(durableenv.ExecutionErrorUnknown, err.Error(), nil)
	}
	var failure *durableenv.ExecutionError
	switch remote.Code {
	case "timeout":
		seconds := "undefined"
		if timeout != nil {
			seconds = jsnumber.String(*timeout)
		}
		failure = durableenv.NewExecutionError(durableenv.ExecutionErrorTimeout, "timeout:"+seconds, nil)
	case "aborted":
		failure = durableenv.NewExecutionError(durableenv.ExecutionErrorAborted, "aborted", nil)
	case "shell_unavailable", "spawn_error":
		failure = durableenv.NewExecutionError(durableenv.ExecutionErrorCode(remote.Code), remote.Message, nil)
	default:
		failure = durableenv.NewExecutionError(durableenv.ExecutionErrorUnknown, remote.Message, nil)
	}
	if spill, ok := remote.Fields["spillPath"].(string); ok {
		failure.SpillPath = spill
	}
	return failure
}

// Cleanup kills the commands this environment started without aborting them, so they settle with their killed status,
// as NodeExecutionEnv does.
func (e *RemoteExecutionEnv) Cleanup(context.Context) error {
	e.mu.Lock()
	running := e.running
	e.running = map[uint32]int{}
	e.mu.Unlock()
	for id, session := range running {
		e.Connection.Kill(id, session)
	}
	return nil
}
