// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// Cmd matches Go's cmd/go/internal/cacheprog.Cmd wire command names (Go 1.24+).
type Cmd string

const (
	CmdGet   Cmd = "get"
	CmdPut   Cmd = "put"
	CmdClose Cmd = "close"
)

// Request matches cmd/go/internal/cacheprog.Request.
type Request struct {
	ID       int64  `json:"ID"`
	Command  Cmd    `json:"Command"`
	ActionID []byte `json:"ActionID,omitempty"`
	OutputID []byte `json:"OutputID,omitempty"`
	BodySize int64  `json:"BodySize,omitempty"`
}

// Response matches cmd/go/internal/cacheprog.Response.
type Response struct {
	ID            int64      `json:"ID"`
	Err           string     `json:"Err,omitempty"`
	KnownCommands []Cmd      `json:"KnownCommands,omitempty"`
	Miss          bool       `json:"Miss,omitempty"`
	OutputID      []byte     `json:"OutputID,omitempty"`
	Size          int64      `json:"Size,omitempty"`
	Time          *time.Time `json:"Time,omitempty"`
	DiskPath      string     `json:"DiskPath,omitempty"`
}

// ErrRemoteSkipped is returned by RemoteBackend.Put when a remote upload is
// intentionally skipped (for example because the artifact exceeds MaxObjectBytes
// or the remote circuit breaker is open). Local PUT still succeeds and the skip
// is tracked separately from RemotePuts and RemoteErrors.
var ErrRemoteSkipped = errors.New("gocacheprog: remote upload skipped")

// RemoteBackend is an optional remote cache store (e.g. S3/R2).
// All remote operations must be treated as best-effort by Server: a remote
// failure falls back cleanly to local disk and never fails the Go build.
type RemoteBackend interface {
	Get(ctx context.Context, actionHex string) (outputID []byte, body []byte, modTime time.Time, hit bool, err error)
	Put(ctx context.Context, actionHex string, outputID []byte, bodyPath string, bodySize int64) error
	ReadOnly() bool
}

// Stats tracks cache activity reported on close when verbose logging is enabled.
type Stats struct {
	LocalHits     int64
	RemoteHits    int64
	Misses        int64
	Puts          int64
	RemotePuts    int64
	RemoteSkipped int64
	RemoteErrors  int64
}

// Server serves the GOCACHEPROG JSON protocol over stdin/stdout backed by a
// local content-addressed disk store and an optional RemoteBackend.
const (
	// DefaultMaxDiskBytes caps the persistent local backing store at 4 GiB.
	DefaultMaxDiskBytes int64 = 4 << 30
	// DefaultMaxDiskAge expires local cache files untouched for more than 7 days.
	DefaultMaxDiskAge = 7 * 24 * time.Hour
	// minLiveGrace provides secondary mtime protection for files touched very recently.
	minLiveGrace = 10 * time.Minute

	// Managed subdirectory names inside diskDir. Pruning operates exclusively inside
	// metaSubdir and objSubdir and only on recognized cache file names so unrelated
	// files placed in diskDir are never touched.
	metaSubdir   = "meta"
	objSubdir    = "obj"
	leasesSubdir = "leases"
	tmpSubdir    = "tmp"
	storeLockNam = ".store.lock"
)

type Server struct {
	diskDir      string
	remote       RemoteBackend
	readOnly     bool // disables remote writes; local disk writes always work for Go builds
	verbose      bool
	logw         io.Writer
	workers      int
	maxDiskBytes int64
	maxDiskAge   time.Duration
	liveGrace    time.Duration
	now          func() time.Time

	// Test hook invoked inside pruneDiskStoreLocked right after scanning and
	// before unlinking (still under the exclusive store lock). Nil in production.
	pruneBeforeUnlinkHook func()

	mu        sync.Mutex
	stats     Stats
	inUseObj  map[string]bool
	leaseID   string
	leaseFile *os.File
	leasePath string
}

// ServerConfig configures a GOCACHEPROG Server.
type ServerConfig struct {
	DiskDir      string
	Remote       RemoteBackend
	ReadOnly     bool
	Verbose      bool
	Log          io.Writer
	Workers      int
	MaxDiskBytes int64
	MaxDiskAge   time.Duration
	// LiveGrace overrides the minimum age before an unpinned object may be evicted
	// (defaults to 10 minutes; tests may set a tiny value). Active cross-process
	// leases protect returned DiskPaths for the entire process lifetime regardless
	// of LiveGrace.
	LiveGrace time.Duration
	Now       func() time.Time
}

// NewServer constructs a GOCACHEPROG Server.
func NewServer(cfg ServerConfig) (*Server, error) {
	if cfg.DiskDir == "" {
		return nil, errors.New("gocacheprog: disk cache directory is required")
	}
	absDir, err := filepath.Abs(cfg.DiskDir)
	if err != nil {
		return nil, fmt.Errorf("gocacheprog: resolve disk dir: %w", err)
	}
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		return nil, fmt.Errorf("gocacheprog: create disk dir: %w", err)
	}
	logw := cfg.Log
	if logw == nil {
		logw = io.Discard
	}
	workers := cfg.Workers
	if workers <= 0 {
		workers = max(runtime.NumCPU(), 4)
	}
	maxDiskBytes := cfg.MaxDiskBytes
	if maxDiskBytes <= 0 {
		maxDiskBytes = DefaultMaxDiskBytes
	}
	maxDiskAge := cfg.MaxDiskAge
	if maxDiskAge <= 0 {
		maxDiskAge = DefaultMaxDiskAge
	}
	liveGrace := cfg.LiveGrace
	if liveGrace <= 0 {
		liveGrace = minLiveGrace
	}
	nowFn := cfg.Now
	if nowFn == nil {
		nowFn = func() time.Time { return time.Now().UTC() }
	}
	var randBuf [8]byte
	_, _ = rand.Read(randBuf[:])
	leaseID := fmt.Sprintf("%d-%s", os.Getpid(), hex.EncodeToString(randBuf[:]))
	return &Server{
		diskDir:      absDir,
		remote:       cfg.Remote,
		readOnly:     cfg.ReadOnly,
		verbose:      cfg.Verbose,
		logw:         logw,
		workers:      workers,
		maxDiskBytes: maxDiskBytes,
		maxDiskAge:   maxDiskAge,
		liveGrace:    liveGrace,
		now:          nowFn,
		inUseObj:     make(map[string]bool),
		leaseID:      leaseID,
	}, nil
}

// Stats returns a snapshot of cache counters.
func (s *Server) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats
}

// ProbeDiskWritable verifies that s.diskDir (and its object shard root) can be
// written by creating, closing, and removing a probe temporary file.
func (s *Server) ProbeDiskWritable() error {
	objRoot := filepath.Join(s.diskDir, objSubdir)
	if err := os.MkdirAll(objRoot, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(objRoot, ".probe-*")
	if err != nil {
		return err
	}
	name := f.Name()
	closeErr := f.Close()
	removeErr := os.Remove(name)
	if closeErr != nil {
		return closeErr
	}
	return removeErr
}

// withStoreLock acquires a shared or exclusive cross-process lock on the disk store.
// Publishers/readers take a shared lock so concurrent GET/PUT operations run in
// parallel; pruneDiskStore takes an exclusive lock so eviction never races with
// publishing, mtime refresh, or lease recording in another helper process.
func (s *Server) withStoreLock(exclusive bool, fn func() error) error {
	lockPath := filepath.Join(s.diskDir, storeLockNam)
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if exclusive {
		if err := flockExclusive(f); err != nil {
			return err
		}
	} else {
		if err := flockShared(f); err != nil {
			return err
		}
	}
	defer func() { _ = flockUnlock(f) }()
	return fn()
}

// ensureLeaseLocked opens and exclusively locks this server's process-lifetime
// lease file under leases/. Holding the OS lock ensures that if this process
// crashes or is killed without CmdClose, the kernel immediately releases the lock
// and other pruners treat the stale lease file as dead and clean it up.
// Caller must hold s.mu.
func (s *Server) ensureLeaseLocked() error {
	if s.leaseFile != nil {
		return nil
	}
	leasesDir := filepath.Join(s.diskDir, leasesSubdir)
	if err := os.MkdirAll(leasesDir, 0o755); err != nil {
		return err
	}
	leasePath := filepath.Join(leasesDir, s.leaseID+".lease")
	f, err := os.OpenFile(leasePath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if err := lockLeaseFile(f); err != nil {
		_ = f.Close()
		_ = os.Remove(leasePath)
		return err
	}
	s.leaseFile = f
	s.leasePath = leasePath
	return nil
}

// recordLeaseLocked appends newly pinned relative paths to the open lease file.
// Caller must hold s.mu.
func (s *Server) recordLeaseLocked(paths ...string) {
	if len(paths) == 0 {
		return
	}
	if err := s.ensureLeaseLocked(); err != nil {
		return
	}
	var buf strings.Builder
	for _, p := range paths {
		rel, err := filepath.Rel(s.diskDir, p)
		if err != nil {
			continue
		}
		buf.WriteString(filepath.ToSlash(rel))
		buf.WriteByte('\n')
	}
	if buf.Len() == 0 {
		return
	}
	_, _ = s.leaseFile.WriteString(buf.String())
}

// releaseLease releases this server's cross-process lease file and removes it.
// Must only be called once the client has closed (CmdClose or stdin EOF) and all
// workers have completed.
func (s *Server) releaseLease() {
	s.mu.Lock()
	f := s.leaseFile
	p := s.leasePath
	s.leaseFile = nil
	s.leasePath = ""
	s.mu.Unlock()
	if f != nil {
		_ = unlockLeaseFile(f)
		_ = f.Close()
	}
	if p != "" {
		_ = os.Remove(p)
	}
}

// collectActiveLeasePins scans leases/*.lease while holding the exclusive store
// lock. For each lease file belonging to another process, it probes the OS file
// lock with a non-blocking exclusive lock:
//   - If the lock cannot be acquired, the owning process is still live: every
//     path recorded in that lease file is added to the returned pinned set.
//   - If the lock succeeds, the owning process is dead (or crashed): the stale
//     lease file is unlinked and ignored.
//
// Returns (pins, unreadableActive). If unreadableActive is true, a live peer
// lease could not be read reliably so callers must conservatively skip eviction.
func (s *Server) collectActiveLeasePins(skipOwnLease bool) (map[string]bool, bool) {
	pins := make(map[string]bool)
	leasesDir := filepath.Join(s.diskDir, leasesSubdir)
	entries, err := os.ReadDir(leasesDir)
	if err != nil {
		return pins, false
	}
	s.mu.Lock()
	ownPath := s.leasePath
	s.mu.Unlock()

	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".lease") {
			continue
		}
		lp := filepath.Join(leasesDir, ent.Name())
		if skipOwnLease && ownPath != "" && lp == ownPath {
			continue
		}
		f, err := os.OpenFile(lp, os.O_RDWR, 0)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			// Could not open an existing peer lease file; abort eviction conservatively.
			return pins, true
		}
		locked, tryErr := tryLockLeaseFile(f)
		if tryErr == nil && locked {
			// No live process holds this lease; remove stale lease file.
			_ = unlockLeaseFile(f)
			_ = f.Close()
			_ = os.Remove(lp)
			continue
		}
		// Lease is held by a live helper process (or lock probe failed conservatively):
		// read pinned relative paths from it.
		raw, rerr := io.ReadAll(f)
		_ = f.Close()
		if rerr != nil {
			// Live peer lease exists but could not be read; abort eviction conservatively.
			return pins, true
		}
		for line := range strings.SplitSeq(string(raw), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			pins[filepath.Join(s.diskDir, filepath.FromSlash(line))] = true
		}
	}
	return pins, false
}

// putPayload holds a validated PUT body staged on disk (or zero-length inline).
type putPayload struct {
	tempPath string // non-empty when BodySize > 0
	size     int64
	sha256   [32]byte
}

func (p *putPayload) cleanup() {
	if p != nil && p.tempPath != "" {
		_ = os.Remove(p.tempPath)
		p.tempPath = ""
	}
}

// decodeRequestFromBufio reads the next JSON request object line from br without
// consuming subsequent body bytes into an opaque json.Decoder buffer.
func decodeRequestFromBufio(br *bufio.Reader, req *Request) error {
	for {
		line, err := br.ReadBytes('\n')
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) > 0 {
			if uerr := json.Unmarshal(trimmed, req); uerr != nil {
				return uerr
			}
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// jsonBase64StringReader streams the raw base64 characters inside a JSON string
// literal from br (starting after optional leading whitespace and the opening '"')
// until the closing '"' without buffering the whole string in memory.
type jsonBase64StringReader struct {
	br   *bufio.Reader
	buf  []byte
	off  int
	done bool
	err  error
}

func newJSONBase64StringReader(br *bufio.Reader) (*jsonBase64StringReader, error) {
	// Skip JSON whitespace before opening quote.
	for {
		b, err := br.ReadByte()
		if err != nil {
			return nil, err
		}
		if b == ' ' || b == '\t' || b == '\r' || b == '\n' {
			continue
		}
		if b != '"' {
			return nil, fmt.Errorf("expected '\"' starting base64 body, got %q", b)
		}
		return &jsonBase64StringReader{br: br}, nil
	}
}

func (jr *jsonBase64StringReader) Read(p []byte) (int, error) {
	if jr.off < len(jr.buf) {
		n := copy(p, jr.buf[jr.off:])
		jr.off += n
		if jr.off == len(jr.buf) {
			jr.buf = jr.buf[:0]
			jr.off = 0
		}
		return n, nil
	}
	if jr.done {
		return 0, io.EOF
	}
	if jr.err != nil {
		return 0, jr.err
	}
	for {
		chunk, err := jr.br.ReadSlice('"')
		if len(chunk) > 0 {
			hasClose := err == nil && chunk[len(chunk)-1] == '"'
			data := chunk
			if hasClose {
				data = chunk[:len(chunk)-1]
			}
			// Handle rare JSON string escape sequences (e.g. \u002b, \/) cleanly,
			// including escapes that might span the end of a ReadSlice chunk.
			if bytes.IndexByte(data, '\\') >= 0 {
				var merged []byte
				merged = append(merged, data...)
				if hasClose && hasTrailingOddBackslashes(merged) {
					// The quote that ended ReadSlice was escaped (\"); keep scanning.
					merged = append(merged, '"')
					hasClose = false
				}
				if !hasClose && hasTrailingIncompleteEscape(merged) {
					extra, xerr := jr.readEscapeTail(merged)
					if xerr != nil {
						jr.err = xerr
						return 0, xerr
					}
					merged = append(merged, extra...)
				}
				decoded, uerr := unescapeJSONBase64Chunk(merged)
				if uerr != nil {
					jr.err = uerr
					return 0, uerr
				}
				data = decoded
			}
			if hasClose {
				jr.done = true
			}
			if len(data) == 0 {
				if hasClose {
					return 0, io.EOF
				}
				continue
			}
			n := copy(p, data)
			if n < len(data) {
				jr.buf = append(jr.buf[:0], data[n:]...)
				jr.off = 0
			}
			if hasClose && n == len(data) {
				return n, io.EOF
			}
			return n, nil
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			jr.err = err
			return 0, err
		}
	}
}

// drainRemaining consumes through the unescaped closing quote if the decoder
// stopped early (e.g. on malformed base64 or size overflow), keeping stdin
// synchronized for subsequent requests.
func (jr *jsonBase64StringReader) drainRemaining() error {
	_, err := jr.drainCheckingNonWhitespace()
	return err
}

func (jr *jsonBase64StringReader) drainCheckingNonWhitespace() (bool, error) {
	hasNonWS := false
	for jr.off < len(jr.buf) {
		b := jr.buf[jr.off]
		jr.off++
		if b != '\r' && b != '\n' {
			hasNonWS = true
		}
	}
	jr.buf = jr.buf[:0]
	jr.off = 0
	if jr.done {
		return hasNonWS, nil
	}
	escaped := false
	for {
		b, err := jr.br.ReadByte()
		if err != nil {
			jr.err = err
			return hasNonWS, err
		}
		if escaped {
			escaped = false
			hasNonWS = true
			continue
		}
		if b == '\\' {
			escaped = true
			continue
		}
		if b == '"' {
			jr.done = true
			return hasNonWS, nil
		}
		if b != '\r' && b != '\n' {
			hasNonWS = true
		}
	}
}

func hasTrailingOddBackslashes(b []byte) bool {
	count := 0
	for i := len(b) - 1; i >= 0 && b[i] == '\\'; i-- {
		count++
	}
	return count%2 == 1
}

func hasTrailingIncompleteEscape(b []byte) bool {
	if hasTrailingOddBackslashes(b) {
		return true
	}
	// Check for trailing \uXXXX split mid-hex (between 1 and 5 chars from end).
	for k := 1; k <= 5 && k < len(b); k++ {
		pos := len(b) - 1 - k
		if b[pos] == '\\' && b[pos+1] == 'u' && !hasTrailingOddBackslashes(b[:pos]) {
			return true
		}
	}
	return false
}

func (jr *jsonBase64StringReader) readEscapeTail(prefix []byte) ([]byte, error) {
	var extra []byte
	for {
		b, err := jr.br.ReadByte()
		if err != nil {
			return nil, err
		}
		extra = append(extra, b)
		combined := append(append([]byte(nil), prefix...), extra...)
		if !hasTrailingIncompleteEscape(combined) {
			return extra, nil
		}
	}
}

func unescapeJSONBase64Chunk(raw []byte) ([]byte, error) {
	var s string
	quoted := make([]byte, 0, len(raw)+2)
	quoted = append(quoted, '"')
	quoted = append(quoted, raw...)
	quoted = append(quoted, '"')
	if err := json.Unmarshal(quoted, &s); err != nil {
		return nil, err
	}
	return []byte(s), nil
}

// streamPutBodyToTemp decodes the JSON base64 string literal directly into a
// temporary file in diskDir/tmp while hashing with SHA-256 and validating the
// decoded byte count against expectedSize.
// Returns (payload, protocolErr, reqErr):
//   - protocolErr != nil means stdin framing is broken / EOF and Serve must abort.
//   - reqErr != nil means framing stayed synchronized and Serve should send an error
//     Response for req.ID and continue processing subsequent requests.
func (s *Server) streamPutBodyToTemp(br *bufio.Reader, expectedSize int64) (payload *putPayload, protocolErr error, reqErr error) {
	jr, err := newJSONBase64StringReader(br)
	if err != nil {
		return nil, err, nil
	}

	tmpDir := filepath.Join(s.diskDir, tmpSubdir)
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		_ = jr.drainRemaining()
		return nil, nil, fmt.Errorf("create temp dir: %w", err)
	}
	tmp, err := os.CreateTemp(tmpDir, ".put-*")
	if err != nil {
		_ = jr.drainRemaining()
		return nil, nil, fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()

	dec := base64.NewDecoder(base64.StdEncoding, jr)
	hasher := sha256.New()
	bw := bufio.NewWriterSize(tmp, 64*1024)
	mw := io.MultiWriter(bw, hasher)

	// Cap copied bytes at expectedSize+1 so a misbehaving client claiming a small
	// BodySize with a huge body fails fast without filling the disk.
	written, copyErr := io.Copy(mw, io.LimitReader(dec, expectedSize+1))
	flushErr := bw.Flush()
	closeErr := tmp.Close()

	if copyErr != nil {
		if jr.err != nil {
			return nil, jr.err, nil
		}
		if drainErr := jr.drainRemaining(); drainErr != nil {
			return nil, drainErr, nil
		}
		return nil, nil, fmt.Errorf("decode base64 body: %w", copyErr)
	}
	if jr.err != nil {
		return nil, jr.err, nil
	}
	if written != expectedSize {
		if drainErr := jr.drainRemaining(); drainErr != nil {
			return nil, drainErr, nil
		}
		return nil, nil, fmt.Errorf("body size mismatch: got %d, want %d", written, expectedSize)
	}
	// Ensure no trailing invalid base64 or extra bytes remain before closing quote.
	var extra [1]byte
	if n, tailErr := dec.Read(extra[:]); n > 0 {
		if drainErr := jr.drainRemaining(); drainErr != nil {
			return nil, drainErr, nil
		}
		return nil, nil, fmt.Errorf("body size mismatch: got >%d, want %d", expectedSize, expectedSize)
	} else if tailErr != nil && !errors.Is(tailErr, io.EOF) {
		if jr.err != nil {
			return nil, jr.err, nil
		}
		if drainErr := jr.drainRemaining(); drainErr != nil {
			return nil, drainErr, nil
		}
		return nil, nil, fmt.Errorf("decode base64 body: %w", tailErr)
	}
	if !jr.done || jr.off < len(jr.buf) {
		// Reject any non-whitespace garbage after base64 padding inside the JSON string.
		if trailing, drainErr := jr.drainCheckingNonWhitespace(); drainErr != nil {
			return nil, drainErr, nil
		} else if trailing {
			return nil, nil, errors.New("decode base64 body: unexpected trailing data after padding")
		}
	}
	if flushErr != nil {
		return nil, nil, fmt.Errorf("flush put temp file: %w", flushErr)
	}
	if closeErr != nil {
		return nil, nil, fmt.Errorf("close put temp file: %w", closeErr)
	}

	var sum [32]byte
	hasher.Sum(sum[:0])
	cleanup = false
	return &putPayload{
		tempPath: tmpName,
		size:     written,
		sha256:   sum,
	}, nil, nil
}

// Serve runs the GOCACHEPROG JSON-lines protocol until CmdClose or stdin EOF.
func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	defer s.releaseLease()

	var writeMu sync.Mutex
	bw := bufio.NewWriter(w)
	send := func(resp Response) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		line, err := json.Marshal(resp)
		if err != nil {
			return err
		}
		if _, err := bw.Write(line); err != nil {
			return err
		}
		if err := bw.WriteByte('\n'); err != nil {
			return err
		}
		return bw.Flush()
	}

	// Advertise supported commands immediately on startup (ID == 0).
	// Always advertise "put" so the go command stores build/test outputs on local
	// disk even when the remote backend is read-only (PR mode) or absent.
	if err := send(Response{
		ID:            0,
		KnownCommands: []Cmd{CmdGet, CmdPut, CmdClose},
	}); err != nil {
		return err
	}

	br := bufio.NewReaderSize(r, 64*1024)
	sem := make(chan struct{}, s.workers)
	var wg sync.WaitGroup
	var writeErrMu sync.Mutex
	var firstWriteErr error
	recordWriteErr := func(err error) {
		if err == nil {
			return
		}
		writeErrMu.Lock()
		if firstWriteErr == nil {
			firstWriteErr = err
		}
		writeErrMu.Unlock()
	}

	for {
		var req Request
		if err := decodeRequestFromBufio(br, &req); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			wg.Wait()
			return fmt.Errorf("gocacheprog: decode request: %w", err)
		}

		// For "put" with BodySize > 0, the body follows immediately on stdin as a
		// JSON base64 string literal. Stream it directly to a temp file on disk
		// so large artifacts never accumulate in RAM.
		var payload *putPayload
		if req.Command == CmdPut && req.BodySize < 0 {
			recordWriteErr(send(Response{ID: req.ID, Err: fmt.Sprintf("invalid negative BodySize: %d", req.BodySize)}))
			continue
		}
		if req.Command == CmdPut && req.BodySize > 0 {
			p, protoErr, reqErr := s.streamPutBodyToTemp(br, req.BodySize)
			if protoErr != nil {
				wg.Wait()
				return fmt.Errorf("gocacheprog: decode put body for request %d: %w", req.ID, protoErr)
			}
			if reqErr != nil {
				recordWriteErr(send(Response{ID: req.ID, Err: reqErr.Error()}))
				continue
			}
			payload = p
		}

		if req.Command == CmdClose {
			// Wait for all in-flight get/put operations (including remote uploads) to
			// finish before replying to close so Go does not terminate us early.
			wg.Wait()
			s.pruneDiskStore()
			s.releaseLease()
			if s.verbose {
				st := s.Stats()
				_, _ = fmt.Fprintf(s.logw, "gocacheprog: close stats local_hits=%d remote_hits=%d misses=%d puts=%d remote_puts=%d remote_skipped=%d remote_errors=%d\n",
					st.LocalHits, st.RemoteHits, st.Misses, st.Puts, st.RemotePuts, st.RemoteSkipped, st.RemoteErrors)
			}
			if err := send(Response{ID: req.ID}); err != nil {
				return err
			}
			return nil
		}

		sem <- struct{}{}
		wg.Add(1)
		go func(rq Request, pl *putPayload) {
			defer func() {
				pl.cleanup()
				<-sem
				wg.Done()
			}()
			resp := s.handle(ctx, rq, pl)
			recordWriteErr(send(resp))
		}(req, payload)
	}

	wg.Wait()
	return firstWriteErr
}

func (s *Server) handle(ctx context.Context, req Request, payload *putPayload) Response {
	switch req.Command {
	case CmdGet:
		return s.handleGet(ctx, req)
	case CmdPut:
		return s.handlePut(ctx, req, payload)
	default:
		return Response{ID: req.ID, Err: fmt.Sprintf("unknown command %q", req.Command)}
	}
}

type localMeta struct {
	OutputID string `json:"outputID"`
	Size     int64  `json:"size"`
	TimeUnix int64  `json:"timeUnix"`
}

func (s *Server) entryPaths(actionHex string) (metaPath, objDir string) {
	prefix := actionHex
	if len(prefix) >= 2 {
		prefix = actionHex[:2]
	}
	dir := filepath.Join(s.diskDir, metaSubdir, prefix)
	return filepath.Join(dir, actionHex+".meta"), filepath.Join(s.diskDir, objSubdir)
}

func (s *Server) objectPath(outputHex string) string {
	prefix := outputHex
	if len(prefix) >= 2 {
		prefix = outputHex[:2]
	}
	// By Go GOCACHEPROG convention, DiskPath files carry no extension.
	return filepath.Join(s.diskDir, objSubdir, prefix, outputHex)
}

func (s *Server) handleGet(ctx context.Context, req Request) Response {
	if len(req.ActionID) == 0 {
		return Response{ID: req.ID, Miss: true}
	}
	actionHex := hex.EncodeToString(req.ActionID)
	metaPath, _ := s.entryPaths(actionHex)

	// Hold the shared store lock while checking local metadata/object existence
	// and pinning the entry into our live lease so a concurrent pruner in another
	// process cannot unlink objPath between os.Stat and pinning.
	var localHitResp *Response
	_ = s.withStoreLock(false, func() error {
		metaBytes, err := os.ReadFile(metaPath)
		if err != nil {
			return nil
		}
		var meta localMeta
		if json.Unmarshal(metaBytes, &meta) != nil || meta.OutputID == "" {
			return nil
		}
		outputID, err := hex.DecodeString(meta.OutputID)
		if err != nil {
			return nil
		}
		objPath := s.objectPath(meta.OutputID)
		info, err := os.Stat(objPath)
		if err != nil || info.Size() != meta.Size {
			return nil
		}
		s.markInUse(objPath, metaPath)
		t := time.Unix(meta.TimeUnix, 0).UTC()
		s.mu.Lock()
		s.stats.LocalHits++
		s.mu.Unlock()
		localHitResp = &Response{
			ID:       req.ID,
			OutputID: outputID,
			Size:     meta.Size,
			Time:     &t,
			DiskPath: objPath,
		}
		return nil
	})
	if localHitResp != nil {
		return *localHitResp
	}

	// Try optional remote cache on local miss. Never fail the build on remote error.
	if s.remote != nil {
		outputID, data, modTime, hit, err := s.remote.Get(ctx, actionHex)
		if err != nil {
			s.recordRemoteError("get", actionHex, err)
		} else if hit && len(outputID) > 0 {
			// Verify integrity of downloaded artifact against its claimed OutputID
			// (OutputID in Go's cache is SHA-256 of the body; see cmd/go/internal/cache/prog.go).
			sum := sha256.Sum256(data)
			if !bytes.Equal(sum[:], outputID) {
				s.recordRemoteError("verify", actionHex, fmt.Errorf("sha256 mismatch for remote object (expected %x, got %x)", outputID, sum[:]))
			} else {
				diskPath, storeErr := s.writeLocal(actionHex, outputID, data, modTime)
				if storeErr == nil {
					if modTime.IsZero() {
						modTime = time.Now().UTC()
					}
					s.mu.Lock()
					s.stats.RemoteHits++
					s.mu.Unlock()
					return Response{
						ID:       req.ID,
						OutputID: outputID,
						Size:     int64(len(data)),
						Time:     &modTime,
						DiskPath: diskPath,
					}
				}
				s.recordRemoteError("write-local", actionHex, storeErr)
			}
		}
	}

	s.mu.Lock()
	s.stats.Misses++
	s.mu.Unlock()
	return Response{ID: req.ID, Miss: true}
}

func (s *Server) handlePut(ctx context.Context, req Request, payload *putPayload) Response {
	if len(req.ActionID) == 0 || len(req.OutputID) == 0 {
		return Response{ID: req.ID, Err: "missing ActionID or OutputID"}
	}
	actionHex := hex.EncodeToString(req.ActionID)
	now := time.Now().UTC()
	var (
		diskPath string
		bodySize int64
		err      error
	)
	if payload != nil && payload.tempPath != "" {
		bodySize = payload.size
		diskPath, err = s.writeLocalFromTemp(actionHex, req.OutputID, payload.tempPath, payload.size, payload.sha256, now)
		if err == nil {
			// File was moved or matched; prevent defer cleanup of renamed path.
			payload.tempPath = ""
		}
	} else {
		bodySize = 0
		diskPath, err = s.writeLocal(actionHex, req.OutputID, nil, now)
	}
	if err != nil {
		return Response{ID: req.ID, Err: fmt.Sprintf("write local cache: %v", err)}
	}
	s.mu.Lock()
	s.stats.Puts++
	s.mu.Unlock()

	// Upload to remote ONLY when a remote backend is configured and neither server nor
	// backend is read-only. Failures are logged and ignored so builds never fail.
	if s.remote != nil && !s.readOnly && !s.remote.ReadOnly() {
		err := s.remote.Put(ctx, actionHex, req.OutputID, diskPath, bodySize)
		if errors.Is(err, ErrRemoteSkipped) {
			s.mu.Lock()
			s.stats.RemoteSkipped++
			s.mu.Unlock()
			if s.verbose {
				_, _ = fmt.Fprintf(s.logw, "gocacheprog: remote put %s skipped (%v)\n", actionHex[:min(len(actionHex), 12)], err)
			}
		} else if err != nil {
			s.recordRemoteError("put", actionHex, err)
		} else {
			s.mu.Lock()
			s.stats.RemotePuts++
			s.mu.Unlock()
		}
	}

	return Response{
		ID:       req.ID,
		DiskPath: diskPath,
	}
}

func (s *Server) markInUse(objPath, metaPath string) {
	var newlyPinned []string
	s.mu.Lock()
	if !s.inUseObj[objPath] {
		s.inUseObj[objPath] = true
		newlyPinned = append(newlyPinned, objPath)
	}
	if !s.inUseObj[metaPath] {
		s.inUseObj[metaPath] = true
		newlyPinned = append(newlyPinned, metaPath)
	}
	s.recordLeaseLocked(newlyPinned...)
	s.mu.Unlock()
	now := s.now()
	_ = os.Chtimes(objPath, now, now)
	_ = os.Chtimes(metaPath, now, now)
}

func fileMatchesContent(path string, expected []byte) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() != int64(len(expected)) {
		return false
	}
	existing, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return bytes.Equal(existing, expected)
}

func fileMatchesSizeAndSHA256(path string, expectedSize int64, expectedSum [32]byte) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() != expectedSize {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false
	}
	var got [32]byte
	h.Sum(got[:0])
	return got == expectedSum
}

func (s *Server) writeLocal(actionHex string, outputID, body []byte, modTime time.Time) (string, error) {
	var objPath string
	err := s.withStoreLock(false, func() error {
		outputHex := hex.EncodeToString(outputID)
		objPath = s.objectPath(outputHex)
		if err := os.MkdirAll(filepath.Dir(objPath), 0o755); err != nil {
			return err
		}
		if !fileMatchesContent(objPath, body) {
			if err := writeAtomic(objPath, body); err != nil {
				return err
			}
		}
		metaPath, _ := s.entryPaths(actionHex)
		if err := os.MkdirAll(filepath.Dir(metaPath), 0o755); err != nil {
			return err
		}
		if modTime.IsZero() {
			modTime = s.now()
		}
		metaBytes, err := json.Marshal(localMeta{
			OutputID: outputHex,
			Size:     int64(len(body)),
			TimeUnix: modTime.Unix(),
		})
		if err != nil {
			return err
		}
		if err := writeAtomic(metaPath, metaBytes); err != nil {
			return err
		}
		s.markInUse(objPath, metaPath)
		return nil
	})
	if err != nil {
		return "", err
	}
	return objPath, nil
}

func (s *Server) writeLocalFromTemp(actionHex string, outputID []byte, tempPath string, size int64, sum [32]byte, modTime time.Time) (string, error) {
	var objPath string
	err := s.withStoreLock(false, func() error {
		outputHex := hex.EncodeToString(outputID)
		objPath = s.objectPath(outputHex)
		if err := os.MkdirAll(filepath.Dir(objPath), 0o755); err != nil {
			return err
		}
		if fileMatchesSizeAndSHA256(objPath, size, sum) {
			_ = os.Remove(tempPath)
		} else {
			if err := renameAtomicFile(tempPath, objPath, size, sum); err != nil {
				return err
			}
		}
		metaPath, _ := s.entryPaths(actionHex)
		if err := os.MkdirAll(filepath.Dir(metaPath), 0o755); err != nil {
			return err
		}
		if modTime.IsZero() {
			modTime = s.now()
		}
		metaBytes, err := json.Marshal(localMeta{
			OutputID: outputHex,
			Size:     size,
			TimeUnix: modTime.Unix(),
		})
		if err != nil {
			return err
		}
		if err := writeAtomic(metaPath, metaBytes); err != nil {
			return err
		}
		s.markInUse(objPath, metaPath)
		return nil
	})
	if err != nil {
		return "", err
	}
	return objPath, nil
}

func isLowerHex(s string) bool {
	if len(s) < 2 {
		return false
	}
	for i := range len(s) {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// isManagedMetaPath reports whether p is a cache-owned metadata file under
// <diskDir>/meta/<2-hex>/<hex>.meta.
func (s *Server) isManagedMetaPath(p string) bool {
	metaRoot := filepath.Join(s.diskDir, metaSubdir)
	rel, err := filepath.Rel(metaRoot, p)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) != 2 {
		return false
	}
	shard, base := parts[0], parts[1]
	if len(shard) != 2 || !isLowerHex(shard) {
		return false
	}
	stem, ok := strings.CutSuffix(base, ".meta")
	if !ok || len(stem) < 2 || len(stem)%2 != 0 || !isLowerHex(stem) || !strings.HasPrefix(stem, shard) {
		return false
	}
	return true
}

// isManagedObjPath reports whether p is a cache-owned object file under
// <diskDir>/obj/<2-hex>/<hex>.
func (s *Server) isManagedObjPath(p string) bool {
	objRoot := filepath.Join(s.diskDir, objSubdir)
	rel, err := filepath.Rel(objRoot, p)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) != 2 {
		return false
	}
	shard, base := parts[0], parts[1]
	if len(shard) != 2 || !isLowerHex(shard) {
		return false
	}
	if len(base) < 2 || len(base)%2 != 0 || !isLowerHex(base) || !strings.HasPrefix(base, shard) {
		return false
	}
	return true
}

// pruneDiskStore enforces maxDiskAge and maxDiskBytes across cache-owned
// metadata files (meta/) and content-addressed object files (obj/) under an
// exclusive cross-process store lock. It never touches unrelated files in
// s.diskDir, never removes any entry pinned by this server or by any active
// peer helper process via leases/, and never races with concurrent publish or
// mtime refreshes.
func (s *Server) pruneDiskStore() {
	if s.maxDiskBytes <= 0 && s.maxDiskAge <= 0 {
		return
	}
	_ = s.withStoreLock(true, func() error {
		s.pruneDiskStoreLocked()
		return nil
	})
}

func (s *Server) pruneDiskStoreLocked() {
	// Collect pins from this process plus every live peer process holding a lease.
	pinned, unreadablePeerLease := s.collectActiveLeasePins(true)
	if unreadablePeerLease {
		// Conservative bail-out: a live peer lease could not be read, so skip
		// eviction rather than risking unlinking another process's live DiskPath.
		return
	}
	s.mu.Lock()
	for p := range s.inUseObj {
		pinned[p] = true
	}
	s.mu.Unlock()

	now := s.now()
	ageCutoff := now.Add(-s.maxDiskAge)
	liveCutoff := now.Add(-s.liveGrace)

	type diskEntry struct {
		path     string
		size     int64
		modTime  time.Time
		isMeta   bool
		outputID string
	}
	var metas []diskEntry
	var objs []diskEntry
	var totalBytes int64

	metaRoot := filepath.Join(s.diskDir, metaSubdir)
	_ = filepath.WalkDir(metaRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !s.isManagedMetaPath(p) {
			return nil
		}
		info, statErr := d.Info()
		if statErr != nil || !info.Mode().IsRegular() {
			return nil
		}
		e := diskEntry{
			path:    p,
			size:    info.Size(),
			modTime: info.ModTime().UTC(),
			isMeta:  true,
		}
		totalBytes += e.size
		if raw, rerr := os.ReadFile(p); rerr == nil {
			var lm localMeta
			if json.Unmarshal(raw, &lm) == nil && lm.OutputID != "" && isLowerHex(lm.OutputID) {
				e.outputID = lm.OutputID
			}
		}
		metas = append(metas, e)
		return nil
	})

	objRoot := filepath.Join(s.diskDir, objSubdir)
	_ = filepath.WalkDir(objRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !s.isManagedObjPath(p) {
			return nil
		}
		info, statErr := d.Info()
		if statErr != nil || !info.Mode().IsRegular() {
			return nil
		}
		e := diskEntry{
			path:    p,
			size:    info.Size(),
			modTime: info.ModTime().UTC(),
			isMeta:  false,
		}
		totalBytes += e.size
		objs = append(objs, e)
		return nil
	})

	// Track objects still referenced by surviving metadata files so we never leave
	// live metadata dangling when evicting objects.
	refCount := make(map[string]int, len(metas))
	for _, m := range metas {
		if m.outputID != "" {
			refCount[s.objectPath(m.outputID)]++
		}
	}

	if s.pruneBeforeUnlinkHook != nil {
		s.pruneBeforeUnlinkHook()
	}

	canEvict := func(p string, modTime time.Time) bool {
		if pinned[p] {
			return false
		}
		if modTime.After(liveCutoff) {
			return false
		}
		// Re-stat under the exclusive store lock immediately before eviction to
		// verify current on-disk state matches the walk snapshot.
		info, err := os.Stat(p)
		if err != nil || !info.Mode().IsRegular() {
			return false
		}
		if info.ModTime().UTC().After(liveCutoff) {
			return false
		}
		return true
	}

	removed := make(map[string]bool)

	// Pass 1: evict expired metadata first, then expired unreferenced objects.
	if s.maxDiskAge > 0 {
		for _, m := range metas {
			if m.modTime.Before(ageCutoff) && canEvict(m.path, m.modTime) {
				if os.Remove(m.path) == nil {
					removed[m.path] = true
					totalBytes -= m.size
					if m.outputID != "" {
						refCount[s.objectPath(m.outputID)]--
					}
				}
			}
		}
		for _, o := range objs {
			if o.modTime.Before(ageCutoff) && refCount[o.path] <= 0 && canEvict(o.path, o.modTime) {
				if os.Remove(o.path) == nil {
					removed[o.path] = true
					totalBytes -= o.size
				}
			}
		}
	}

	// Pass 2: if still over maxDiskBytes, evict oldest entries (metadata + their
	// unreferenced objects) until totalBytes <= maxDiskBytes.
	if s.maxDiskBytes > 0 && totalBytes > s.maxDiskBytes {
		sort.Slice(metas, func(i, j int) bool { return metas[i].modTime.Before(metas[j].modTime) })
		sort.Slice(objs, func(i, j int) bool { return objs[i].modTime.Before(objs[j].modTime) })

		// First remove oldest unreferenced orphan objects.
		for _, o := range objs {
			if totalBytes <= s.maxDiskBytes {
				break
			}
			if removed[o.path] || refCount[o.path] > 0 || !canEvict(o.path, o.modTime) {
				continue
			}
			if os.Remove(o.path) == nil {
				removed[o.path] = true
				totalBytes -= o.size
			}
		}
		// Build quick size/mtime lookup for objects.
		objByPath := make(map[string]diskEntry, len(objs))
		for _, o := range objs {
			objByPath[o.path] = o
		}
		// Evict oldest metadata entries and, once refCount drops to 0, their object.
		for _, m := range metas {
			if totalBytes <= s.maxDiskBytes {
				break
			}
			if removed[m.path] || !canEvict(m.path, m.modTime) {
				continue
			}
			if os.Remove(m.path) == nil {
				removed[m.path] = true
				totalBytes -= m.size
				if m.outputID != "" {
					op := s.objectPath(m.outputID)
					refCount[op]--
					if o, ok := objByPath[op]; ok && !removed[op] && refCount[op] <= 0 && canEvict(op, o.modTime) {
						if os.Remove(op) == nil {
							removed[op] = true
							totalBytes -= o.size
						}
					}
				}
			}
		}
	}
}

func (s *Server) recordRemoteError(op, actionHex string, err error) {
	s.mu.Lock()
	s.stats.RemoteErrors++
	count := s.stats.RemoteErrors
	s.mu.Unlock()
	// Avoid flooding stderr if a bucket is unreachable; log first 5 errors or all in verbose mode.
	if s.verbose || count <= 5 {
		_, _ = fmt.Fprintf(s.logw, "gocacheprog: remote %s %s failed (falling back to local disk): %v\n", op, actionHex[:min(len(actionHex), 12)], err)
	}
}

func renameAtomicFile(srcTemp, dest string, expectedSize int64, expectedSum [32]byte) error {
	if err := os.Rename(srcTemp, dest); err != nil {
		// On Windows, Rename over an existing destination can fail; verify whether
		// dest already holds identical content, or remove and retry once.
		if fileMatchesSizeAndSHA256(dest, expectedSize, expectedSum) {
			_ = os.Remove(srcTemp)
			return nil
		}
		if removeErr := os.Remove(dest); removeErr == nil {
			if retryErr := os.Rename(srcTemp, dest); retryErr == nil {
				return nil
			}
		}
		if fileMatchesSizeAndSHA256(dest, expectedSize, expectedSum) {
			_ = os.Remove(srcTemp)
			return nil
		}
		return err
	}
	return nil
}

func writeAtomic(dest string, data []byte) error {
	dir := filepath.Dir(dest)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if len(data) > 0 {
		if _, err := tmp.Write(data); err != nil {
			_ = tmp.Close()
			return err
		}
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, dest); err != nil {
		// On Windows, Rename over an existing destination can fail; attempt explicit
		// removal and retry once, or verify an existing destination has identical bytes
		// before treating a concurrent write race as success.
		if fileMatchesContent(dest, data) {
			return nil
		}
		if removeErr := os.Remove(dest); removeErr == nil {
			if retryErr := os.Rename(tmpName, dest); retryErr == nil {
				cleanup = false
				return nil
			}
		}
		if fileMatchesContent(dest, data) {
			return nil
		}
		return err
	}
	cleanup = false
	return nil
}
