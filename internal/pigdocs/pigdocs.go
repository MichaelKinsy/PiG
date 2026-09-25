// Package pigdocs materializes the reference documentation embedded in Stock
// Pig under the user's config root (`~/.pig/docs` by default).
//
// Pig ships as a standalone binary. Unlike upstream Pi, which installs package
// documentation beside its npm entry point, Pig cannot assume that a source
// checkout exists. The embedded bundle lets the running agent inspect the exact
// extension, Package, and Piglet contracts that its binary implements.
//
// The docs sync is idempotent and content-addressed. Users can also force a sync
// or read the bundle through `pig docs`. This is stock distribution
// infrastructure; it does not activate an extension or optional Piglet.
//
// Output layout
//
//	~/.pig/docs/
//	  README.md            index
//	  commands.md          slash commands + CLI verbs
//	  concepts.md          how pig fits together: resources, packages, piglets
//	  config.md            settings, env vars, paths
//	  divergences.md       pig vs upstream pi
//	  extension-api.md     subprocess SDK host API surface
//	  extensions.md        extension model, manifest, lifecycle
//	  models.md            model selection, cycling, thinking
//	  packages.md          packages: shareable resource bundles installed with pig install
//	  piglets.md          Piglet schema, carriers, component closure, and environments
//	  providers.md         provider list, auth, env
//	  runtime-cells.md     packed-cell runtime mechanics
//	  skills.md            SKILL.md skills: format, loading, discovery
package pigdocs

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed content/*.md
var content embed.FS

const (
	// SubDir is the docs directory name under the pig config root.
	SubDir = "docs"
	// markerFile records the digest of the embedded bundle that owns the
	// materialized directory.
	markerFile = ".pig-docs-digest"
)

// DocsDir returns the absolute docs directory for a given config root.
func DocsDir(configRoot string) string {
	return filepath.Join(configRoot, SubDir)
}

// List returns the embedded doc filenames in stable alphabetical
// order. It is the canonical source for `pig docs list` and for any
// caller that needs to enumerate available topics.
func List() []string {
	entries, err := content.ReadDir("content")
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

func contentDigest() (string, error) {
	hash := sha256.New()
	for _, name := range List() {
		data, err := content.ReadFile("content/" + name)
		if err != nil {
			return "", fmt.Errorf("pig docs: read %s for digest: %w", name, err)
		}
		_, _ = fmt.Fprintf(hash, "%s\x00%d\x00", name, len(data))
		_, _ = hash.Write(data)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// Read returns the embedded bytes of a single doc file. Names may be
// given with or without the .md suffix; ".." and absolute paths are
// rejected so this is safe to drive from CLI input.
func Read(name string) ([]byte, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("pig docs: empty doc name")
	}
	if strings.ContainsAny(name, "/\\") || strings.Contains(name, "..") {
		return nil, fmt.Errorf("pig docs: invalid doc name %q", name)
	}
	if !strings.HasSuffix(name, ".md") {
		name += ".md"
	}
	return content.ReadFile("content/" + name)
}

// Sync writes every embedded doc into <configRoot>/docs, overwriting
// existing files. It also writes the content digest marker. Returns the
// list of relative paths written.
func Sync(configRoot string) ([]string, error) {
	digest, err := contentDigest()
	if err != nil {
		return nil, err
	}
	dir := DocsDir(configRoot)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("pig docs: mkdir %s: %w", dir, err)
	}
	entries, err := fs.ReadDir(content, "content")
	if err != nil {
		return nil, fmt.Errorf("pig docs: read embed: %w", err)
	}
	written := make([]string, 0, len(entries))
	managed := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		managed[e.Name()] = struct{}{}
		data, err := content.ReadFile("content/" + e.Name())
		if err != nil {
			return nil, fmt.Errorf("pig docs: read %s: %w", e.Name(), err)
		}
		target := filepath.Join(dir, e.Name())
		if err := writeFileAtomic(target, data, 0o644); err != nil {
			return nil, err
		}
		written = append(written, e.Name())
	}
	// This directory is fully managed by Pig. Remove Markdown pages that were
	// present in an older bundle so renamed/folded topics cannot survive a sync
	// and mislead users or coding agents.
	onDisk, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("pig docs: read managed dir: %w", err)
	}
	for _, entry := range onDisk {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		if _, ok := managed[entry.Name()]; ok {
			continue
		}
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
			return nil, fmt.Errorf("pig docs: prune %s: %w", entry.Name(), err)
		}
	}
	if err := writeFileAtomic(filepath.Join(dir, markerFile), []byte(digest+"\n"), 0o644); err != nil {
		return nil, err
	}
	sort.Strings(written)
	return written, nil
}

// EnsureSynced is the idempotent startup helper. It writes embedded docs only
// when the on-disk digest differs, so warm starts do no writable filesystem
// work. All errors are returned to the caller; Pig core should treat any
// failure as non-fatal (logs a warning at most).
func EnsureSynced(configRoot string) error {
	digest, err := contentDigest()
	if err != nil {
		return err
	}
	dir := DocsDir(configRoot)
	marker := filepath.Join(dir, markerFile)
	if data, err := os.ReadFile(marker); err == nil && strings.TrimSpace(string(data)) == digest {
		return nil
	}
	_, err = Sync(configRoot)
	return err
}

// writeFileAtomic writes through a temp file + rename so a partial
// write never leaves a half-populated doc on disk.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".pigdocs-*")
	if err != nil {
		return fmt.Errorf("pig docs: temp: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		_ = os.Remove(tmpName)
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("pig docs: write %s: %w", path, err)
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("pig docs: chmod %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("pig docs: close %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("pig docs: rename %s: %w", path, err)
	}
	return nil
}

// RunCommand implements the stock pre-session `pig docs` command. It returns
// -1 when the arguments target another command.
//
//	pig docs                       sync + summary (default)
//	pig docs sync                  force re-sync embedded docs
//	pig docs path                  print docs dir
//	pig docs list                  list available doc files
//	pig docs show <name>           print one doc to stdout
func RunCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "docs" {
		return -1
	}
	sub := ""
	rest := args[1:]
	if len(rest) > 0 {
		sub = rest[0]
		rest = rest[1:]
	}
	root, err := configRoot()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "pig docs: %v\n", err)
		return 1
	}
	switch sub {
	case "", "sync":
		written, err := Sync(root)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "pig docs: %v\n", err)
			return 1
		}
		_, _ = fmt.Fprintf(stdout, "pig docs: synced %d files to %s\n", len(written), DocsDir(root))
		for _, name := range written {
			_, _ = fmt.Fprintf(stdout, "  %s\n", name)
		}
		return 0
	case "path":
		_, _ = fmt.Fprintln(stdout, DocsDir(root))
		return 0
	case "list":
		for _, name := range List() {
			_, _ = fmt.Fprintln(stdout, name)
		}
		return 0
	case "show":
		if len(rest) == 0 {
			_, _ = fmt.Fprintln(stderr, "pig docs show <name>")
			return 2
		}
		data, err := Read(rest[0])
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "pig docs: %v\n", err)
			return 1
		}
		_, _ = stdout.Write(data)
		return 0
	case "-h", "--help", "help":
		printUsage(stdout)
		return 0
	default:
		_, _ = fmt.Fprintf(stderr, "pig docs: unknown subcommand %q\n", sub)
		printUsage(stderr)
		return 2
	}
}

func printUsage(w io.Writer) {
	_, _ = fmt.Fprintln(w, "Usage: pig docs [sync|path|list|show <name>]")
	_, _ = fmt.Fprintln(w, "  sync     write embedded docs into ~/.pig/docs (default)")
	_, _ = fmt.Fprintln(w, "  path     print absolute docs directory")
	_, _ = fmt.Fprintln(w, "  list     list available doc filenames")
	_, _ = fmt.Fprintln(w, "  show     print one doc to stdout")
}

// configRoot mirrors codingagent.ConfigRoot to avoid an import cycle. The docs
// command runs before runtime services are constructed.
func configRoot() (string, error) {
	if v := os.Getenv("PIG_HOME"); v != "" {
		return expandTilde(v), nil
	}
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(expandTilde(v), "pig"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".pig"), nil
}

func expandTilde(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			if p == "~" {
				return home
			}
			return filepath.Join(home, p[2:])
		}
	}
	return p
}
