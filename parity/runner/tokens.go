//go:build parity

package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Hermetic per-run substitution tokens.
//
// Background. The original scenario isolation strategy used literal
// `/tmp/pig-parity-<NN>-sessions` paths in CLI args and `pre_clear_paths`.
// That had four failure modes observed in production runs:
//
//  1. Two concurrent runs of the same scenario (durability 3x, two
//     developers, `make parity` while a prior run was still finalizing)
//     race on the same `/tmp` path.
//  2. `pre_clear_paths` does `os.RemoveAll` on a literal path. If a
//     second run starts during the first run's tail, the second wipes
//     the first's live session-dir.
//  3. Scenarios that used a *relative* `--session-dir sessions` inside
//     a snapshotted CWD shared that subdir between the pig and pi
//     invocations within a single run, leaking state cross-side.
//  4. Snapshot machinery (snapshotEnvDirs) only covered env vars, not
//     CLI args. Hard-coded paths in `pig_args` / `pi_args` bypassed
//     all isolation.
//
// Fix. Scenarios declare their writable paths using these substitution
// tokens. The runner expands every token to a fresh per-binary, per-run
// `t.TempDir()` path before launching the binary, in CLI args, env
// values, and CWD. Tokens are NEVER shared between pig and pi. The
// tempdirs auto-clean via t.Cleanup.
//
// Token reference:
//
//   {{TEMP}}       : a fresh per-binary tempdir (root)
//   {{TEMP}}/<sub> : a subdir under the per-binary tempdir
//   {{FAKE_BIN}}   : absolute path to parity/testdata/fake-bin, a
//                     directory of no-op shims (open, xdg-open, start)
//                     used in scenarios that would otherwise launch a
//                     real browser via OAuth login. PATH-shadow these
//                     to keep tests hermetic.
//   {{PATH}}       : the parent process's PATH at test launch. Use
//                     this when the scenario needs to prepend FAKE_BIN
//                     without losing access to node / git / etc., e.g.
//                     `PATH={{FAKE_BIN}}:{{PATH}}`.
//
// No scenario should reference any literal /tmp path. The scenario lint
// (parity/cmd/lint) rejects literal /tmp in scenario fields.

// tokenContext holds the per-binary, per-run tempdir for substitution.
type tokenContext struct {
	tempRoot string
}

// newTokenContext allocates a fresh tempdir for one binary's run and
// registers it for automatic cleanup. Callers receive a distinct
// context per (binary, run), guaranteeing no cross-side or cross-run
// state visibility.
func newTokenContext(t *testing.T, label string) *tokenContext {
	t.Helper()
	dir, err := os.MkdirTemp("", fmt.Sprintf("parity-%s-*", label))
	if err != nil {
		t.Fatalf("parity: alloc tempdir for %s: %v", label, err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	})
	return &tokenContext{tempRoot: dir}
}

// expand performs token substitution on a single string. Returns the
// expanded value. Safe to call on strings with no tokens (returns input
// unchanged).
func (tc *tokenContext) expand(s string) string {
	if tc == nil {
		return s
	}
	if strings.Contains(s, "{{TEMP}}") {
		s = strings.ReplaceAll(s, "{{TEMP}}", tc.tempRoot)
	}
	if strings.Contains(s, "{{FAKE_BIN}}") {
		s = strings.ReplaceAll(s, "{{FAKE_BIN}}", fakeBinDir())
	}
	if strings.Contains(s, "{{PATH}}") {
		s = strings.ReplaceAll(s, "{{PATH}}", os.Getenv("PATH"))
	}
	return s
}

// expandSlice returns a new slice with every element expanded. The
// input is not mutated.
func (tc *tokenContext) expandSlice(in []string) []string {
	if tc == nil {
		return in
	}
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = tc.expand(v)
	}
	return out
}

// containsLiteralTmpPath reports whether any element looks like a
// literal /tmp path that bypasses the substitution mechanism. Used by
// the scenario lint to flag isolation regressions.
func containsLiteralTmpPath(parts []string) (string, bool) {
	for _, p := range parts {
		if strings.HasPrefix(p, "/tmp/") {
			return p, true
		}
		// Catch embedded /tmp/ in args like `--session-dir=/tmp/...`.
		if idx := strings.Index(p, "=/tmp/"); idx >= 0 {
			return p[idx+1:], true
		}
	}
	return "", false
}

// fakeBinDir resolves to the absolute path of parity/testdata/fake-bin
// relative to the parity/runner package source directory. The directory
// holds no-op shims (open, xdg-open, start) used by hermetic OAuth
// scenarios to suppress real browser launches.
//
// Resolution uses runtime.Caller so it works whether tests are run from
// the package dir or the repo root, in CI or locally.
func fakeBinDir() string {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "parity/testdata/fake-bin"
	}
	// thisFile = .../parity/runner/tokens.go
	return filepath.Join(filepath.Dir(filepath.Dir(thisFile)), "testdata", "fake-bin")
}

// shellQuote returns s safely quoted for inclusion in an inline shell
// command line. Preserves KEY=value env assignments by quoting only the
// value half; anything else is wrapped in single-quotes when it
// contains shell metacharacters.
//
// This exists because the tmux driver joins all parts into a single
// shell command line; user PATHs that include directories like
// "/Applications/Visual Studio Code.app/Contents/Resources/app/bin"
// would otherwise be word-split by the shell, breaking the launch.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if !needsShellQuote(s) {
		return s
	}
	// KEY=value form: quote only the value, leave KEY= bare so the
	// shell still treats it as an env assignment for the next command.
	if i := indexEnvAssign(s); i > 0 {
		return s[:i+1] + singleQuoteEscape(s[i+1:])
	}
	return singleQuoteEscape(s)
}

func needsShellQuote(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '_', r == '-', r == '.', r == '/', r == ':', r == ',', r == '=', r == '@', r == '+', r == '%':
			continue
		default:
			return true
		}
	}
	return false
}

func indexEnvAssign(s string) int {
	for i, r := range s {
		if r == '=' {
			if i == 0 {
				return -1
			}
			// Anything before '=' must look like a shell identifier
			// (letters, digits, underscore) for this to be an env
			// assignment.
			for _, c := range s[:i] {
				ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
				if !ok {
					return -1
				}
			}
			return i
		}
	}
	return -1
}

func singleQuoteEscape(s string) string {
	// Wrap in single-quotes and replace any embedded ' with '"'"'.
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('\'')
	for _, r := range s {
		if r == '\'' {
			b.WriteString(`'"'"'`)
			continue
		}
		b.WriteRune(r)
	}
	b.WriteByte('\'')
	return b.String()
}
