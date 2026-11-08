package plugins

import (
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"io"
	"maps"
	"os"
	"sync"
	"sync/atomic"

	"github.com/evanw/esbuild/pkg/api"
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

// HashFile is the hash of the bytes of the file at path as ContentHash gives it, streamed; ok is false when it cannot be read.
func HashFile(path string) (text string, ok bool) { return hashFile(path) }

// hashFile is the hash of the bytes of the file at path as ContentHash gives it, streamed (a base file is never held whole
// for it); ok is false when it cannot be read (gone, a directory, no access).
func hashFile(path string) (text string, ok bool) {
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

// Recorder records what a build read: the one read of each file, its hash kept by path, its bytes for esbuild. It is safe for
// the goroutines of one build.
type Recorder struct {
	mu     sync.Mutex
	inputs BaseInputs
}

// NewRecorder is a Recorder that has read nothing.
func NewRecorder() *Recorder { return &Recorder{inputs: BaseInputs{}} }

// Read reads path for a build, recording its bytes' hash: the one read of the file, its bytes for esbuild.
func (r *Recorder) Read(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	hash := ContentHash(data)
	r.mu.Lock()
	r.inputs[path] = hash
	r.mu.Unlock()
	return data, nil
}

// Inputs is a copy of what the build read so far, by absolute path.
func (r *Recorder) Inputs() BaseInputs {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(BaseInputs, len(r.inputs))
	maps.Copy(out, r.inputs)
	return out
}

// Plugin is an esbuild plugin loading every file through Read, with esbuild's own loader for its extension: what the build
// read, in Inputs. It is the last of a build's plugins: an earlier OnLoad returning contents reads (and records) the file
// itself.
func (r *Recorder) Plugin() api.Plugin {
	return api.Plugin{
		Name: "chord-recorded-sources",
		Setup: func(builder api.PluginBuild) {
			builder.OnLoad(api.OnLoadOptions{Filter: ".*"}, func(args api.OnLoadArgs) (api.OnLoadResult, error) {
				if args.Namespace != "file" {
					return api.OnLoadResult{}, nil
				}
				data, err := r.Read(args.Path)
				if err != nil {
					return api.OnLoadResult{}, err
				}
				text := string(data)
				return api.OnLoadResult{Contents: &text, Loader: api.LoaderDefault}, nil
			})
		},
	}
}

// hashFiles is the hash of each of files (hashFile's), "" for one that cannot be read, read by at most maxComparers goroutines
// that end before it returns: a start hashes a whole base directory, and upstream reads its files at once on libuv's threads.
func hashFiles(files []string) []string {
	hashes := make([]string, len(files))
	var next atomic.Int64
	var group sync.WaitGroup
	for range min(maxComparers, len(files)) {
		group.Go(func() {
			for {
				i := int(next.Add(1)) - 1
				if i >= len(files) {
					return
				}
				hashes[i], _ = hashFile(files[i])
			}
		})
	}
	group.Wait()
	return hashes
}
