package daemon

// Ports packages/env/daemon/src/main.rs

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
	"github.com/MichaelKinsy/PiG/durable/env/node"
)

const (
	// Protocol is the wire protocol version.
	Protocol = 1

	pingInterval   = 5 * time.Second
	silenceLimit   = 30 * time.Second
	scanChunk      = 64 * 1024
	workers        = 16
	maxHandles     = 4096
	controlPayload = 64 * 1024
)

// Options configure Serve.
type Options struct {
	// Version is what `hello` reports as version: the release the daemon shipped with.
	Version string
	// PingInterval and SilenceLimit default to the protocol's 5 s and 30 s.
	PingInterval time.Duration
	SilenceLimit time.Duration
	// OnSilence runs when the client went silent for the limit, after everything it started was killed; it defaults to
	// exiting the process, as the daemon does.
	OnSilence func()
}

type fileHandle struct {
	file *os.File
	path string
	// failed poisons a write handle after a failed chunk write, so later chunks cannot leave a gap.
	failed atomic.Bool
	// serial runs chunk writes one at a time in arrival order, though the client sends several at once.
	mu      sync.Mutex
	jobs    []func()
	running bool
}

type dirHandle struct {
	mu      sync.Mutex
	file    *os.File
	path    string
	pending []string
	done    bool
}

type server struct {
	out     *output
	options Options
	tmpdir  string

	mu         sync.Mutex
	files      map[uint64]*fileHandle
	dirs       map[uint64]*dirHandle
	nextHandle uint64
	controls   map[uint32]*control
	runs       map[*node.NodeExecutionEnv]struct{}
	// requests joins the requests that run on their own goroutines, which an ended connection cancels.
	requests sync.WaitGroup
}

func field(request Object, key string) (string, *Failure) {
	text, ok := request[key].(string)
	if !ok {
		return "", newFailure("EINVAL", "missing field "+key)
	}
	return text, nil
}

func number(request Object, key string) (uint64, *Failure) {
	value, ok := unsignedNumber(request[key])
	if !ok {
		return 0, newFailure("EINVAL", "missing or invalid field "+key)
	}
	return value, nil
}

func flag(request Object, key string) bool {
	value, _ := request[key].(bool)
	return value
}

func (s *server) trackRun(environment *node.NodeExecutionEnv) {
	s.mu.Lock()
	s.runs[environment] = struct{}{}
	s.mu.Unlock()
}

func (s *server) untrackRun(environment *node.NodeExecutionEnv) {
	s.mu.Lock()
	delete(s.runs, environment)
	s.mu.Unlock()
}

// killAll kills every command this connection started.
func (s *server) killAll() {
	s.mu.Lock()
	running := make([]*node.NodeExecutionEnv, 0, len(s.runs))
	for environment := range s.runs {
		running = append(running, environment)
	}
	s.mu.Unlock()
	for _, environment := range running {
		_ = environment.Cleanup(context.Background())
	}
}

// cancelAll aborts every request still running: commands are killed and watchers stop.
func (s *server) cancelAll() {
	s.mu.Lock()
	controls := make([]*control, 0, len(s.controls))
	for _, ctl := range s.controls {
		controls = append(controls, ctl)
	}
	s.mu.Unlock()
	for _, ctl := range controls {
		ctl.cancelRequest(false)
	}
}

func (s *server) file(request Object) (*fileHandle, *Failure) {
	id, failure := number(request, "handle")
	if failure != nil {
		return nil, failure
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if handle, ok := s.files[id]; ok {
		return handle, nil
	}
	if _, ok := s.dirs[id]; ok {
		return nil, newFailure("EBADF", "not a file handle")
	}
	return nil, newFailure("EBADF", "unknown handle")
}

func (s *server) insertFile(file *os.File, path string) (uint64, *Failure) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.files)+len(s.dirs) >= maxHandles {
		return 0, newFailure("EMFILE", "EMFILE: too many open files")
	}
	id := s.nextHandle
	s.nextHandle++
	s.files[id] = &fileHandle{file: file, path: path}
	return id, nil
}

func (s *server) insertDir(handle *dirHandle) (uint64, *Failure) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.files)+len(s.dirs) >= maxHandles {
		return 0, newFailure("EMFILE", "EMFILE: too many open files")
	}
	id := s.nextHandle
	s.nextHandle++
	s.dirs[id] = handle
	return id, nil
}

func (s *server) closeHandle(id uint64) {
	s.mu.Lock()
	file, isFile := s.files[id]
	dir, isDir := s.dirs[id]
	delete(s.files, id)
	delete(s.dirs, id)
	s.mu.Unlock()
	switch {
	case isFile:
		_ = file.file.Close()
	case isDir:
		_ = dir.file.Close()
	}
}

func (s *server) closeAll() {
	s.mu.Lock()
	files, dirs := s.files, s.dirs
	s.files, s.dirs = map[uint64]*fileHandle{}, map[uint64]*dirHandle{}
	s.mu.Unlock()
	for _, handle := range files {
		_ = handle.file.Close()
	}
	for _, handle := range dirs {
		_ = handle.file.Close()
	}
}

// dispatch runs one request; payload is the request's raw bytes.
func (s *server) dispatch(id uint32, request any, payload []byte, ctl *control) (Object, []byte, *Failure) {
	object, ok := request.(Object)
	if request == nil {
		return nil, nil, newFailure("EINVAL", "invalid JSON")
	}
	if !ok {
		object = Object{}
	}
	op, _ := object["op"].(string)
	plain := func(value Object, failure *Failure) (Object, []byte, *Failure) { return value, nil, failure }
	path := func() (string, *Failure) { return field(object, "path") }
	switch op {
	case "hello":
		return plain(s.hello(), nil)
	case "lstat":
		p, failure := path()
		if failure != nil {
			return nil, nil, failure
		}
		return plain(lstat(p))
	case "realpath":
		p, failure := path()
		if failure != nil {
			return nil, nil, failure
		}
		return plain(realpath(p))
	case "write":
		return s.opWrite(object, payload)
	case "writeChunk":
		handle, failure := s.file(object)
		if failure != nil {
			return nil, nil, failure
		}
		if handle.failed.Load() {
			return nil, nil, newFailure("EBADF", "an earlier write failed")
		}
		if _, err := handle.file.Write(payload); err != nil {
			handle.failed.Store(true)
			return nil, nil, ioFailure(err, "write", handle.path)
		}
		return plain(Object{}, nil)
	case "truncate":
		p, failure := path()
		if failure != nil {
			return nil, nil, failure
		}
		size, failure := number(object, "size")
		if failure != nil {
			return nil, nil, failure
		}
		return plain(truncate(p, int64(min(size, 1<<62))))
	case "fsync":
		p, failure := path()
		if failure != nil {
			return nil, nil, failure
		}
		return plain(fsync(p))
	case "rename":
		from, failure := path()
		if failure != nil {
			return nil, nil, failure
		}
		to, failure := field(object, "to")
		if failure != nil {
			return nil, nil, failure
		}
		return plain(rename(from, to))
	case "mkdir":
		p, failure := path()
		if failure != nil {
			return nil, nil, failure
		}
		return plain(mkdir(p, flag(object, "recursive")))
	case "rm":
		p, failure := path()
		if failure != nil {
			return nil, nil, failure
		}
		return plain(rm(p, flag(object, "recursive"), flag(object, "force")))
	case "mkdtemp":
		p, failure := path()
		if failure != nil {
			return nil, nil, failure
		}
		return plain(mkdtemp(p))
	case "open":
		return s.opOpen(object)
	case "pread":
		return s.opPread(object)
	case "fstat":
		handle, failure := s.file(object)
		if failure != nil {
			return nil, nil, failure
		}
		stats, err := handle.file.Stat()
		if err != nil {
			return nil, nil, ioFailure(err, "fstat", handle.path)
		}
		return plain(info(handle.path, stats, true), nil)
	case "scanLines":
		handle, failure := s.file(object)
		if failure != nil {
			return nil, nil, failure
		}
		return plain(scanLines(handle, object, ctl))
	case "opendir":
		p, failure := path()
		if failure != nil {
			return nil, nil, failure
		}
		return s.opOpendir(p)
	case "readdir":
		return plain(s.readDir(object))
	case "close":
		id, failure := number(object, "handle")
		if failure != nil {
			return nil, nil, failure
		}
		s.closeHandle(id)
		return plain(Object{}, nil)
	case "exec":
		request, failure := parseExecRequest(object)
		if failure != nil {
			return nil, nil, failure
		}
		return plain(s.runExec(id, request, ctl))
	case "watch":
		return plain(s.runWatch(id, object, ctl))
	}
	return nil, nil, newFailure("EINVAL", fmt.Sprintf("unknown operation %s", op))
}

// pig divergence (D96): the Go daemon spells its system as Rust's std::env::consts do, so the client sees what Pi's daemon reports.
func (s *server) hello() Object {
	cwd, _ := os.Getwd()
	return Object{
		"protocol":  Protocol,
		"version":   s.options.Version,
		"os":        osName(),
		"arch":      archName(),
		"home":      home(),
		"separator": pathSeparator,
		"tmpdir":    s.tmpdir,
		"cwd":       cwd,
		"driveCwds": driveCwds(),
		"pid":       os.Getpid(),
	}
}

// osName is Rust's std::env::consts::OS for this system.
func osName() string {
	if targetOS == "darwin" {
		return "macos"
	}
	return targetOS
}

// archName is Rust's std::env::consts::ARCH for this system.
func archName() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "aarch64"
	}
	return runtime.GOARCH
}

func (s *server) opWrite(request Object, payload []byte) (Object, []byte, *Failure) {
	p, failure := field(request, "path")
	if failure != nil {
		return nil, nil, failure
	}
	parents := true
	if value, ok := request["parents"].(bool); ok {
		parents = value
	}
	file, failure := writeFile(p, flag(request, "append"), parents, payload)
	if failure != nil {
		return nil, nil, failure
	}
	if !flag(request, "keep") {
		_ = file.Close()
		return Object{}, nil, nil
	}
	handle, failure := s.insertFile(file, p)
	if failure != nil {
		_ = file.Close()
		return nil, nil, failure
	}
	return Object{"handle": handle}, nil, nil
}

func (s *server) opOpen(request Object) (Object, []byte, *Failure) {
	p, failure := field(request, "path")
	if failure != nil {
		return nil, nil, failure
	}
	if mode, _ := request["mode"].(string); mode == "read" {
		// Node's open(path, "r"): any kind of file, blocking; reads report what the file is.
		file, err := openForRead(p)
		if err != nil {
			return nil, nil, ioFailure(err, "open", p)
		}
		result := Object{}
		if stats, err := file.Stat(); err != nil {
			result["statError"] = ioFailure(err, "fstat", p).toJSON()
		} else {
			result["stat"] = info(p, stats, true)
		}
		handle, failure := s.insertFile(file, p)
		if failure != nil {
			_ = file.Close()
			return nil, nil, failure
		}
		result["handle"] = handle
		return result, nil, nil
	}
	file, fileInfo, failure := openReader(p, flag(request, "noFollow"))
	if failure != nil {
		return nil, nil, failure
	}
	handle, failure := s.insertFile(file, p)
	if failure != nil {
		_ = file.Close()
		return nil, nil, failure
	}
	return Object{"handle": handle, "info": fileInfo}, nil, nil
}

func (s *server) opPread(request Object) (Object, []byte, *Failure) {
	handle, failure := s.file(request)
	if failure != nil {
		return nil, nil, failure
	}
	requested, failure := number(request, "length")
	if failure != nil {
		return nil, nil, failure
	}
	length := int(min(requested, MaxPayload))
	var bytes []byte
	if offset, ok := unsignedNumber(request["offset"]); ok {
		bytes, failure = pread(handle.file, handle.path, int64(min(offset, 1<<62)), length)
	} else {
		bytes, failure = read(handle.file, handle.path, length)
	}
	if failure != nil {
		return nil, nil, failure
	}
	return Object{}, bytes, nil
}

func (s *server) opOpendir(p string) (Object, []byte, *Failure) {
	file, err := os.Open(p)
	if err == nil {
		var stats os.FileInfo
		if stats, err = file.Stat(); err == nil && !stats.IsDir() {
			err = synthetic("ENOTDIR")
		}
		if err != nil {
			_ = file.Close()
		}
	}
	if err != nil {
		return nil, nil, ioFailure(err, "opendir", p)
	}
	handle, failure := s.insertDir(&dirHandle{file: file, path: p})
	if failure != nil {
		_ = file.Close()
		return nil, nil, failure
	}
	return Object{"handle": handle}, nil, nil
}

func (s *server) readDir(request Object) (Object, *Failure) {
	id, failure := number(request, "handle")
	if failure != nil {
		return nil, failure
	}
	maxEntries, failure := number(request, "max")
	if failure != nil {
		return nil, failure
	}
	count := int(min(max(maxEntries, 1), 1<<30))
	// Lock only this directory, so other requests are not held up by a slow listing.
	s.mu.Lock()
	dir, ok := s.dirs[id]
	s.mu.Unlock()
	if !ok {
		return nil, newFailure("EBADF", "not a directory handle")
	}
	dir.mu.Lock()
	defer dir.mu.Unlock()
	entries := []Object{}
	for len(entries) < count {
		if len(dir.pending) == 0 && !dir.done {
			names, err := dir.file.Readdirnames(128)
			if err != nil && !errorsIsEOF(err) {
				return nil, ioFailure(err, "readdir", dir.path)
			}
			if len(names) == 0 {
				dir.done = true
			}
			dir.pending = names
		}
		if len(dir.pending) == 0 {
			break
		}
		entries = append(entries, dirEntry(dir.path, dir.pending[0]))
		dir.pending = dir.pending[1:]
	}
	return Object{"entries": entries, "done": dir.done && len(dir.pending) == 0}, nil
}

func errorsIsEOF(err error) bool { return err == io.EOF } //nolint:errorlint // Readdirnames returns io.EOF itself

func scanLines(handle *fileHandle, request Object, ctl *control) (Object, *Failure) {
	startLine, failure := number(request, "startLine")
	if failure != nil {
		return nil, failure
	}
	var endLine *int64
	if end, ok := unsignedNumber(request["endLine"]); ok {
		if end <= startLine {
			return nil, newFailure("EINVAL", "Invalid line range")
		}
		value := int64(min(end, 1<<62))
		endLine = &value
	}
	scanner, err := durableenv.NewLineScanner(int64(min(startLine, 1<<62)), endLine)
	if err != nil {
		return nil, newFailure("EINVAL", err.Error())
	}
	buffer := make([]byte, scanChunk)
	var position int64
	for {
		if ctl.aborted.Load() {
			return nil, newFailure("aborted", "aborted")
		}
		read, err := handle.file.ReadAt(buffer, position)
		if read > 0 {
			scanner.Push(buffer[:read])
			position += int64(read)
		}
		if errorsIsEOF(err) {
			break
		}
		if err != nil {
			return nil, ioFailure(err, "read", handle.path)
		}
	}
	scan := scanner.Finish()
	return Object{
		"newlines":       scan.Newlines,
		"start":          scan.Start,
		"end":            scan.End,
		"firstLineEnd":   scan.FirstLineEnd,
		"lastLineStart":  scan.LastLineStart,
		"selectedBytes":  scan.SelectedBytes,
		"firstLineBytes": scan.FirstLineBytes,
	}, nil
}

// respond runs one request and queues its reply.
func (s *server) respond(id uint32, request *Frame, ctl *control) {
	isExec := false
	if object, ok := request.JSON.(Object); ok {
		isExec = object["op"] == "exec"
	}
	value, payload, failure := s.dispatch(id, request.JSON, request.Payload, ctl)
	var reply *Frame
	if failure != nil {
		reply = &Frame{Kind: FrameError, ID: id, JSON: failure.toJSON()}
	} else {
		reply = &Frame{Kind: FrameResult, ID: id, JSON: value, Payload: payload}
	}
	s.mu.Lock()
	delete(s.controls, id)
	s.mu.Unlock()
	// A command's result follows its output; large payloads do not hold up pings and small replies.
	if isExec || len(reply.Payload) > controlPayload {
		s.out.sendBulk(reply, nil)
	} else {
		s.out.sendControl(reply)
	}
}

// jobQueue is the unbounded queue of the fixed worker pool, so reading frames never waits for a worker.
type jobQueue struct {
	mu   sync.Mutex
	cond *sync.Cond
	jobs []func()
	done bool
}

func newJobQueue() *jobQueue {
	queue := &jobQueue{}
	queue.cond = sync.NewCond(&queue.mu)
	return queue
}

func (q *jobQueue) push(job func()) {
	q.mu.Lock()
	q.jobs = append(q.jobs, job)
	q.mu.Unlock()
	q.cond.Signal()
}

func (q *jobQueue) close() {
	q.mu.Lock()
	q.done = true
	q.mu.Unlock()
	q.cond.Broadcast()
}

func (q *jobQueue) next() (func(), bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.jobs) == 0 && !q.done {
		q.cond.Wait()
	}
	if len(q.jobs) == 0 {
		return nil, false
	}
	job := q.jobs[0]
	q.jobs = q.jobs[1:]
	return job, true
}

// runInOrder queues job behind the handle's earlier chunk writes; one pool job drains the queue.
func runInOrder(queue *jobQueue, handle *fileHandle, job func()) {
	handle.mu.Lock()
	handle.jobs = append(handle.jobs, job)
	if handle.running {
		handle.mu.Unlock()
		return
	}
	handle.running = true
	handle.mu.Unlock()
	queue.push(func() {
		for {
			handle.mu.Lock()
			var next func()
			if len(handle.jobs) > 0 {
				next = handle.jobs[0]
				handle.jobs = handle.jobs[1:]
			} else {
				handle.running = false
			}
			handle.mu.Unlock()
			if next == nil {
				return
			}
			next()
		}
	})
}

// serveFrames reads requests until input ends. Frame workers are joined by the caller through the returned wait.
func (s *server) serveFrames(input io.Reader) error {
	queue := newJobQueue()
	defer queue.close()
	for range workers {
		go func() {
			for {
				job, ok := queue.next()
				if !ok {
					return
				}
				job()
			}
		}()
	}
	for {
		request, err := ReadFrame(input)
		if err != nil || request == nil {
			return err
		}
		switch request.Kind {
		case FrameRequest:
			id := request.ID
			// Registered before the request runs, so a cancel right behind it is not lost.
			ctl := newControl()
			s.mu.Lock()
			s.controls[id] = ctl
			s.mu.Unlock()
			object, _ := request.JSON.(Object)
			switch object["op"] {
			case "exec", "watch":
				// Commands and watchers last; they get their own goroutines.
				s.requests.Go(func() { s.respond(id, request, ctl) })
			case "writeChunk":
				job := func() { s.respond(id, request, ctl) }
				if handle, failure := s.file(object); failure == nil {
					runInOrder(queue, handle, job)
				} else {
					queue.push(job)
				}
			default:
				queue.push(func() { s.respond(id, request, ctl) })
			}
		case FrameCancel:
			s.mu.Lock()
			ctl := s.controls[request.ID]
			s.mu.Unlock()
			if ctl != nil {
				object, _ := request.JSON.(Object)
				mode, _ := object["mode"].(string)
				ctl.cancelRequest(mode == "kill")
			}
		}
	}
}

// seen is stdin that records when bytes last arrived, so a large frame arriving slowly counts as a live client.
type seen struct {
	reader   io.Reader
	lastSeen *atomic.Int64
}

func (s seen) Read(buffer []byte) (int, error) {
	read, err := s.reader.Read(buffer)
	if read > 0 {
		s.lastSeen.Store(time.Now().UnixMilli())
	}
	return read, err
}

// Serve runs one connection: it writes the sync line `PI-ENV <token>`, then answers frames from input on output until
// input ends. Everything the client started is killed when it returns.
func Serve(input io.Reader, output io.Writer, token string, options Options) error {
	if _, err := fmt.Fprintf(output, "PI-ENV %s\n", token); err != nil {
		return err
	}
	if flusher, ok := output.(interface{ Flush() error }); ok {
		if err := flusher.Flush(); err != nil {
			return err
		}
	}
	if options.PingInterval == 0 {
		options.PingInterval = pingInterval
	}
	if options.SilenceLimit == 0 {
		options.SilenceLimit = silenceLimit
	}
	out := newOutput()
	var writer sync.WaitGroup
	writer.Go(func() { out.run(output) })
	s := &server{
		out:        out,
		options:    options,
		tmpdir:     tmpdir(),
		files:      map[uint64]*fileHandle{},
		dirs:       map[uint64]*dirHandle{},
		nextHandle: 1,
		controls:   map[uint32]*control{},
		runs:       map[*node.NodeExecutionEnv]struct{}{},
	}
	var lastSeen atomic.Int64
	lastSeen.Store(time.Now().UnixMilli())
	stop := make(chan struct{})
	var pings sync.WaitGroup
	pings.Go(func() {
		ticker := time.NewTicker(options.PingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
			}
			out.sendControl(&Frame{Kind: FramePing, JSON: Object{}})
			// A client that went silent (a phone that lost its network) leaves nothing running.
			if time.Since(time.UnixMilli(lastSeen.Load())) > options.SilenceLimit {
				s.killAll()
				if options.OnSilence != nil {
					options.OnSilence()
				} else {
					os.Exit(0)
				}
				return
			}
		}
	})
	err := s.serveFrames(bufio.NewReaderSize(seen{reader: input, lastSeen: &lastSeen}, 1<<20))
	// However input ended, the client is gone: stop everything it started.
	close(stop)
	pings.Wait()
	s.cancelAll()
	s.killAll()
	s.requests.Wait()
	s.closeAll()
	out.close()
	writer.Wait()
	return err
}
