package storage_test

// Ports packages/durable/test/storage-runtime-boundary.test.ts

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

const modulePath = "github.com/MichaelKinsy/PiG/"

// hostImports are the Go counterparts of Node built-ins: packages that reach the host process, file system, network,
// or a native database driver. A portable package may not import one directly.
var hostImports = map[string]bool{
	"os": true, "os/exec": true, "os/signal": true, "syscall": true, "net": true, "net/http": true,
	"database/sql": true, "modernc.org/sqlite": true, "path/filepath": true,
}

type listedPackage struct {
	ImportPath string
	Imports    []string
}

// sourceGraph returns the durable packages that entry loads, in the way upstream follows only relative imports within
// packages/durable/src. It fails the test when any of them imports a host package.
func sourceGraph(t *testing.T, entry string) []string {
	t.Helper()
	command := exec.Command("go", "list", "-deps", "-json=ImportPath,Imports", modulePath+entry)
	output, err := command.Output()
	if err != nil {
		if exitError, ok := errors.AsType[*exec.ExitError](err); ok {
			t.Fatalf("go list %s: %v\n%s", entry, err, exitError.Stderr)
		}
		t.Fatalf("go list %s: %v", entry, err)
	}
	var graph []string
	decoder := json.NewDecoder(bufio.NewReader(strings.NewReader(string(output))))
	for {
		var listed listedPackage
		if err := decoder.Decode(&listed); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		if listed.ImportPath != modulePath+"durable" && !strings.HasPrefix(listed.ImportPath, modulePath+"durable/") {
			continue
		}
		graph = append(graph, listed.ImportPath)
		for _, imported := range listed.Imports {
			if hostImports[imported] {
				t.Errorf("%s imports the host package %s", listed.ImportPath, imported)
			}
		}
	}
	return graph
}

func some(graph []string, matches func(string) bool) bool {
	return slices.ContainsFunc(graph, matches)
}

func within(directory string) func(string) bool {
	return func(path string) bool {
		return path == modulePath+directory || strings.HasPrefix(path, modulePath+directory+"/")
	}
}

func exactly(pkg string) func(string) bool {
	return func(path string) bool { return path == modulePath+pkg }
}

func TestDurableStorageRuntimeBoundaries(t *testing.T) {
	t.Run("keeps the package root limited to portable core storage", func(t *testing.T) {
		graph := sourceGraph(t, "durable")
		for _, directory := range []string{"durable/storage/jsonl", "durable/storage/sqlite"} {
			if some(graph, within(directory)) {
				t.Errorf("the durable root loads %s", directory)
			}
		}
		// Upstream types.ts imports ExecutionEnv from ./env type-only, which loads nothing. Go has no type-only
		// import, so the root may load the portable env declarations, which sourceGraph has checked for host
		// imports, but nothing beneath them.
		if some(graph, func(path string) bool { return strings.HasPrefix(path, modulePath+"durable/env/") }) {
			t.Error("the durable root loads an environment adapter")
		}
	})

	t.Run("keeps the portable SQLite subpath free of Node imports", func(t *testing.T) {
		graph := sourceGraph(t, "durable/storage/sqlite")
		if some(graph, exactly("durable/storage/sqlite/node")) {
			t.Error("the portable SQLite package loads its node adapter")
		}
	})

	t.Run("keeps the portable environment subpath free of Node imports", func(t *testing.T) {
		graph := sourceGraph(t, "durable/env")
		if some(graph, exactly("durable/env/node")) {
			t.Error("the portable environment package loads its node adapter")
		}
	})

	t.Run("keeps the portable JSONL subpath free of Node imports", func(t *testing.T) {
		graph := sourceGraph(t, "durable/storage/jsonl")
		if some(graph, exactly("durable/storage/jsonl/node")) {
			t.Error("the portable JSONL package loads its node adapter")
		}
		if some(graph, exactly("durable/env/node")) {
			t.Error("the portable JSONL package loads the node environment")
		}
	})
}
