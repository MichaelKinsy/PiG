package build

// Ports packages/chord/src/node/plugins/base-inputs.ts

import (
	"maps"
	"os"
	"sync"

	"github.com/evanw/esbuild/pkg/api"

	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins/model"
)

// Recorder records what a build read: the one read of each file, its hash kept by path, its bytes for esbuild. It is safe for
// the goroutines of one build.
type Recorder struct {
	mu     sync.Mutex
	inputs model.BaseInputs
}

// NewRecorder is a Recorder that has read nothing.
func NewRecorder() *Recorder { return &Recorder{inputs: model.BaseInputs{}} }

// Read reads path for a build, recording its bytes' hash: the one read of the file, its bytes for esbuild.
func (r *Recorder) Read(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	hash := model.ContentHash(data)
	r.mu.Lock()
	r.inputs[path] = hash
	r.mu.Unlock()
	return data, nil
}

// Inputs is a copy of what the build read so far, by absolute path.
func (r *Recorder) Inputs() model.BaseInputs {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(model.BaseInputs, len(r.inputs))
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
