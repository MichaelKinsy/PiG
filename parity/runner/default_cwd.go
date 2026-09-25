//go:build parity

package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// Every driver copies its cwd fixture under a validated temporary root. Ancestor context discovery remains enabled in both binaries, so the root must be outside the checkout and have no discoverable ancestor context files.

// snapshotIDDigits is the width of the random suffix of every per-binary
// snapshot directory. A fixed width keeps each path that embeds one the same
// length for pig and pi, so layout, truncation, and the system-prompt size
// cannot differ between the binaries or between runs.
const snapshotIDDigits = 10

// mkdirTempFixed creates a directory under the canonical, context-free os.TempDir, named prefix followed by snapshotIDDigits random decimal digits.
func mkdirTempFixed(prefix string) (string, error) {
	root, err := cleanSnapshotRoot(os.TempDir())
	if err != nil {
		return "", fmt.Errorf("parity cwd temporary root: %w; set TMPDIR to an existing directory outside the checkout with no ancestor context files", err)
	}
	return mkdirFixed(root, prefix)
}

// cleanSnapshotRoot checks the same canonical ancestry the child process will use as its cwd. Returning the resolved path prevents a symlink alias from selecting a different ancestor chain.
func cleanSnapshotRoot(root string) (string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", root)
	}
	checkout, err := filepath.EvalSymlinks(filepath.Join(defaultCWDFixture(), "..", "..", ".."))
	if err != nil {
		return "", fmt.Errorf("resolve checkout: %w", err)
	}
	for dir := root; ; dir = filepath.Dir(dir) {
		if dir == checkout {
			return "", fmt.Errorf("%s is inside checkout %s", root, checkout)
		}
		// upstream: packages/coding-agent/src/core/resource-loader.ts:loadContextFileFromDir
		for _, name := range []string{"AGENTS.override.md", "AGENTS.md", "AGENTS.MD", "CLAUDE.md", "CLAUDE.MD"} {
			path := filepath.Join(dir, name)
			info, err := os.Stat(path)
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return "", fmt.Errorf("inspect ancestor context: %w", err)
			}
			if err == nil && info.Mode().IsRegular() {
				return "", fmt.Errorf("%s inherits ancestor context %s", root, path)
			}
		}
		if filepath.Dir(dir) == dir {
			return root, nil
		}
	}
}

// mkdirFixed creates a new directory under root named prefix followed by
// snapshotIDDigits random decimal digits.
func mkdirFixed(root, prefix string) (string, error) {
	for range 100 {
		name := fmt.Sprintf("%s%0*d", prefix, snapshotIDDigits, rand.Uint64N(10_000_000_000))
		dir := filepath.Join(root, name)
		err := os.Mkdir(dir, 0o700)
		if err == nil {
			return dir, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", err
		}
	}
	return "", fmt.Errorf("mkdir %s: too many collisions", filepath.Join(root, prefix+"*"))
}

// Documentation paths in the system prompt.
//
// With no provider usage, the footer's context percentage is a chars/4
// estimate of the system prompt. The prompts of pig and pi differ only in the
// docs section and preamble (D22): pig names itself and points at
// <PIG_HOME>/docs twice, and Pi points at its package directory three times.
// If those directories lived under the temp root or the checkout, the two
// estimates would move apart with the host's path lengths and round to
// different percentages. The runner therefore places both under the fixed
// promptPathRoot with lengths that make the two prompts equally long:
//
//	3*len(piPackageDir) == 2*(len(pigHome)+len("/docs")) + d22ConstantGap
//
// d22ConstantGap is how many more characters pig's fixed docs and preamble
// text has than Pi's, measured against Pi 0.87.1. TestPromptPathLengthsBalance
// checks the arithmetic; the rpc get_session_stats scenario checks the
// estimate itself.
const (
	promptPathRoot  = "/tmp"
	pigHomePrefix   = "parity-snap-PIG_HOME-"
	piPackagePrefix = "parity-pi-pkg-"
	piPackageLink   = "pi-coding-pkg"
	d22ConstantGap  = 47
)

// defaultCWDFixture returns parity/testdata/default-cwd.
func defaultCWDFixture() string {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return filepath.Join("parity", "testdata", "default-cwd")
	}
	return filepath.Join(filepath.Dir(filepath.Dir(thisFile)), "testdata", "default-cwd")
}

// defaultCWD returns a fresh per-binary snapshot of the default cwd fixture.
func defaultCWD(t *testing.T) (string, error) {
	t.Helper()
	return snapshotCWD(t, defaultCWDFixture())
}

// initSnapshotGitBranch makes dir an empty git repository whose HEAD names
// the unborn branch, when branch is set. Pi and pig both ask git for the
// branch (git symbolic-ref --short HEAD), so the repository must be real.
func initSnapshotGitBranch(ctx context.Context, dir, branch string) error {
	if branch == "" {
		return nil
	}
	for _, args := range [][]string{
		{"init", "--quiet", dir},
		{"-C", dir, "symbolic-ref", "HEAD", "refs/heads/" + branch},
	} {
		if out, err := exec.CommandContext(ctx, "git", args...).CombinedOutput(); err != nil {
			return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// cwdSnapshotID matches the random suffix of a cwd snapshot directory, also
// when a renderer truncated the path inside it.
var cwdSnapshotID = regexp.MustCompile(`(parity-snap-cwd-)([0-9]{1,` + fmt.Sprint(snapshotIDDigits) + `})`)

// maskCWDSnapshotIDs replaces the random digits of every cwd snapshot
// directory name with zeros of the same count. The digits are the only part
// of the path that differs between pig and pi, and the equal count keeps
// padding and truncation byte-identical.
func maskCWDSnapshotIDs(s string) string {
	if !strings.Contains(s, "parity-snap-cwd-") {
		return s
	}
	return cwdSnapshotID.ReplaceAllStringFunc(s, func(m string) string {
		digits := len(m) - len("parity-snap-cwd-")
		return "parity-snap-cwd-" + strings.Repeat("0", digits)
	})
}

// maskResultCWDSnapshotIDs applies maskCWDSnapshotIDs to every compared
// field of r.
func maskResultCWDSnapshotIDs(r Result) Result {
	r.Output = maskCWDSnapshotIDs(r.Output)
	r.Escaped = maskCWDSnapshotIDs(r.Escaped)
	r.Artifact = maskCWDSnapshotIDs(r.Artifact)
	return r
}

// pinPiPackageDir returns ref with PI_PACKAGE_DIR pointing at a symlink,
// under promptPathRoot, to the Pi package that owns ref.Path. Pi writes its
// package directory three times into the system prompt's docs section
// (getReadmePath, getDocsPath, getExamplesPath); see "Documentation paths in
// the system prompt". PI_PACKAGE_DIR is Pi's own override for that directory
// (config.ts getPackageDir), and the symlink keeps every file Pi reads through
// it unchanged.
func pinPiPackageDir(t *testing.T, ref BinaryRef) BinaryRef {
	t.Helper()
	root, err := piPackageRoot(ref.Path)
	if err != nil {
		t.Fatalf("parity: locate the Pi package for PI_PACKAGE_DIR: %v", err)
	}
	dir, err := mkdirFixed(promptPathRoot, piPackagePrefix)
	if err != nil {
		t.Fatalf("parity: create PI_PACKAGE_DIR link dir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	})
	link := filepath.Join(dir, piPackageLink)
	if err := os.Symlink(root, link); err != nil {
		t.Fatalf("parity: link PI_PACKAGE_DIR: %v", err)
	}
	ref.Env = append(slices.Clone(ref.Env), "PI_PACKAGE_DIR="+link)
	return ref
}

// piPackageRoot resolves bin's symlinks and walks up to the directory whose
// package.json names the Pi coding-agent package.
func piPackageRoot(bin string) (string, error) {
	real, err := filepath.EvalSymlinks(bin)
	if err != nil {
		return "", err
	}
	for dir := filepath.Dir(real); ; dir = filepath.Dir(dir) {
		data, err := os.ReadFile(filepath.Join(dir, "package.json"))
		if err == nil {
			var pkg struct {
				Name string `json:"name"`
			}
			if json.Unmarshal(data, &pkg) == nil && strings.HasSuffix(pkg.Name, "/pi-coding-agent") {
				return dir, nil
			}
		}
		if parent := filepath.Dir(dir); parent == dir {
			return "", fmt.Errorf("no pi-coding-agent package.json above %s", real)
		}
	}
}
