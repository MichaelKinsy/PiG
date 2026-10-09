package main

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
)

// The shared derivation cache. Every ledger lane derives the same 12k rows from nearly the same tree, and the two expensive stages are
// pure functions of files: the reach graph (cmd/prodreach) of the source that cmd/pig links, and the decisions of the rules over the Go
// tree, the upstream inventory and the reviewed inputs. Each result is stored once under a content key, so a lane (or the integrator)
// whose inputs are byte-identical to an earlier derivation reads it instead of recomputing it.
//
// The key is exact, not a heuristic: it hashes every file the derivation reads (inputFiles), the engine included, so a hit returns
// what a full derivation would have written. A per-package incremental derivation is deliberately absent: a row's decision depends on
// the declarations of every package its Go types reach and on every caller and test of the symbol it picks, and cmd/pig links all of
// them, so no sound smaller footprint exists (docs/parity/gap-closure/ledger-autobind.md, "The derivation cache").

// cacheSchema versions the stored layout; a change to what is stored bumps it, which changes every key.
const cacheSchema = "ledger-cache-1"

// ledgerCache is the on-disk store; a nil cache computes everything.
type ledgerCache struct {
	dir string
	log io.Writer
}

// defaultCacheDir returns PIG_LEDGER_CACHE when it is set (lane hosts point it at one directory shared by every lane), else a
// per-checkout directory.
func defaultCacheDir(root string) string {
	if env := os.Getenv("PIG_LEDGER_CACHE"); env != "" {
		return env
	}
	return filepath.Join(root, "build", "ledger-cache")
}

// openCache resolves the -cache flag: "" disables the cache, "auto" picks defaultCacheDir, anything else is the directory.
func openCache(flagValue, root string, log io.Writer) (*ledgerCache, error) {
	dir := flagValue
	switch dir {
	case "":
		return nil, nil
	case "auto":
		dir = defaultCacheDir(root)
	}
	if err := os.MkdirAll(dir, 0o775); err != nil {
		return nil, fmt.Errorf("cache directory: %w", err)
	}
	return &ledgerCache{dir: dir, log: log}, nil
}

func (c *ledgerCache) logf(format string, args ...any) {
	if c != nil && c.log != nil {
		_, _ = fmt.Fprintf(c.log, format+"\n", args...)
	}
}

// inputFiles reports whether the derivation reads the repository file at rel (slash separated). The set is a superset of what the engine
// opens: Go sources (go/packages, the library route, the sync's declaration parse), the module files, the upstream inventory and
// mapping, the behavior contracts, PORT_MAP, and the engine's own inputs. The gap baseline and the generated reports are outputs, not
// inputs.
func inputFiles(rel string) bool {
	switch {
	case strings.HasSuffix(rel, ".go"):
		return true
	case path.Base(rel) == "go.mod", path.Base(rel) == "go.sum", rel == "go.work", rel == "go.work.sum":
		// A workspace module's go.mod takes part in the workspace's version selection, so it can change the types the index loads.
		return true
	case rel == "test/parity/behavior-contracts.toml", rel == "docs/parity/PORT_MAP.md":
		return true
	case strings.HasPrefix(rel, "test/parity/interfaces/"):
		return true
	case strings.HasPrefix(rel, "test/parity/interface-closure/autobind/"):
		return true
	}
	return false
}

// listRepoFiles returns the tracked and untracked non-ignored files of root, plus every file under a git-ignored path that the go command can
// load. Go does not read .gitignore: `./...` loads a scratch package under build/, bin/ or tmp/, so its sources are inputs of the index. A
// fixture module outside any git repository falls back to a directory walk.
func listRepoFiles(root string) ([]string, error) {
	files, err := gitFiles(root, "-co", "--exclude-standard")
	if err != nil {
		return walkGoVisible(root, "")
	}
	ignored, err := gitFiles(root, "-oi", "--exclude-standard", "--directory")
	if err != nil {
		return nil, fmt.Errorf("list ignored files: %w", err)
	}
	for _, entry := range ignored {
		if !goVisible(strings.TrimSuffix(entry, "/")) {
			continue
		}
		if !strings.HasSuffix(entry, "/") {
			files = append(files, entry)
			continue
		}
		more, err := walkGoVisible(root, strings.TrimSuffix(entry, "/"))
		if err != nil {
			return nil, err
		}
		files = append(files, more...)
	}
	return files, nil
}

func gitFiles(root string, args ...string) ([]string, error) {
	cmd := exec.Command("git", append(append([]string{"ls-files"}, args...), "-z")...)
	cmd.Dir = root
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	var files []string
	for f := range strings.SplitSeq(out.String(), "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	return files, nil
}

// goVisible reports whether the go command can load a package from the slash-separated path: no element starts with "." or "_" or is
// testdata.
func goVisible(rel string) bool {
	for elem := range strings.SplitSeq(rel, "/") {
		if strings.HasPrefix(elem, ".") || strings.HasPrefix(elem, "_") || elem == "testdata" {
			return false
		}
	}
	return true
}

// walkGoVisible lists the files under root/sub (root itself when sub is empty) whose directories the go command can load packages from.
func walkGoVisible(root, sub string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(filepath.Join(root, filepath.FromSlash(sub)), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel != "." && !goVisible(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		files = append(files, rel)
		return nil
	})
	return files, err
}

// hashFiles returns "path\x00sha256" lines, sorted, for the files that exist; a file the listing names but the tree no longer holds (a
// deletion not yet staged) is not part of the input.
func hashFiles(root string, files []string) ([]string, error) {
	lines := make([]string, len(files))
	sem := make(chan struct{}, max(4, runtime.GOMAXPROCS(0)))
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	for i, rel := range files {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
			switch {
			case errors.Is(err, fs.ErrNotExist):
				return
			case err != nil:
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				return
			}
			sum := sha256.Sum256(data)
			lines[i] = rel + "\x00" + hex.EncodeToString(sum[:])
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	lines = slices.DeleteFunc(lines, func(s string) bool { return s == "" })
	slices.Sort(lines)
	return lines, nil
}

// upstreamPin identifies the upstream mirror the derivation reads by the content of its source files under .upstream/current/packages (the
// directory the alias, library and placement rules read). A version directory is not assumed immutable: a mirror that was filled wrongly and
// repaired in place must not leave entries keyed on the wrong content.
func upstreamPin(root string) (string, error) {
	base := filepath.Join(root, ".upstream", "current", "packages")
	resolved, err := filepath.EvalSymlinks(base)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var files []string
	err = filepath.WalkDir(resolved, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(resolved, p)
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return "", err
	}
	lines, err := hashFiles(resolved, files)
	if err != nil {
		return "", err
	}
	return keyOf(append([]string{"mirror"}, lines...)...), nil
}

// goEnv is the toolchain identity both keys include.
func goEnv(root string) (string, error) {
	cmd := exec.Command("go", "env", "GOVERSION", "GOOS", "GOARCH", "CGO_ENABLED", "GOFLAGS", "GOEXPERIMENT")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("go env: %w", err)
	}
	// GOWORK=off (or another workspace file) changes which modules `./...` loads; its resolved value is a path inside each checkout, so only
	// the explicit setting enters the key.
	return string(out) + "GOWORK=" + os.Getenv("GOWORK") + "\n", nil
}

// keyOf hashes labelled parts into a short stable key.
func keyOf(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		_, _ = fmt.Fprintf(h, "%d:%s\n", len(p), p)
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// inputKey is the content key of a derivation: the toolchain, the pinned upstream mirror and every input file.
func inputKey(root, version string) (string, error) {
	files, err := listRepoFiles(root)
	if err != nil {
		return "", fmt.Errorf("list files: %w", err)
	}
	files = slices.DeleteFunc(files, func(f string) bool { return !inputFiles(f) })
	lines, err := hashFiles(root, files)
	if err != nil {
		return "", err
	}
	env, err := goEnv(root)
	if err != nil {
		return "", err
	}
	pin, err := upstreamPin(root)
	if err != nil {
		return "", err
	}
	return keyOf(cacheSchema, "derive", version, env, pin, strings.Join(lines, "\n")), nil
}

// derivationKey is inputKey, plus the content of the -reach file when one is given: a derivation over a supplied reach graph is a
// function of that file too, so it must not be stored under (or read from) the key of the tree's own reach graph.
func derivationKey(root string, o options) (string, error) {
	key, err := inputKey(root, o.version)
	if err != nil || o.reach == "" {
		return key, err
	}
	data, err := os.ReadFile(o.reach)
	if err != nil {
		return "", fmt.Errorf("reach file: %w", err)
	}
	sum := sha256.Sum256(data)
	return keyOf(key, "reach-file", hex.EncodeToString(sum[:])), nil
}

// reachKey is the content key of the reach graph: the toolchain and the source of every package that cmd/pig and cmd/pig-experimental link
// in either build. Tests, tools and packages the binaries do not link do not change what they can reach, so a lane that only adds tests
// or ledger rows hits the entry another lane stored.
func reachKey(root string, tags []string, patterns []string) (string, error) {
	env, err := goEnv(root)
	if err != nil {
		return "", err
	}
	parts := []string{cacheSchema, "reach", env, strings.Join(tags, ";"), strings.Join(patterns, ";")}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	for _, set := range append([]string{""}, tags...) {
		args := []string{"list", "-deps", "-f", `{{if not .Standard}}{{.ImportPath}}|{{.Dir}}|{{with .Module}}{{.Path}}@{{.Version}}{{end}}|{{join .GoFiles ","}}|{{join .CgoFiles ","}}|{{join .SFiles ","}}|{{join .EmbedFiles ","}}{{end}}`}
		if set != "" {
			args = append(args, "-tags="+set)
		}
		args = append(args, patterns...)
		cmd := exec.Command("go", args...)
		cmd.Dir = root
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("go list: %w: %s", err, strings.TrimSpace(stderr.String()))
		}
		var files []string
		var pkgLines []string
		for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
			if line == "" {
				continue
			}
			f := strings.Split(line, "|")
			pkgLines = append(pkgLines, f[0]+"|"+f[2])
			// A package inside the repository contributes its file contents; a module-cache package is identified by its pinned version
			// (go.sum, below, pins the content).
			rel, err := filepath.Rel(rootAbs, f[1])
			if err != nil || strings.HasPrefix(rel, "..") {
				continue
			}
			for _, group := range f[3:] {
				for name := range strings.SplitSeq(group, ",") {
					if name != "" {
						files = append(files, filepath.ToSlash(filepath.Join(rel, name)))
					}
				}
			}
		}
		slices.Sort(files)
		files = slices.Compact(files)
		lines, err := hashFiles(root, files)
		if err != nil {
			return "", err
		}
		slices.Sort(pkgLines)
		parts = append(parts, set, strings.Join(pkgLines, "\n"), strings.Join(lines, "\n"))
	}
	modLines, err := hashFiles(root, []string{"go.mod", "go.sum"})
	if err != nil {
		return "", err
	}
	parts = append(parts, strings.Join(modLines, "\n"))
	// The reach tool is part of the key.
	toolFiles, err := listRepoFiles(root)
	if err != nil {
		return "", err
	}
	toolFiles = slices.DeleteFunc(toolFiles, func(f string) bool {
		return !strings.HasPrefix(f, "test/parity/cmd/prodreach/") || !strings.HasSuffix(f, ".go")
	})
	toolLines, err := hashFiles(root, toolFiles)
	if err != nil {
		return "", err
	}
	parts = append(parts, strings.Join(toolLines, "\n"))
	return keyOf(parts...), nil
}

func (c *ledgerCache) entryDir(kind, key string) string { return filepath.Join(c.dir, kind+"-"+key) }

// publish writes the files of an entry into a scratch directory and renames it into place, so a reader sees an entry whole or not at all.
func (c *ledgerCache) publish(kind, key string, files map[string][]byte) error {
	final := c.entryDir(kind, key)
	tmp, err := os.MkdirTemp(c.dir, "."+kind+"-tmp-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(tmp, name), data, 0o664); err != nil {
			return err
		}
	}
	if err := os.Chmod(tmp, 0o775); err != nil {
		return err
	}
	if err := os.Rename(tmp, final); err != nil {
		if _, statErr := os.Stat(final); statErr == nil {
			return nil // another process published the same content first
		}
		return err
	}
	return nil
}

// read returns one file of an entry and marks the entry used.
func (c *ledgerCache) read(kind, key, name string) ([]byte, bool) {
	dir := c.entryDir(kind, key)
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return nil, false
	}
	now := time.Now()
	_ = os.Chtimes(dir, now, now)
	return data, true
}

// prune removes entries (and their lock files) nobody has read for a week.
func (c *ledgerCache) prune() {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-7 * 24 * time.Hour)
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "reach-") && !strings.HasPrefix(e.Name(), "derive-") {
			continue
		}
		if info, err := e.Info(); err == nil && info.ModTime().Before(cutoff) {
			_ = os.RemoveAll(filepath.Join(c.dir, e.Name()))
		}
	}
}

// derivation is what the rules decide, before the reviewed gaps and holds apply: the decisions and the reviewed type exceptions a
// decision used.
type derivation struct {
	Decisions    []*decision `json:"decisions"`
	ReviewedUsed []string    `json:"reviewedUsed"`
}

// usedSet returns the reviewed type exceptions a decision used, as a set.
func (dv *derivation) usedSet() map[string]bool {
	used := make(map[string]bool, len(dv.ReviewedUsed))
	for _, id := range dv.ReviewedUsed {
		used[id] = true
	}
	return used
}

func (c *ledgerCache) loadDerivation(key string) (*derivation, bool) {
	data, ok := c.read("derive", key, "derivation.json.gz")
	if !ok {
		return nil, false
	}
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, false
	}
	var dv derivation
	if err := json.NewDecoder(zr).Decode(&dv); err != nil {
		return nil, false
	}
	return &dv, true
}

func (c *ledgerCache) storeDerivation(key string, dv *derivation, meta map[string]any) error {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if err := json.NewEncoder(zw).Encode(dv); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	m, _ := json.MarshalIndent(meta, "", " ")
	return c.publish("derive", key, map[string][]byte{"derivation.json.gz": buf.Bytes(), "meta.json": m})
}

func (c *ledgerCache) loadReach(key string) ([]string, bool) {
	data, ok := c.read("reach", key, "reach.json")
	if !ok {
		return nil, false
	}
	var list []string
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, false
	}
	return list, true
}

func (c *ledgerCache) storeReach(key string, data []byte) error {
	return c.publish("reach", key, map[string][]byte{"reach.json": data})
}
