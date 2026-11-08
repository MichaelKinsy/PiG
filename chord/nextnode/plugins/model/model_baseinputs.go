package model

import (
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"io"
	"maps"
	"os"
	"sync"
	"sync/atomic"
)

// Ports packages/chord/src/node/plugins/base-inputs.ts. What a base build read (decision "Workers start from the base code
// the server booted with", slice 11 §1; design §21): each file's bytes, hashed as esbuild's loader receives them (one read
// per file, never a later read racing a save), the record the watcher's `base` mark compares the files on disk with.
//
// Upstream's `readRecorded(path, inputs)` and `recordedSources(inputs)` share one `Map` the build's callbacks fill; esbuild's
// Go API runs those callbacks on several goroutines, so the map is a Recorder, which keeps it under a lock: one mechanism for
// the read, the plugin and the record.

// BaseInputs is what a base artifact was built from: the hash of each file's bytes as read (ContentHash), by absolute path.
type BaseInputs map[string]string

// ContentHash is the hash of data a base record keeps (SHA-256, hex).
func ContentHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// HashFile is the hash of the bytes of the file at path as ContentHash gives it, streamed (a base file is never held whole for
// it); ok is false when it cannot be read (gone, a directory, no access).
func HashFile(path string) (text string, ok bool) {
	file, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer func() { _ = file.Close() }()
	state := hashers.Get().(*hasher)
	defer hashers.Put(state)
	state.sum.Reset()
	// The reader is wrapped so that the copy uses the buffer, not the file's own WriterTo (which allocates one of its own).
	if _, err := io.CopyBuffer(state.sum, struct{ io.Reader }{file}, state.buf[:]); err != nil {
		return "", false
	}
	hex.Encode(state.text[:], state.sum.Sum(state.digest[:0]))
	return string(state.text[:]), true
}

// hasher is what hashing one file needs, kept for the next: a start hashes a whole base directory, so the digest, its
// text and the buffer the file is streamed through are made once per goroutine at work, not once per file.
type hasher struct {
	sum    hash.Hash
	buf    [32 << 10]byte
	digest [sha256.Size]byte
	text   [2 * sha256.Size]byte
}

var hashers = sync.Pool{New: func() any { return &hasher{sum: sha256.New()} }}

// HashFiles is the hash of each of files (hashFile's), "" for one that cannot be read, read by at most maxComparers goroutines
// that end before it returns: a start hashes a whole base directory, and upstream reads its files at once on libuv's threads.
func HashFiles(files []string) []string {
	hashes := make([]string, len(files))
	var next atomic.Int64
	var group sync.WaitGroup
	for range min(MaxComparers, len(files)) {
		group.Go(func() {
			for {
				i := int(next.Add(1)) - 1
				if i >= len(files) {
					return
				}
				hashes[i], _ = HashFile(files[i])
			}
		})
	}
	group.Wait()
	return hashes
}

// MaxComparers bounds the goroutines reading base files at once.
const MaxComparers = 8
