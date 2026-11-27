// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"bytes"
	"context"
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
	LocalHits    int64
	RemoteHits   int64
	Misses       int64
	Puts         int64
	RemotePuts   int64
	RemoteErrors int64
}

// Server serves the GOCACHEPROG JSON protocol over stdin/stdout backed by a
// local content-addressed disk store and an optional RemoteBackend.
const (
	// DefaultMaxDiskBytes caps the persistent local backing store at 4 GiB.
	DefaultMaxDiskBytes int64 = 4 << 30
	// DefaultMaxDiskAge expires local cache files untouched for more than 7 days.
	DefaultMaxDiskAge = 7 * 24 * time.Hour
	// minLiveGrace protects files touched by concurrent Go processes from eviction.
	minLiveGrace = 10 * time.Minute
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

	mu       sync.Mutex
	stats    Stats
	inUseObj map[string]bool
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
	// LiveGrace overrides the minimum age before an object not pinned by this
	// process may be evicted (defaults to 10 minutes; tests may set a tiny value).
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
	objRoot := filepath.Join(s.diskDir, "obj")
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

// Serve runs the GOCACHEPROG JSON-lines protocol until CmdClose or stdin EOF.
func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
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

	jd := json.NewDecoder(bufio.NewReaderSize(r, 256*1024))
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
		if err := jd.Decode(&req); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			wg.Wait()
			return fmt.Errorf("gocacheprog: decode request: %w", err)
		}

		// For "put" with BodySize > 0, the body follows immediately on stdin as a
		// JSON base64 string literal. Read it sequentially on the reader goroutine
		// before dispatching work concurrently.
		var body []byte
		if req.Command == CmdPut && req.BodySize > 0 {
			var encoded string
			if err := jd.Decode(&encoded); err != nil {
				wg.Wait()
				return fmt.Errorf("gocacheprog: decode put body for request %d: %w", req.ID, err)
			}
			decoded, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				_ = send(Response{ID: req.ID, Err: fmt.Sprintf("decode base64 body: %v", err)})
				continue
			}
			if int64(len(decoded)) != req.BodySize {
				_ = send(Response{ID: req.ID, Err: fmt.Sprintf("body size mismatch: got %d, want %d", len(decoded), req.BodySize)})
				continue
			}
			body = decoded
		}

		if req.Command == CmdClose {
			// Wait for all in-flight get/put operations (including remote uploads) to
			// finish before replying to close so Go does not terminate us early.
			wg.Wait()
			s.pruneDiskStore()
			if s.verbose {
				st := s.Stats()
				_, _ = fmt.Fprintf(s.logw, "gocacheprog: close stats local_hits=%d remote_hits=%d misses=%d puts=%d remote_puts=%d remote_errors=%d\n",
					st.LocalHits, st.RemoteHits, st.Misses, st.Puts, st.RemotePuts, st.RemoteErrors)
			}
			if err := send(Response{ID: req.ID}); err != nil {
				return err
			}
			return nil
		}

		sem <- struct{}{}
		wg.Add(1)
		go func(rq Request, payload []byte) {
			defer func() {
				<-sem
				wg.Done()
			}()
			resp := s.handle(ctx, rq, payload)
			recordWriteErr(send(resp))
		}(req, body)
	}

	wg.Wait()
	return firstWriteErr
}

func (s *Server) handle(ctx context.Context, req Request, body []byte) Response {
	switch req.Command {
	case CmdGet:
		return s.handleGet(ctx, req)
	case CmdPut:
		return s.handlePut(ctx, req, body)
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
	dir := filepath.Join(s.diskDir, prefix)
	return filepath.Join(dir, actionHex+".meta"), filepath.Join(s.diskDir, "obj")
}

func (s *Server) objectPath(outputHex string) string {
	prefix := outputHex
	if len(prefix) >= 2 {
		prefix = outputHex[:2]
	}
	// By Go GOCACHEPROG convention, DiskPath files carry no extension.
	return filepath.Join(s.diskDir, "obj", prefix, outputHex)
}

func (s *Server) handleGet(ctx context.Context, req Request) Response {
	if len(req.ActionID) == 0 {
		return Response{ID: req.ID, Miss: true}
	}
	actionHex := hex.EncodeToString(req.ActionID)
	metaPath, _ := s.entryPaths(actionHex)

	if metaBytes, err := os.ReadFile(metaPath); err == nil {
		var meta localMeta
		if json.Unmarshal(metaBytes, &meta) == nil && meta.OutputID != "" {
			if outputID, err := hex.DecodeString(meta.OutputID); err == nil {
				objPath := s.objectPath(meta.OutputID)
				if info, err := os.Stat(objPath); err == nil && info.Size() == meta.Size {
					s.markInUse(objPath, metaPath)
					t := time.Unix(meta.TimeUnix, 0).UTC()
					s.mu.Lock()
					s.stats.LocalHits++
					s.mu.Unlock()
					return Response{
						ID:       req.ID,
						OutputID: outputID,
						Size:     meta.Size,
						Time:     &t,
						DiskPath: objPath,
					}
				}
			}
		}
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

func (s *Server) handlePut(ctx context.Context, req Request, body []byte) Response {
	if len(req.ActionID) == 0 || len(req.OutputID) == 0 {
		return Response{ID: req.ID, Err: "missing ActionID or OutputID"}
	}
	actionHex := hex.EncodeToString(req.ActionID)
	now := time.Now().UTC()
	diskPath, err := s.writeLocal(actionHex, req.OutputID, body, now)
	if err != nil {
		return Response{ID: req.ID, Err: fmt.Sprintf("write local cache: %v", err)}
	}
	s.mu.Lock()
	s.stats.Puts++
	s.mu.Unlock()

	// Upload to remote ONLY when a remote backend is configured and neither server nor
	// backend is read-only. Failures are logged and ignored so builds never fail.
	if s.remote != nil && !s.readOnly && !s.remote.ReadOnly() {
		if err := s.remote.Put(ctx, actionHex, req.OutputID, diskPath, int64(len(body))); err != nil {
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
	s.mu.Lock()
	s.inUseObj[objPath] = true
	s.inUseObj[metaPath] = true
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

func (s *Server) writeLocal(actionHex string, outputID, body []byte, modTime time.Time) (string, error) {
	outputHex := hex.EncodeToString(outputID)
	objPath := s.objectPath(outputHex)
	if err := os.MkdirAll(filepath.Dir(objPath), 0o755); err != nil {
		return "", err
	}
	if !fileMatchesContent(objPath, body) {
		if err := writeAtomic(objPath, body); err != nil {
			return "", err
		}
	}
	metaPath, _ := s.entryPaths(actionHex)
	if err := os.MkdirAll(filepath.Dir(metaPath), 0o755); err != nil {
		return "", err
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
		return "", err
	}
	if err := writeAtomic(metaPath, metaBytes); err != nil {
		return "", err
	}
	s.markInUse(objPath, metaPath)
	return objPath, nil
}

// pruneDiskStore enforces maxDiskAge and maxDiskBytes across both metadata files
// and content-addressed object files in s.diskDir, while never removing any
// DiskPath or metadata entry pinned by this server or modified within liveGrace
// (protecting concurrent Go processes sharing the backing store).
func (s *Server) pruneDiskStore() {
	if s.maxDiskBytes <= 0 && s.maxDiskAge <= 0 {
		return
	}
	s.mu.Lock()
	pinned := make(map[string]bool, len(s.inUseObj))
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

	_ = filepath.WalkDir(s.diskDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		base := d.Name()
		if strings.HasPrefix(base, ".tmp-") || strings.HasPrefix(base, ".probe-") {
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
			isMeta:  strings.HasSuffix(base, ".meta"),
		}
		totalBytes += e.size
		if e.isMeta {
			if raw, rerr := os.ReadFile(p); rerr == nil {
				var lm localMeta
				if json.Unmarshal(raw, &lm) == nil {
					e.outputID = lm.OutputID
				}
			}
			metas = append(metas, e)
		} else {
			objs = append(objs, e)
		}
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

	canEvict := func(p string, modTime time.Time) bool {
		if pinned[p] {
			return false
		}
		if modTime.After(liveCutoff) {
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
