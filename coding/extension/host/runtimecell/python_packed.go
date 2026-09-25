package runtimecell

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/internal/pigsdklock"
)

// PythonExtension describes one factory-style Python SDK extension that can be
// packed into a generated subprocess runner. The module must expose a factory
// returning pig_sdk.Extension, typically:
//
//	def new_extension() -> pig_sdk.Extension
//
// The generated runner is a Python subprocess and uses one socket per contained
// extension.
type PythonExtension struct {
	Name    string
	Root    string
	Package string
	Factory string
	Hash    string
}

// PythonPackedCell describes a generated Python packed-cell artifact.
type PythonPackedCell struct {
	Key           string
	Hash          string
	CacheDir      string
	BinaryPath    string
	Cached        bool
	BuildDuration time.Duration
	Extensions    []PythonExtension
}

func BuildPythonPackedCell(ctx context.Context, cacheRoot, key string, extensions []PythonExtension) (*PythonPackedCell, error) {
	if len(extensions) == 0 {
		return nil, fmt.Errorf("packed Python cell requires at least one extension")
	}
	normalized, err := normalizePythonExtensions(extensions)
	if err != nil {
		return nil, err
	}
	lockCandidates := append([]string{os.Getenv("PIG_SDK_PY_ROOT")}, stagedSDKRoots("sdk-py")...)
	return pigsdklock.WithBuildCandidates(ctx, lockCandidates, func() (*PythonPackedCell, error) {
		sdkRoot, err := findPythonSDKRoot()
		if err != nil {
			return nil, err
		}
		return pigsdklock.WithBuildForRoot(ctx, sdkRoot, func() (*PythonPackedCell, error) {
			hash := pythonPackedCellHash(cacheRoot, key, normalized, sdkRoot)
			if after, ok := strings.CutPrefix(hash, "error:"); ok {
				return nil, fmt.Errorf("hash Python packed cell: %s", after)
			}
			cellDir := filepath.Join(cacheRoot, "cells", "python", hash)
			start := time.Now()
			artifactName := packedRunnerName(runtime.GOOS, "python")
			entry, err := PublishArtifact(ctx, cellDir, artifactName, hash, "python", func(scratch string) (string, error) {
				out := filepath.Join(scratch, artifactName)
				if err := os.WriteFile(out, []byte(renderPythonRunner(normalized, sdkRoot)), 0o755); err != nil {
					return "", fmt.Errorf("write generated Python runner: %w", err)
				}
				return out, nil
			})
			if err != nil {
				return nil, err
			}
			var dur time.Duration
			if !entry.Reused {
				dur = time.Since(start)
			}
			return &PythonPackedCell{Key: key, Hash: hash, CacheDir: entry.Dir, BinaryPath: entry.ArtifactPath, Cached: entry.Reused, BuildDuration: dur, Extensions: normalized}, nil
		})
	})
}

func normalizePythonExtensions(extensions []PythonExtension) ([]PythonExtension, error) {
	out := append([]PythonExtension(nil), extensions...)
	for i := range out {
		ext := &out[i]
		ext.Name = strings.TrimSpace(ext.Name)
		ext.Root = strings.TrimSpace(ext.Root)
		ext.Package = strings.TrimSpace(ext.Package)
		ext.Factory = strings.TrimSpace(ext.Factory)
		ext.Hash = strings.TrimSpace(ext.Hash)
		if ext.Name == "" {
			return nil, fmt.Errorf("extension[%d]: name is required", i)
		}
		if ext.Root == "" {
			return nil, fmt.Errorf("extension %q: root is required", ext.Name)
		}
		abs, err := filepath.Abs(ext.Root)
		if err != nil {
			return nil, fmt.Errorf("extension %q: resolve root: %w", ext.Name, err)
		}
		ext.Root = abs
		if ext.Package == "" {
			return nil, fmt.Errorf("extension %q: package/module is required", ext.Name)
		}
		if ext.Factory != "new_extension" {
			return nil, fmt.Errorf("extension %q: factory must be def new_extension() -> Extension", ext.Name)
		}
		if ext.Hash == "" {
			ext.Hash = "unknown"
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func pythonPackedCellHash(cacheRoot, key string, extensions []PythonExtension, sdkRoot string) string {
	sdkHash := hashTree(sdkRoot)
	if strings.HasPrefix(sdkHash, "error:") {
		return sdkHash
	}
	h := sha256.New()
	_, _ = h.Write([]byte("pig-python-packed-cell\x00"))
	_, _ = h.Write([]byte(key))
	_, _ = h.Write([]byte("\x00"))
	hashBuildInput(h, "sdk", sdkHash)
	hashBuildInput(h, "runtime", commandVersion(cacheRoot, pythonExecutableName(runtime.GOOS), "--version"))
	hashBuildInput(h, "template", renderPythonRunner(extensions, sdkRoot))
	for _, ext := range extensions {
		for _, part := range []string{ext.Name, ext.Package, ext.Factory, ext.Hash} {
			_, _ = h.Write([]byte(part))
			_, _ = h.Write([]byte("\x00"))
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

func findPythonSDKRoot() (string, error) {
	if root := strings.TrimSpace(os.Getenv("PIG_SDK_PY_ROOT")); root != "" {
		if abs, err := filepath.Abs(root); err == nil && statHasFile(abs, filepath.Join("pig_sdk", "__init__.py")) {
			return abs, nil
		}
	}
	for _, root := range stagedSDKRoots("sdk-py") {
		if _, err := os.Stat(filepath.Join(root, "pig_sdk", "__init__.py")); err == nil {
			return root, nil
		}
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get cwd: %w", err)
	}
	for dir := wd; ; dir = filepath.Dir(dir) {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil && strings.Contains(string(data), "module github.com/MichaelKinsy/PiG") {
			if cand := filepath.Join(dir, "extensions", "sdk-py"); statHasFile(cand, filepath.Join("pig_sdk", "__init__.py")) {
				return cand, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	// Staged copy under the config-root data dir (~/.pig/source) or
	// PIG_SOURCE_ROOT, written by Stock Pig's SDK staging step. This is what
	// lets an installed pig resolve the Python SDK on a clean host with no
	// checkout and no env config.
	for _, root := range installedPigSourceRoots() {
		cand := filepath.Join(root, "extensions", "sdk-py")
		if _, err := os.Stat(filepath.Join(cand, "pig_sdk", "__init__.py")); err == nil {
			return cand, nil
		}
	}
	return "", fmt.Errorf("cannot locate github.com/MichaelKinsy/PiG checkout for Python SDK; set PIG_SDK_PY_ROOT")
}

func renderPythonRunner(extensions []PythonExtension, sdkRoot string) string {
	var b strings.Builder
	b.WriteString("import importlib\nimport os\nimport sys\nimport threading\n\n")
	b.WriteString("sys.dont_write_bytecode = True\nos.environ.setdefault('PYTHONDONTWRITEBYTECODE', '1')\n\n")
	fmt.Fprintf(&b, "sys.path.insert(0, %q)\n", filepath.ToSlash(sdkRoot))
	for _, ext := range extensions {
		fmt.Fprintf(&b, "sys.path.insert(0, %q)\n", filepath.ToSlash(ext.Root))
	}
	b.WriteString("\nITEMS = [\n")
	for _, ext := range extensions {
		fmt.Fprintf(&b, "    (%q, %q, %q, %q),\n", ext.Name, SocketEnvName(ext.Name), ext.Package, ext.Factory)
	}
	b.WriteString("]\n\n")
	b.WriteString("def _run(name, env, module_name, factory_name):\n")
	b.WriteString("    sock = os.environ.get(env)\n")
	b.WriteString("    if not sock and len(ITEMS) == 1:\n        sock = os.environ.get('PIG_EXT_SOCKET')\n")
	b.WriteString("    if not sock:\n        raise RuntimeError(f'{env} not set for {name}')\n")
	b.WriteString("    mod = importlib.import_module(module_name)\n")
	b.WriteString("    ext = getattr(mod, factory_name)()\n")
	b.WriteString("    ext.run_with_socket(sock)\n\n")
	b.WriteString(pythonRunnerFailureReport)
	b.WriteString("def main():\n")
	b.WriteString("    errors = []\n    threads = []\n")
	b.WriteString("    active_value = os.environ.get('PIG_EXT_ACTIVE_MEMBERS')\n")
	b.WriteString("    active = set(active_value.split(',')) if active_value else None\n")
	b.WriteString("    def target(item):\n        try:\n            _run(*item)\n        except BaseException as exc:\n            errors.append(f'{item[0]}: {exc}')\n            _report_member_failure(item, exc)\n")
	b.WriteString("    for item in ITEMS:\n        if active is not None and item[0] not in active:\n            continue\n        t = threading.Thread(target=target, args=(item,))\n        t.start()\n        threads.append(t)\n")
	b.WriteString("    for t in threads:\n        t.join()\n")
	b.WriteString("    if errors:\n        print('\\n'.join(errors), file=sys.stderr)\n        raise SystemExit(1)\n\n")
	b.WriteString("if __name__ == '__main__':\n    main()\n")
	return b.String()
}

// pythonRunnerFailureReport tells the host that one member failed while the
// shared process lives on: it prints the error and connects to the member's
// socket and closes it, so the host's wait for that member ends with a
// visible load error instead of waiting for a connection that never comes.
// It connects as the SDK does: CPython on Windows has no socket.AF_UNIX, and
// the SDK reaches the host's AF_UNIX socket through Winsock there.
const pythonRunnerFailureReport = `def _report_member_failure(item, exc):
    print(f'{item[0]}: {exc}', file=sys.stderr, flush=True)
    sock = os.environ.get(item[1]) or (os.environ.get('PIG_EXT_SOCKET') if len(ITEMS) == 1 else None)
    if not sock:
        return
    from pig_sdk import _connect_unix
    try:
        _connect_unix(sock).close()
    except OSError:
        pass

`

// CurrentPythonPackedCellEntry returns the valid cache entry selected by the
// same normalization and hash inputs as BuildPythonPackedCell, without building it.
func CurrentPythonPackedCellEntry(cacheRoot, key string, extensions []PythonExtension) (string, bool, error) {
	normalized, err := normalizePythonExtensions(extensions)
	if err != nil {
		return "", false, err
	}
	lockCandidates := append([]string{os.Getenv("PIG_SDK_PY_ROOT")}, stagedSDKRoots("sdk-py")...)
	return pigsdklock.WithBuildEntryCandidates(context.Background(), lockCandidates, func() (string, bool, error) {
		sdkRoot, err := findPythonSDKRoot()
		if err != nil {
			return "", false, err
		}
		return pigsdklock.WithBuildEntryForRoot(context.Background(), sdkRoot, func() (string, bool, error) {
			hash := pythonPackedCellHash(cacheRoot, key, normalized, sdkRoot)
			if after, ok := strings.CutPrefix(hash, "error:"); ok {
				return "", false, fmt.Errorf("hash Python packed cell: %s", after)
			}
			entry := filepath.Join(cacheRoot, "cells", "python", hash)
			_, valid := validCellEntry(entry, EntryIdentity{InputDigest: hash, Artifact: "runner.py", Language: "python"})
			return entry, valid, nil
		})
	})
}
