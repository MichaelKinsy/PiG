package plugins

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"sync"

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

// hashFile is the hash of the bytes of the file at path as ContentHash gives it, streamed (a base file is never held whole
// for it); ok is false when it cannot be read (gone, a directory, no access).
func hashFile(path string) (hash string, ok bool) {
	file, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer file.Close()
	buf := hashBuffers.Get().(*[]byte)
	defer func() {
		if cap(*buf) == hashBufferSize {
			hashBuffers.Put(buf)
		}
	}()
	sum := sha256.New()
	// The reader is wrapped so that the copy uses buf, not the file's own WriterTo (which allocates a buffer of its own).
	if _, err := io.CopyBuffer(sum, struct{ io.Reader }{file}, *buf); err != nil {
		return "", false
	}
	return hex.EncodeToString(sum.Sum(nil)), true
}

// hashBuffers are the buffers files are streamed through to be hashed: a start hashes a whole base directory.
const hashBufferSize = 32 << 10

var hashBuffers = sync.Pool{New: func() any { buf := make([]byte, hashBufferSize); return &buf }}

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
	for path, hash := range r.inputs {
		out[path] = hash
	}
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
