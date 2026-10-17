//go:build !pig_strip_node_extensions

package subprocess

import (
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// nodeRuntimeFS is the embedded runtime extracted once per test binary, under the scoped test temp directory that TestMain removes. Names carry the runtime-node/ prefix the archive's files had.
var nodeRuntimeFS extractedRuntimeFS

type extractedRuntimeFS struct{}

var extractedRuntime = sync.OnceValues(func() (fs.FS, error) {
	parent, err := os.MkdirTemp("", "pig-node-runtime-")
	if err != nil {
		return nil, err
	}
	root := filepath.Join(parent, "runtime-node")
	if err := os.Mkdir(root, 0o755); err != nil {
		return nil, err
	}
	if err := extractNodeRuntime(root); err != nil {
		return nil, err
	}
	return os.DirFS(parent), nil
})

func (extractedRuntimeFS) Open(name string) (fs.File, error) {
	tree, err := extractedRuntime()
	if err != nil {
		return nil, err
	}
	return tree.Open(name)
}

func (extractedRuntimeFS) ReadFile(name string) ([]byte, error) {
	tree, err := extractedRuntime()
	if err != nil {
		return nil, err
	}
	return fs.ReadFile(tree, name)
}
