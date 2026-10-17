package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// coEdit is one commit in which a test file's expectation line changed while a
// production file of the same directory changed too.
type coEdit struct {
	Commit string
	// Authored is the author date, RFC 3339.
	Authored string
	Subject  string
	Path     string
	Edited   int
	// ValueEdits counts expected literals that left the file in the commit and
	// were replaced by others, which separates a changed expectation from a
	// renamed identifier.
	ValueEdits int
	// Cited lists the TypeScript file names cited with a line (name.ts:line) in lines the commit added to the test file.
	Cited []string
	Code  []string
}

var (
	hunkRE = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+\d+(?:,\d+)? @@`)
	// An expectation line holds a literal and a comparison or an expectation name.
	expectRE  = regexp.MustCompile(`(?i)(?:\bwant\w*\b|\bexpect\w*\b|\bgot\b|!=|==|Equal|\bgolden\b|Contains)`)
	literalRE = regexp.MustCompile("(?:\"[^\"]{2,}\"|`[^`]{2,}`|\\b\\d{2,}\\b)")
)

// piLineRE matches a citation of a TypeScript source line, such as resource-loader.ts:610-612 or agent-loop.test.ts:12;
// its group is the file name.
var piLineRE = regexp.MustCompile(`([A-Za-z0-9_.-]+\.ts):\d+`)

// citedNames returns the file names a line cites with a line number.
func citedNames(line string) []string {
	var names []string
	for _, m := range piLineRE.FindAllStringSubmatch(line, -1) {
		names = append(names, strings.TrimLeft(m[1], "."))
	}
	return names
}

// pathLike reports a quoted literal that names a file or package path: moving code changes these without changing behavior.
func pathLike(literal string) bool {
	body := strings.Trim(literal, "\"`")
	return strings.Contains(body, "/") && !strings.ContainsAny(body, " \t\n")
}

func isExpectation(line string) bool {
	return expectRE.MatchString(line) && literalRE.MatchString(line)
}

func isProdGo(p string) bool {
	return strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") && !strings.Contains(p, "/testdata/")
}

// scanCoEdits streams `git log -p` for range spec and reports every co-edited
// expectation. A line pair counts when a removed and an added line both are
// expectations: a pure addition covers new behavior, a pure removal removes it.
func scanCoEdits(root string, logArgs []string) ([]coEdit, error) {
	args := append([]string{"-C", root, "log", "--no-merges", "-U0", "-p", "--format=@@COMMIT %H %aI %s"}, logArgs...)
	cmd := exec.Command("git", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	r := bufio.NewReaderSize(stdout, 1<<20)
	var out []coEdit
	var commit, authored, subject string
	type fileState struct {
		removed, added int
		gone, came     map[string]int
		cited          []string
	}
	files := map[string]*fileState{}
	var order []string
	var cur *fileState
	flush := func() {
		if commit == "" {
			return
		}
		var prod []string
		for _, f := range order {
			if isProdGo(f) {
				prod = append(prod, f)
			}
		}
		for _, f := range order {
			st := files[f]
			if !strings.HasSuffix(f, "_test.go") || st.removed == 0 || st.added == 0 {
				continue
			}
			var same []string
			for _, p := range prod {
				if path.Dir(p) == path.Dir(f) {
					same = append(same, p)
				}
			}
			if len(same) == 0 {
				continue
			}
			edited := min(st.removed, st.added)
			out = append(out, coEdit{Commit: commit, Authored: authored, Subject: subject, Path: f, Edited: edited, ValueEdits: valueEdits(st.gone, st.came), Cited: st.cited, Code: same})
		}
		files = map[string]*fileState{}
		order = nil
		cur = nil
	}
	for {
		line, err := r.ReadString('\n')
		if line != "" {
			line = strings.TrimRight(line, "\r\n")
			switch {
			case strings.HasPrefix(line, "@@COMMIT "):
				flush()
				rest := strings.TrimPrefix(line, "@@COMMIT ")
				var fields string
				commit, fields, _ = strings.Cut(rest, " ")
				authored, subject, _ = strings.Cut(fields, " ")
			case strings.HasPrefix(line, "+++ b/"):
				name := strings.TrimPrefix(line, "+++ b/")
				cur = &fileState{gone: map[string]int{}, came: map[string]int{}}
				files[name] = cur
				order = append(order, name)
			case strings.HasPrefix(line, "--- "), strings.HasPrefix(line, "diff --git"), strings.HasPrefix(line, "+++ "):
				if strings.HasPrefix(line, "+++ /dev/null") {
					cur = nil
				}
			case hunkRE.MatchString(line):
			case cur != nil && strings.HasPrefix(line, "+") && piLineRE.MatchString(line[1:]):
				cur.cited = append(cur.cited, citedNames(line[1:])...)
			case cur != nil && strings.HasPrefix(line, "-") && isExpectation(line[1:]):
				cur.removed++
				for _, l := range literalRE.FindAllString(line[1:], -1) {
					if !pathLike(l) {
						cur.gone[l]++
					}
				}
			case cur != nil && strings.HasPrefix(line, "+") && isExpectation(line[1:]):
				cur.cited = append(cur.cited, citedNames(line[1:])...)
				cur.added++
				for _, l := range literalRE.FindAllString(line[1:], -1) {
					if !pathLike(l) {
						cur.came[l]++
					}
				}
			}
		}
		if err != nil {
			if err != io.EOF {
				return nil, err
			}
			break
		}
	}
	flush()
	if err := cmd.Wait(); err != nil {
		return nil, err
	}
	return out, nil
}

func runHistory(root string, args []string) error {
	fs := flag.NewFlagSet("history", flag.ExitOnError)
	outDir := fs.String("out", "tmp/test-prune", "output directory")
	rev := fs.String("rev", "HEAD", "history tip")
	if err := fs.Parse(args); err != nil {
		return err
	}
	edits, err := scanCoEdits(root, []string{*rev, "--", "*_test.go", "*.go"})
	if err != nil {
		return err
	}
	type agg struct {
		commits, lines int
	}
	perFile := map[string]*agg{}
	for _, e := range edits {
		a := perFile[e.Path]
		if a == nil {
			a = &agg{}
			perFile[e.Path] = a
		}
		if e.ValueEdits == 0 {
			continue
		}
		a.commits++
		a.lines += e.ValueEdits
	}
	files := make([]string, 0, len(perFile))
	for f := range perFile {
		if _, err := os.Stat(filepath.Join(root, f)); err == nil {
			files = append(files, f)
		}
	}
	sort.Slice(files, func(i, j int) bool { return perFile[files[i]].lines > perFile[files[j]].lines })
	var b strings.Builder
	b.WriteString("path\tco_edit_commits\tchanged_expected_values\n")
	for _, f := range files {
		fmt.Fprintf(&b, "%s\t%d\t%d\n", f, perFile[f].commits, perFile[f].lines)
	}
	dir := filepath.Join(root, *outDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "coedits.tsv"), []byte(b.String()), 0o644); err != nil {
		return err
	}
	fmt.Printf("%d test files changed an expected value in a commit that also changed production code of their directory\n", len(files))
	return nil
}

// valueEdits counts literals that were removed and not re-added, bounded by the
// literals that were added and not removed.
func valueEdits(gone, came map[string]int) int {
	var g, c int
	for l, n := range gone {
		if came[l] < n {
			g += n - came[l]
		}
	}
	for l, n := range came {
		if gone[l] < n {
			c += n - gone[l]
		}
	}
	return min(g, c)
}
