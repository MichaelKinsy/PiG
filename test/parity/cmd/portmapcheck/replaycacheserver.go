package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// The replay cache server is a GOCACHEPROG (https://pkg.go.dev/cmd/go/internal/cacheprog): the go command of a replay starts it, speaks
// JSON lines to it over its stdin and stdout, and closes its stdin when it is done, which ends the server. It serves the lookups of the
// go command from two directories in the layout of the go command's own disk cache:
//
//   - the store (GOCACHE of the replay): what this run's replays compiled; read and written;
//   - the user's cache (envSharedCache): read only, never written, linked or touched.
//
// The layout is cmd/go's, not an API: an action entry <id[:2]>/<id>-a holds "v1 <hex id> <hex output id> <size, 20 wide> <unix
// nanoseconds, 20 wide>\n" and the output is <out[:2]>/<out>-d. The reader below accepts an entry only when it parses exactly as cmd/go
// parses it, the output is a regular file of the recorded size and its sha256 is the output id; anything else is a MISS, so a Go release
// that changes the layout makes replays slower, never wrong. TestServerReadsTheLayoutOfGo runs the repository's go against it.

const (
	hashSize  = sha256.Size
	hexSize   = hashSize * 2
	entrySize = 2 + 1 + hexSize + 1 + hexSize + 1 + 20 + 1 + 20 + 1
)

// serveCacheServerIfAsked runs the cache server when the binary was started as one, and exits.
func serveCacheServerIfAsked() {
	if len(os.Args) != 2 || os.Args[1] != cacheServerArg {
		return
	}
	shared, store, stats := os.Getenv(envSharedCache), os.Getenv("GOCACHE"), os.Getenv(envStatsFile)
	if shared == "" || store == "" {
		fmt.Fprintln(os.Stderr, "portmapcheck cache server: "+envSharedCache+" and GOCACHE must be set")
		os.Exit(2)
	}
	if err := serveReplayCache(os.Stdin, os.Stdout, shared, store, stats); err != nil {
		fmt.Fprintln(os.Stderr, "portmapcheck cache server:", err)
		os.Exit(1)
	}
	os.Exit(0)
}

type cacheRequest struct {
	ID       int64
	Command  string
	ActionID []byte `json:",omitempty"`
	OutputID []byte `json:",omitempty"`
	BodySize int64  `json:",omitempty"`
}

type cacheResponse struct {
	ID            int64
	Err           string     `json:",omitempty"`
	KnownCommands []string   `json:",omitempty"`
	Miss          bool       `json:",omitempty"`
	OutputID      []byte     `json:",omitempty"`
	Size          int64      `json:",omitempty"`
	Time          *time.Time `json:",omitempty"`
	DiskPath      string     `json:",omitempty"`
}

type cacheEntry struct {
	out  [hashSize]byte
	size int64
	when time.Time
}

type replayCacheServer struct {
	shared, store string
	hits, misses  atomic.Int64
	rejected      atomic.Int64
	puts          atomic.Int64
	copied        atomic.Int64 // bytes copied from the user's cache into the store
}

// serveReplayCache answers the go command's requests on in and out until in ends or the go command says close, then appends its
// counters to statsPath (when not empty). It returns an error only when the protocol broke.
func serveReplayCache(in io.Reader, out io.Writer, shared, store, statsPath string) error {
	s := &replayCacheServer{shared: shared, store: store}
	bw := bufio.NewWriter(out)
	enc := json.NewEncoder(bw)
	var wmu sync.Mutex
	send := func(r cacheResponse) {
		wmu.Lock()
		defer wmu.Unlock()
		_ = enc.Encode(r)
		_ = bw.Flush()
	}
	send(cacheResponse{KnownCommands: []string{"get", "put", "close"}})
	dec := json.NewDecoder(bufio.NewReader(in))
	var wg sync.WaitGroup
	var protoErr error
loop:
	for {
		var req cacheRequest
		if err := dec.Decode(&req); err != nil {
			if err != io.EOF {
				protoErr = err
			}
			break
		}
		var body []byte
		if req.Command == "put" && req.BodySize > 0 {
			// The body follows as a JSON string of base64.
			if err := dec.Decode(&body); err != nil {
				protoErr = fmt.Errorf("reading a put body: %w", err)
				break
			}
		}
		switch req.Command {
		case "get":
			wg.Go(func() { send(s.get(req)) })
		case "put":
			wg.Go(func() { send(s.put(req, body)) })
		case "close":
			wg.Wait()
			send(cacheResponse{ID: req.ID})
			break loop
		default:
			send(cacheResponse{ID: req.ID, Err: "unknown command " + req.Command})
		}
	}
	wg.Wait()
	if statsPath != "" {
		if f, err := os.OpenFile(statsPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			st := cacheStats{Hits: int(s.hits.Load()), Misses: int(s.misses.Load()), Rejected: int(s.rejected.Load()), Puts: int(s.puts.Load()), Copied: s.copied.Load()}
			_, _ = f.WriteString(st.statsLine())
			_ = f.Close()
		}
	}
	return protoErr
}

func fileName(dir string, id []byte, kind string) string {
	h := hex.EncodeToString(id)
	return filepath.Join(dir, h[:2], h+"-"+kind)
}

// readEntry reads the action entry of dir exactly as cmd/go's DiskCache.get does: any deviation is a miss.
func readEntry(dir string, action []byte) (cacheEntry, bool) {
	if len(action) != hashSize {
		return cacheEntry{}, false
	}
	f, err := os.Open(fileName(dir, action, "a"))
	if err != nil {
		return cacheEntry{}, false
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, entrySize+1) // one more byte to see that the file is not longer
	if n, err := io.ReadFull(f, buf); n != entrySize || err != io.ErrUnexpectedEOF {
		return cacheEntry{}, false
	}
	e := buf[:entrySize]
	if e[0] != 'v' || e[1] != '1' || e[2] != ' ' || e[3+hexSize] != ' ' || e[3+hexSize+1+hexSize] != ' ' || e[3+hexSize+1+hexSize+1+20] != ' ' || e[entrySize-1] != '\n' {
		return cacheEntry{}, false
	}
	eid, e := e[3:3+hexSize], e[3+hexSize:]
	eout, e := e[1:1+hexSize], e[1+hexSize:]
	esize, e := e[1:1+20], e[1+20:]
	etime := e[1 : 1+20]
	var id, out [hashSize]byte
	if _, err := hex.Decode(id[:], eid); err != nil || !bytes.Equal(id[:], action) {
		return cacheEntry{}, false
	}
	if _, err := hex.Decode(out[:], eout); err != nil {
		return cacheEntry{}, false
	}
	size, err := strconv.ParseInt(strings.TrimSpace(string(esize)), 10, 64)
	if err != nil || size < 0 {
		return cacheEntry{}, false
	}
	nanos, err := strconv.ParseInt(strings.TrimSpace(string(etime)), 10, 64)
	if err != nil || nanos < 0 {
		return cacheEntry{}, false
	}
	return cacheEntry{out: out, size: size, when: time.Unix(0, nanos)}, true
}

// outputOK reports whether the output file of e in dir is a regular file of the recorded size. cmd/go itself trusts size alone for its
// own cache, and so does this for the store, which only this run writes.
func outputOK(dir string, e cacheEntry) (path string, ok bool) {
	path = fileName(dir, e.out[:], "d")
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != e.size {
		return "", false
	}
	return path, true
}

// adoptShared copies the output of e from the user's cache into the store, and its entry with it, and returns the copy's path (ok is
// false when the user's output is not a regular file of the recorded size and sha256: it is read once, so what is checked is what is
// copied). The user's cache is checked in full because a replay builds on whatever this returns. The replay is handed the copy and
// never the user's file: an ordinary `go` cache trim (entries not used for five days) can delete that file while the replay still
// reads it, and the copy is the run's own, so nothing a replay does to its file can reach the user's cache. A hard link would be free
// of copying, but it shares the inode with the user's file (a write through it would change the user's cache), needs the two
// directories on one filesystem, and so needs a copy as its fallback as well. The cost is one copy per distinct output, once: the next
// lookup is answered from the store, and the store is emptied every replaysPerTrim replays.
func (s *replayCacheServer) adoptShared(e cacheEntry, action []byte) (path string, ok bool, err error) {
	f, err := os.Open(fileName(s.shared, e.out[:], "d"))
	if err != nil {
		return "", false, nil
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != e.size {
		return "", false, nil
	}
	data := make([]byte, e.size)
	if _, err := io.ReadFull(f, data); err != nil {
		return "", false, nil
	}
	if sum := sha256.Sum256(data); sum != e.out {
		return "", false, nil
	}
	path = fileName(s.store, e.out[:], "d")
	if err := writeFileAtomic(path, data); err != nil {
		return "", false, err
	}
	entry := fmt.Sprintf("v1 %x %x %20d %20d\n", action, e.out, e.size, e.when.UnixNano())
	if err := writeFileAtomic(fileName(s.store, action, "a"), []byte(entry)); err != nil {
		return "", false, err
	}
	s.copied.Add(e.size)
	return path, true, nil
}

func (s *replayCacheServer) get(req cacheRequest) cacheResponse {
	hit := func(e cacheEntry, path string) cacheResponse {
		s.hits.Add(1)
		t := e.when
		return cacheResponse{ID: req.ID, OutputID: e.out[:], Size: e.size, Time: &t, DiskPath: path}
	}
	if e, ok := readEntry(s.store, req.ActionID); ok {
		if p, ok := outputOK(s.store, e); ok {
			return hit(e, p)
		}
	}
	if e, ok := readEntry(s.shared, req.ActionID); ok {
		p, ok, err := s.adoptShared(e, req.ActionID)
		if err != nil {
			return cacheResponse{ID: req.ID, Err: err.Error()}
		}
		if ok {
			return hit(e, p)
		}
		s.rejected.Add(1)
	} else if _, err := os.Lstat(fileName(s.shared, req.ActionID, "a")); err == nil {
		s.rejected.Add(1) // the file is there but is not an entry this reader understands
	}
	s.misses.Add(1)
	return cacheResponse{ID: req.ID, Miss: true}
}

func (s *replayCacheServer) put(req cacheRequest, body []byte) cacheResponse {
	fail := func(err error) cacheResponse { return cacheResponse{ID: req.ID, Err: err.Error()} }
	if len(req.ActionID) != hashSize || len(req.OutputID) != hashSize || int64(len(body)) != req.BodySize {
		return fail(fmt.Errorf("malformed put: action %d bytes, output %d bytes, body %d of %d bytes", len(req.ActionID), len(req.OutputID), len(body), req.BodySize))
	}
	out := fileName(s.store, req.OutputID, "d")
	if err := writeFileAtomic(out, body); err != nil {
		return fail(err)
	}
	entry := fmt.Sprintf("v1 %x %x %20d %20d\n", req.ActionID, req.OutputID, len(body), time.Now().UnixNano())
	if err := writeFileAtomic(fileName(s.store, req.ActionID, "a"), []byte(entry)); err != nil {
		return fail(err)
	}
	s.puts.Add(1)
	return cacheResponse{ID: req.ID, DiskPath: out}
}

// writeFileAtomic writes data to path through a temporary file in the same directory, so a reader never sees a part of it.
func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".put-")
	if err != nil {
		return err
	}
	_, werr := f.Write(data)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(f.Name(), path)
	}
	if werr != nil {
		_ = os.Remove(f.Name())
	}
	return werr
}
