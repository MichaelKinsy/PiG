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
	"os"
	"path/filepath"
	"runtime"
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
type Server struct {
	diskDir  string
	remote   RemoteBackend
	readOnly bool // disables remote writes; local disk writes always work for Go builds
	verbose  bool
	logw     io.Writer
	workers  int

	mu    sync.Mutex
	stats Stats
}

// ServerConfig configures a GOCACHEPROG Server.
type ServerConfig struct {
	DiskDir  string
	Remote   RemoteBackend
	ReadOnly bool
	Verbose  bool
	Log      io.Writer
	Workers  int
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
	return &Server{
		diskDir:  absDir,
		remote:   cfg.Remote,
		readOnly: cfg.ReadOnly,
		verbose:  cfg.Verbose,
		logw:     logw,
		workers:  workers,
	}, nil
}

// Stats returns a snapshot of cache counters.
func (s *Server) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats
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

func (s *Server) writeLocal(actionHex string, outputID, body []byte, modTime time.Time) (string, error) {
	outputHex := hex.EncodeToString(outputID)
	objPath := s.objectPath(outputHex)
	if err := os.MkdirAll(filepath.Dir(objPath), 0o755); err != nil {
		return "", err
	}
	if _, err := os.Stat(objPath); err != nil {
		if err := writeAtomic(objPath, body); err != nil {
			return "", err
		}
	}
	metaPath, _ := s.entryPaths(actionHex)
	if err := os.MkdirAll(filepath.Dir(metaPath), 0o755); err != nil {
		return "", err
	}
	if modTime.IsZero() {
		modTime = time.Now().UTC()
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
	return objPath, nil
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
		// On Windows, concurrent writers to the same content-addressed object path can
		// collide on Rename; if dest already exists, the content is identical.
		if _, statErr := os.Stat(dest); statErr == nil {
			return nil
		}
		return err
	}
	cleanup = false
	return nil
}
