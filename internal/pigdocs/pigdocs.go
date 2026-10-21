// Package pigdocs materializes the reference documentation embedded in Stock
// Pig under the user's config root.
//
// Pig ships as a standalone binary. Unlike upstream Pi, which installs package
// documentation beside its npm entry point, Pig cannot assume that a source
// checkout exists. The embedded bundle lets the running agent inspect the exact
// extension, Package, and Piglet contracts that its binary implements.
//
// Each build writes its bundle to its own content-addressed directory, so PiG
// builds that share a config root (two installed versions, or stock PiG beside
// a Piglet Binary) never rewrite each other's docs, and a running session's
// docs never change under it. Users can also force a sync or read the bundle
// through `pig docs`. A session leases its bundle ([HoldBundle]), so another build's eviction never removes the pages the session's prompt names. This is stock distribution infrastructure; it does not
// activate an extension or optional Piglet.
//
// Output layout
//
//	~/.pig/docs/
//	  bundle-<hash>/       one build's bundle; <hash> is its content digest
//	    README.md          index
//	    commands.md        slash commands + CLI verbs
//	    ...                one page per embedded content/*.md
//	    .pig-docs-digest   the full digest, written last
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
	"sync"

	"github.com/gofrs/flock"

	"github.com/MichaelKinsy/PiG/internal/configroot"
	"github.com/MichaelKinsy/PiG/internal/contentdirs"
)

//go:embed content/*.md
var content embed.FS

const (
	// stagingPrefix names the private trees a sync builds beside the bundle directories.
	stagingPrefix = ".docs-stage-"
	// stagingLockFile excludes the leftover sweep and eviction, which hold it exclusively, from live syncs, which hold it shared. It does not match stagingPrefix.
	stagingLockFile = ".docs-stage.lock"
	// SubDir is the docs directory name under the pig config root.
	SubDir = "docs"
	// bundleBase names each build's bundle directory, bundle-<hash>.
	bundleBase = "bundle"
	// markerFile records the digest of the embedded bundle that owns the
	// materialized directory.
	markerFile = ".pig-docs-digest"
)

func init() { configroot.RegisterDocsBundle(bundleName) }

// bundleName is this build's bundle directory name under <config root>/docs.
func bundleName() (string, error) {
	digest, err := contentDigest()
	if err != nil {
		return "", err
	}
	return contentdirs.Name(bundleBase, digest)
}

// DocsDir returns the absolute directory of this build's bundle for a given config root.
func DocsDir(configRoot string) string {
	name, err := bundleName()
	if err != nil {
		// The embedded bundle is part of the binary; reading it fails only when the binary itself is broken.
		panic(err)
	}
	return filepath.Join(configRoot, SubDir, name)
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

// contentDigest is the digest of the embedded docs. The embedded content never changes, so it is computed once per process.
var contentDigest = sync.OnceValues(computeContentDigest)

// computeContentDigest hashes "name\x00size\x00contents" for every doc, streaming the contents so the startup check does not copy every embedded doc into memory.
func computeContentDigest() (string, error) {
	hash := sha256.New()
	buffer := make([]byte, 32<<10)
	for _, name := range List() {
		file, err := content.Open("content/" + name)
		if err != nil {
			return "", fmt.Errorf("pig docs: read %s for digest: %w", name, err)
		}
		info, err := file.Stat()
		if err != nil {
			_ = file.Close()
			return "", fmt.Errorf("pig docs: read %s for digest: %w", name, err)
		}
		_, _ = fmt.Fprintf(hash, "%s\x00%d\x00", name, info.Size())
		// The embedded file's reader is the only method used; hiding the other methods keeps io.CopyBuffer on the supplied buffer.
		_, err = io.CopyBuffer(hash, struct{ io.Reader }{file}, buffer)
		_ = file.Close()
		if err != nil {
			return "", fmt.Errorf("pig docs: read %s for digest: %w", name, err)
		}
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

// Sync writes this build's bundle directory from the embedded docs, replacing a directory already there, and returns the page names written.
// `pig docs sync` uses it to repair edited or removed pages.
func Sync(configRoot string) ([]string, error) { return syncBundle(configRoot, true) }

// syncAttempts bounds the syncs one call runs. A sync that started while another process held the staging lock exclusively may lose its staging
// tree to that process's sweep; the sweep is a single pass, so the next attempt normally succeeds.
const syncAttempts = 3

// errStagingRemoved marks a failure whose staging tree another process's sweep removed. The file system reports that as a missing file on Linux
// and as an invalid argument on macOS, so the tree itself is checked.
var errStagingRemoved = errors.New("staging tree removed by another sync")

// syncBundle writes this build's bundle directory and returns the page names written. It builds the complete tree, marker last, beside the bundle
// directories and renames it into place, so a reader sees none of the bundle or all of it. A complete bundle that a concurrent sync of this build
// installs first is kept unless replace is set. It never waits for another process. When it finds the staging lock free it first removes
// stranded staging trees and evicts the least recently used bundles of other builds; other builds' bundles are otherwise left alone.
func syncBundle(configRoot string, replace bool) ([]string, error) {
	digest, err := contentDigest()
	if err != nil {
		return nil, err
	}
	dir := DocsDir(configRoot)
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return nil, fmt.Errorf("pig docs: mkdir %s: %w", parent, err)
	}
	housekeep(parent, dir)
	var errs []error
	for range syncAttempts {
		written, err := syncOnce(parent, dir, digest, replace)
		if err == nil {
			sort.Strings(written)
			return written, nil
		}
		errs = append(errs, err)
		if current(dir, digest) && !replace {
			return List(), nil
		}
		if !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, errStagingRemoved) {
			// Only a staging tree removed under this sync is worth another attempt; a file system that refuses the write refuses it again.
			break
		}
	}
	return nil, errors.Join(errs...)
}

// housekeep tries once to take the docs staging lock in parent exclusively and, when it does, removes the staging trees that earlier syncs
// stranded by dying before their rename and evicts the least recently used bundles of other builds, never own. The operating system releases a
// dead holder's lock, and every live sync holds the lock shared while its tree exists when it can, so a tree found under the exclusive lock has no
// live owner, unless its sync started during this sweep; that sync retries. It never waits: a holder is a live sync, and startup must not stall
// behind it.
func housekeep(parent, own string) {
	lock := flock.New(filepath.Join(parent, stagingLockFile))
	if held, err := lock.TryLock(); err != nil || !held {
		return
	}
	defer func() { _ = lock.Unlock() }()
	leftovers, _ := filepath.Glob(filepath.Join(parent, stagingPrefix+"*"))
	for _, leftover := range leftovers {
		_ = os.RemoveAll(leftover)
	}
	// Eviction is housekeeping: a bundle it cannot remove only stays on disk.
	_, _ = contentdirs.Evict(parent, bundleBase, own)
}

// syncOnce builds the bundle in a staging tree and installs it as dir, holding the staging lock shared when no other process holds it
// exclusively, so housekeeping does not remove the tree.
func syncOnce(parent, dir, digest string, replace bool) ([]string, error) {
	lock := flock.New(filepath.Join(parent, stagingLockFile))
	if held, err := lock.TryRLock(); err == nil && held {
		defer func() { _ = lock.Unlock() }()
	}
	staging, written, err := buildStaging(parent, digest)
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	if err := install(staging, dir, digest, replace); err != nil {
		return nil, err
	}
	return written, nil
}

// installAttempts bounds how often install renames staging over dir. Each failed attempt observes another sync of this build replacing dir.
const installAttempts = 3

// install renames the complete tree staging to dir. A directory already there is kept when it holds this build's complete bundle and replace is
// not set; otherwise it is moved aside and removed. A concurrent sync of this build may install its own tree in between; when every attempt is
// raced, dir holding this build's complete bundle is success.
func install(staging, dir, digest string, replace bool) error {
	var installErr error
	for attempt := range installAttempts {
		installErr = os.Rename(staging, dir)
		if installErr == nil {
			return nil
		}
		if (!replace || attempt > 0) && current(dir, digest) {
			return nil
		}
		aside := staging + ".old"
		if err := os.Rename(dir, aside); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				// Another sync moved the directory aside first; the next rename may install this tree.
				continue
			}
			return fmt.Errorf("pig docs: replace %s: %w", dir, errors.Join(installErr, err))
		}
		_ = os.RemoveAll(aside)
	}
	if current(dir, digest) {
		return nil
	}
	return fmt.Errorf("pig docs: install %s: %w", dir, installErr)
}

// buildStaging writes the complete bundle, marker last, into a new private tree in parent and returns the tree and the page names written.
func buildStaging(parent, digest string) (staging string, written []string, err error) {
	entries, err := fs.ReadDir(content, "content")
	if err != nil {
		return "", nil, fmt.Errorf("pig docs: read embed: %w", err)
	}
	staging, err = os.MkdirTemp(parent, stagingPrefix)
	if err != nil {
		return "", nil, fmt.Errorf("pig docs: stage: %w", err)
	}
	fail := func(err error) (string, []string, error) {
		if _, statErr := os.Lstat(staging); errors.Is(statErr, fs.ErrNotExist) {
			err = errors.Join(err, errStagingRemoved)
		}
		_ = os.RemoveAll(staging)
		return "", nil, fmt.Errorf("pig docs: stage: %w", err)
	}
	if err := os.Chmod(staging, 0o755); err != nil {
		return fail(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := content.ReadFile("content/" + e.Name())
		if err != nil {
			return fail(err)
		}
		if err := writeNewFile(filepath.Join(staging, e.Name()), data, 0o644); err != nil {
			return fail(err)
		}
		written = append(written, e.Name())
	}
	if err := writeNewFile(filepath.Join(staging, markerFile), []byte(digest+"\n"), 0o644); err != nil {
		return fail(err)
	}
	return staging, written, nil
}

// writeNewFile creates path with exactly mode and fails if it exists.
func writeNewFile(path string, data []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Chmod(mode); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// current reports whether dir holds the complete bundle with digest: its marker, written last, matches.
func current(dir, digest string) bool {
	data, err := os.ReadFile(filepath.Join(dir, markerFile))
	return err == nil && strings.TrimSpace(string(data)) == digest
}

// HoldBundle leases this build's bundle in configRoot for as long as the process lives, so another build's eviction does not remove the pages a running
// session's prompt names. It does nothing when the bundle is not complete on disk, and nothing is written. A session does not call the returned release until it
// exits (the exit path calls it just before the exit record counts descriptors) and tests call it. The bundle is read again once leased, so an eviction between the check and the lease is not mistaken for a held bundle.
// pig additive (D22): Pi's package docs stay where they are for as long as Pi runs; PiG's bundle lives in a directory that other builds evict.
func HoldBundle(configRoot string) (release func()) {
	digest, err := contentDigest()
	if err != nil {
		return func() {}
	}
	dir := DocsDir(configRoot)
	if !current(dir, digest) {
		return func() {}
	}
	release = contentdirs.Hold(dir)
	if !current(dir, digest) {
		release()
		return func() {}
	}
	return release
}

// Prepare is the startup check that defers the write of the bundle until something can read it. It returns present, whether text may name the
// bundle, and pending, whether [EnsureSynced] must run before anything other than PiG reads it. A complete bundle of this build costs one
// marker read and, at most hourly, a recorded use time; it is present and nothing is pending. Otherwise the bundle is named when the config
// root lets this process create it, the condition under which [EnsureSynced] writes it; nothing is written here.
// pig additive (D22): Pi's package docs exist before it starts; PiG writes its bundle before the first reader instead of at every start.
func Prepare(configRoot string) (present, pending bool, err error) {
	digest, err := contentDigest()
	if err != nil {
		return false, false, err
	}
	dir := DocsDir(configRoot)
	if current(dir, digest) {
		contentdirs.Touch(dir)
		return true, false, nil
	}
	return canCreate(filepath.Dir(dir)), true, nil
}

// canCreate reports whether this process may create dir: whether it may create entries in dir, or in dir's nearest existing ancestor.
func canCreate(dir string) bool {
	for {
		info, err := os.Stat(dir)
		if err == nil {
			return info.IsDir() && canCreateIn(dir)
		}
		parent := filepath.Dir(dir)
		if !errors.Is(err, fs.ErrNotExist) || parent == dir {
			return false
		}
		dir = parent
	}
}

// EnsureSynced is the idempotent startup helper. A complete bundle of this build costs one marker read and, at most hourly, a recorded use time;
// otherwise it syncs. All errors are returned to the caller; Pig core treats any failure as the bundle being absent.
func EnsureSynced(configRoot string) error {
	digest, err := contentDigest()
	if err != nil {
		return err
	}
	if dir := DocsDir(configRoot); current(dir, digest) {
		contentdirs.Touch(dir)
		return nil
	}
	_, err = syncBundle(configRoot, false)
	return err
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
	root, err := configroot.Resolve()
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
		// The printed directory exists for whoever reads it next. The command prints it either way: on a config root this user cannot write,
		// `pig docs show` still reads the embedded pages.
		_ = EnsureSynced(root)
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
	_, _ = fmt.Fprintln(w, "  sync     write this build's docs bundle under ~/.pig/docs (default)")
	_, _ = fmt.Fprintln(w, "  path     write this build's docs bundle when missing and print its absolute directory")
	_, _ = fmt.Fprintln(w, "  list     list available doc filenames")
	_, _ = fmt.Fprintln(w, "  show     print one doc to stdout")
}
