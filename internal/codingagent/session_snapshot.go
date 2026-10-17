package codingagent

import (
	"io"
	"os"
	"sync"
)

// sessionSnapshot is a Session's private copy of its session file: the records of entries that are not resident are read back from it, so
// a change another program makes to the session file reaches no read, as none reaches Pi's entries, which Pi reads into memory once
// (session-manager.ts setSessionFile, loadEntriesFromFile). It holds the file's lines as the Session loaded them and every line the Session
// writes after them. The file has no name: it is removed when it is created, or deleted on close, so it is gone when the process ends;
// os.File closes it when the snapshot is no longer reachable, from the Session or a lazy entry it handed out.
// pig additive (D112): Pi holds every session entry in memory; the snapshot keeps their records on disk instead.
type sessionSnapshot struct {
	file *os.File
	mu   sync.Mutex
	size int64
}

func (p *sessionSnapshot) ReadAt(buf []byte, off int64) (int, error) { return p.file.ReadAt(buf, off) }

// add writes lines, each followed by a newline, after the snapshot's last byte and returns the offset of the first. A failed write leaves
// the size unchanged, so the next one overwrites its partial bytes.
func (p *sessionSnapshot) add(lines ...[]byte) (int64, error) {
	n := 0
	for _, line := range lines {
		n += len(line) + 1
	}
	buf := make([]byte, 0, n)
	for _, line := range lines {
		buf = append(append(buf, line...), '\n')
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	off := p.size
	if _, err := p.file.WriteAt(buf, off); err != nil {
		return -1, err
	}
	p.size += int64(n)
	return off, nil
}

// addRaw writes data after the snapshot's last byte and returns its offset.
func (p *sessionSnapshot) addRaw(data []byte) (int64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	off := p.size
	if _, err := p.file.WriteAt(data, off); err != nil {
		return -1, err
	}
	p.size += int64(len(data))
	return off, nil
}

func (p *sessionSnapshot) close() { _ = p.file.Close() }

// newSessionSnapshot creates an empty snapshot for the session file at path in snapshotDir: beside it, where a clone of it can share its
// blocks and its bytes cost no memory (the temporary directory on Windows, where a snapshot is a copy that its directory would list), or in
// the temporary directory when that directory takes no file.
var newSessionSnapshot = func(path string) (*sessionSnapshot, error) {
	file, err := anonymousFile(snapshotDir(path))
	if err != nil {
		if file, err = anonymousFile(os.TempDir()); err != nil {
			return nil, err
		}
	}
	return &sessionSnapshot{file: file}, nil
}

// cloneSessionSnapshot returns a snapshot that shares the blocks of the session file src, opened from path, when its file system clones
// files (Linux FICLONE, APFS clonefile). ok is false when it does not.
var cloneSessionSnapshot = func(src *os.File, path string) (*sessionSnapshot, bool) {
	file, ok := cloneFile(src, path)
	if !ok {
		return nil, false
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, false
	}
	return &sessionSnapshot{file: file, size: info.Size()}, true
}

// snapshotCopier writes what a load reads from a session file into a snapshot, on its own goroutine so the write overlaps the load's
// decoding. A failed write ends the copy: the load then keeps reading the session file and gets no snapshot.
type snapshotCopier struct {
	snapshot *sessionSnapshot
	chunks   chan []byte
	free     chan []byte
	done     chan error
	made     int
}

// snapshotCopyBuffers bounds the chunks queued for the copier: the load waits for a free buffer rather than queue more.
// pig additive (D112): the copy of a session file into its snapshot holds at most this many read chunks.
const snapshotCopyBuffers = 4

func startSnapshotCopy(snapshot *sessionSnapshot) *snapshotCopier {
	c := &snapshotCopier{snapshot: snapshot, chunks: make(chan []byte, snapshotCopyBuffers), free: make(chan []byte, snapshotCopyBuffers), done: make(chan error, 1)}
	go func() {
		var err error
		for chunk := range c.chunks {
			if err == nil {
				if _, werr := snapshot.file.WriteAt(chunk, snapshot.size); werr != nil {
					err = werr
				} else {
					snapshot.size += int64(len(chunk))
				}
			}
			c.free <- chunk
		}
		c.done <- err
	}()
	return c
}

// Write queues a copy of p, in one of at most snapshotCopyBuffers buffers.
func (c *snapshotCopier) Write(p []byte) (int, error) {
	var buf []byte
	if c.made < snapshotCopyBuffers {
		c.made++
	} else {
		buf = <-c.free
	}
	c.chunks <- append(buf[:0], p...)
	return len(p), nil
}

// finish waits for the queued chunks and returns the snapshot, or nil with the snapshot closed when a write failed.
func (c *snapshotCopier) finish() *sessionSnapshot {
	close(c.chunks) //portlint:allow doubleclose finish runs once per copier: both callers (session_file.go) drop the copier after it
	if err := <-c.done; err != nil {
		c.snapshot.close()
		return nil
	}
	return c.snapshot
}

var _ io.Writer = (*snapshotCopier)(nil)
