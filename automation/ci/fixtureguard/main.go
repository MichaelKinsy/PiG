// SPDX-License-Identifier: MIT

// Command fixtureguard refuses test fixtures that look like verbatim captures of provider traffic. A captured stream carries model reasoning text, encrypted reasoning blobs, billing rows, real request and response ids, and the capturing machine's paths; none of it belongs in the public tree, and a fixture only needs the minimal synthetic shape its test reads.
//
// It scans every tracked file plus untracked files git does not ignore, so a new fixture is checked before it is committed. Findings print as path:line with a rule code and a size; the matched content is never echoed.
//
//	FG001  opaque encrypted or signature value, or a large base64 blob
//	FG002  provider billing or quota row, or a request-id header with a real-looking value
//	FG003  real-looking provider id or created timestamp (fixture files only)
//	FG004  absolute /Users/<name> or /home/<name> path of a non-placeholder account (fixture files only)
//	FG005  natural-language reasoning text accumulated across stream or session fields
//	FG006  a term from the private denylist
//
// Reviewed false positives live in allowlist.go with a reason; an entry that no longer matches fails.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode"
)

const (
	privateTermsEnv = "PIG_PRIVATE_TERMS_FILE"
	maxFileSize     = 64 << 20
	minTermLength   = 3
)

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Usage = func() {
		_, _ = fmt.Fprintf(flag.CommandLine.Output(), `Usage: fixtureguard [-root dir]

Refuses test fixtures that look like verbatim provider captures: encrypted or
signature blobs, billing rows, real provider ids and timestamps, reasoning text,
and absolute home paths. Scans tracked files and untracked files git does not
ignore. Exit 0 clean, 1 findings, 2 usage or I/O error.

Environment:
  %s  path to a file OUTSIDE the repository with one private term per
      line (at least %d characters, matched case-insensitively against every
      scanned file's content and path, binary files, vendored trees and this
      tool's own sources included, and against the text of reassembled stream
      deltas). Blank lines are skipped; a leading UTF-8 BOM is ignored. A match
      prints only file:line and the term's line number in that file, never the
      term. Unset, the denylist is skipped.

Run it before pushing: make fixture-guard.
`, privateTermsEnv, minTermLength)
	}
	flag.Parse()
	os.Exit(run(*root, os.Getenv(privateTermsEnv), os.Stdout, os.Stderr))
}

func run(root, termsFile string, stdout, stderr io.Writer) int {
	terms, err := loadTerms(root, termsFile)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "fixture-guard:", err)
		return 2
	}
	files, err := listFiles(root)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "fixture-guard:", err)
		return 2
	}
	findings, scanned, err := scanTree(root, files, terms)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "fixture-guard:", err)
		return 2
	}
	findings, stale := applyAllowlist(findings)
	for _, f := range findings {
		_, _ = fmt.Fprintf(stdout, "%s:%d: %s: %s\n", redactPath(f.path, terms), f.line, f.code, f.message)
	}
	for _, s := range stale {
		_, _ = fmt.Fprintf(stdout, "allowlist.go: stale entry %s %s matches no finding; delete it\n", redactPath(s.path, terms), s.code)
	}
	if len(findings) > 0 || len(stale) > 0 {
		_, _ = fmt.Fprintln(stdout, "fixture-guard: replace the captured content with a minimal synthetic shape generated in the test; do not commit the capture")
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "fixture-guard: clean (%d files)\n", scanned)
	return 0
}

// term is one denylist entry; line is its 1-based line in the terms file.
type term struct {
	line  int
	lower []byte
	runes []rune
}

var utf8BOM = []byte{0xef, 0xbb, 0xbf}

func loadTerms(root, path string) ([]term, error) {
	if path == "" {
		return nil, nil
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", privateTermsEnv, unwrapPathError(err))
	}
	repo, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("resolve repository root: %w", unwrapPathError(err))
	}
	if resolved, err = filepath.Abs(resolved); err != nil {
		return nil, err
	}
	if repo, err = filepath.Abs(repo); err != nil {
		return nil, err
	}
	if relative, err := filepath.Rel(repo, resolved); err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("%s must name a file outside the repository", privateTermsEnv)
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", privateTermsEnv, unwrapPathError(err))
	}
	data = bytes.TrimPrefix(data, utf8BOM)
	var terms []term
	for i, raw := range strings.Split(string(data), "\n") {
		text := strings.TrimSpace(raw)
		if text == "" {
			continue
		}
		if len([]rune(text)) < minTermLength {
			return nil, fmt.Errorf("%s line %d is shorter than %d characters", privateTermsEnv, i+1, minTermLength)
		}
		lower := bytes.ToLower([]byte(text))
		terms = append(terms, term{line: i + 1, lower: lower, runes: []rune(string(lower))})
	}
	return terms, nil
}

// unwrapPathError drops the path an I/O error carries: a path may name a private term.
func unwrapPathError(err error) error {
	if pathErr, ok := errors.AsType[*fs.PathError](err); ok {
		return pathErr.Err
	}
	return err
}

// redactPath masks any term occurrence in a printed path so a file named after a term does not reveal it. It works on runes, because lower-casing can change a character's byte length, and falls back to a placeholder if a term still shows after masking.
func redactPath(path string, terms []term) string {
	if len(terms) == 0 {
		return path
	}
	original := []rune(path)
	lower := make([]rune, len(original))
	for i, c := range original {
		lower[i] = unicode.ToLower(c)
	}
	var out []rune
	masked := false
	for i := 0; i < len(original); {
		matched := false
		for _, t := range terms {
			if n := len(t.runes); n > 0 && i+n <= len(lower) && slices.Equal(lower[i:i+n], t.runes) {
				out = append(out, []rune(fmt.Sprintf("<term-%d>", t.line))...)
				i += n
				matched, masked = true, true
				break
			}
		}
		if !matched {
			out = append(out, original[i])
			i++
		}
	}
	if !masked {
		return path
	}
	result := string(out)
	lowerResult := bytes.ToLower([]byte(result))
	for _, t := range terms {
		if bytes.Contains(lowerResult, t.lower) {
			return "<path-with-private-term>"
		}
	}
	return result
}

// listFiles returns tracked files plus untracked files git does not ignore, so a fixture is checked before it is committed.
func listFiles(root string) ([]string, error) {
	cmd := exec.Command("git", "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	var files []string
	for name := range strings.SplitSeq(string(out), "\x00") {
		if name != "" {
			files = append(files, name)
		}
	}
	slices.Sort(files)
	return slices.Compact(files), nil
}

// vendored reports third-party code, upstream mirrors and this tool's own sources, which are not fixtures. The private-term check still covers them.
func vendored(path string) bool {
	// The Node runtime shims embed upstream dist code and WASM payloads; this tool's own sources hold the detector patterns it scans for.
	for _, segment := range []string{"third_party/", ".upstream/", "node_modules/", "vendor/", "runtime-node/shims/", "automation/ci/fixtureguard/"} {
		if strings.HasPrefix(path, segment) || strings.Contains(path, "/"+segment) {
			return true
		}
	}
	return false
}

var fixtureDir = regexp.MustCompile(`(^|/)(testdata|fixtures?|__fixtures__)/`)

var dataExtensions = map[string]bool{".json": true, ".jsonl": true, ".ndjson": true, ".sse": true, ".txt": true, ".log": true, ".toml": true, ".yaml": true, ".yml": true, ".har": true, ".http": true}

// isFixture reports files that hold test data: anything under a testdata or fixtures directory, and data files under test/. A Go test file is not a fixture; it still gets the capture detectors that do not misfire on prose.
func isFixture(path string) bool {
	if fixtureDir.MatchString(path) {
		return !strings.HasSuffix(path, ".go") || strings.Contains(path, "/testdata/")
	}
	return strings.HasPrefix(path, "test/") && dataExtensions[strings.ToLower(filepath.Ext(path))]
}

// scanTree scans every listed path. The private-term check covers every name and every regular file's bytes, binary and vendored included; the structural rules cover non-vendored text files. It returns the findings and the count of files whose content was read.
func scanTree(root string, files []string, terms []term) ([]finding, int, error) {
	var findings []finding
	scanned := 0
	for _, name := range files {
		findings = append(findings, scanPathTerms(name, terms)...)
		data, ok, err := readFile(filepath.Join(root, name))
		if err != nil {
			return nil, 0, fmt.Errorf("read %s: %w", redactPath(name, terms), unwrapPathError(err))
		}
		if !ok {
			continue
		}
		scanned++
		findings = append(findings, scanTerms(name, data, terms)...)
		if vendored(name) || isBinary(data) {
			continue
		}
		findings = append(findings, scanText(name, data, isFixture(name))...)
	}
	return findings, scanned, nil
}

// readFile returns the file's bytes when it is a regular file of bounded size. A path git lists but the working tree lacks, a symlink or a submodule directory is skipped.
func readFile(path string) ([]byte, bool, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxFileSize {
		return nil, false, nil //nolint:nilerr // git ls-files can list a path deleted from the working tree; there is nothing to scan.
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func isBinary(data []byte) bool {
	probe := data
	if len(probe) > 8192 {
		probe = probe[:8192]
	}
	return bytes.IndexByte(probe, 0) >= 0
}

func scanPathTerms(path string, terms []term) []finding {
	if len(terms) == 0 {
		return nil
	}
	lower := bytes.ToLower([]byte(path))
	var found []finding
	for _, t := range terms {
		if bytes.Contains(lower, t.lower) {
			found = append(found, finding{path, 0, codePrivate, fmt.Sprintf("path matches private term at terms-file line %d", t.line)})
		}
	}
	return found
}

// scanTerms reports each term occurrence in a file's bytes, and in its reassembled stream text, as path:line with the term's line number in the terms file; neither the term nor the matched line is printed.
func scanTerms(path string, data []byte, terms []term) []finding {
	if len(terms) == 0 {
		return nil
	}
	var found []finding
	lowerData := bytes.ToLower(data)
	var newlines []int
	lineAt := func(off int) int {
		if newlines == nil {
			newlines = []int{}
			for i, c := range lowerData {
				if c == '\n' {
					newlines = append(newlines, i)
				}
			}
		}
		return 1 + sort.SearchInts(newlines, off)
	}
	each := func(haystack []byte, line func(int) int) {
		for _, t := range terms {
			for offset := 0; ; {
				i := bytes.Index(haystack[offset:], t.lower)
				if i < 0 {
					break
				}
				at := offset + i
				found = append(found, finding{path, line(at), codePrivate, fmt.Sprintf("private term at terms-file line %d", t.line)})
				offset = at + len(t.lower)
			}
		}
	}
	each(lowerData, lineAt)
	for _, s := range reassemble(path, data) {
		each(bytes.ToLower(s.buf), s.lineAt)
	}
	return dedupe(found)
}

// applyAllowlist drops reviewed false positives and reports every allowlist entry that matched nothing, including one whose file was deleted or renamed.
func applyAllowlist(findings []finding) ([]finding, []allowed) {
	used := make([]bool, len(allowlist))
	var kept []finding
	for _, f := range findings {
		suppressed := false
		if f.code != codePrivate {
			for i, a := range allowlist {
				if a.path == f.path && a.code == f.code {
					used[i] = true
					suppressed = true
				}
			}
		}
		if !suppressed {
			kept = append(kept, f)
		}
	}
	var stale []allowed
	for i, a := range allowlist {
		if !used[i] {
			stale = append(stale, a)
		}
	}
	return kept, stale
}
