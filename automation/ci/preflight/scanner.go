// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// scanTimeout bounds one tool run.
const scanTimeout = 5 * time.Minute

var hunkHeaderRE = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

// numberedLine is a line a patch adds, with its line number at the head.
type numberedLine struct {
	n    int
	text string
}

// addedNumbered returns the lines a unified diff patch adds, with their head line numbers.
func addedNumbered(patch string) []numberedLine {
	var out []numberedLine
	line := 0
	for text := range strings.SplitSeq(patch, "\n") {
		if m := hunkHeaderRE.FindStringSubmatch(text); m != nil {
			line, _ = strconv.Atoi(m[1])
			continue
		}
		switch {
		case line == 0:
		case strings.HasPrefix(text, "+"):
			out = append(out, numberedLine{line, text[1:]})
			line++
		case strings.HasPrefix(text, "-"), strings.HasPrefix(text, `\`):
		default:
			line++
		}
	}
	return out
}

// selectedFiles are the PR's added and modified files a scan reads: all of them, or those its select matches.
func selectedFiles(d *data, s *scanSpec) []prFile {
	var out []prFile
	for _, f := range d.Files {
		if f.Status != "removed" && (s.selectRE == nil || s.selectRE.MatchString(f.Filename)) {
			out = append(out, f)
		}
	}
	return out
}

// scanInputHash is the SHA-256 of what a scan decides on: its definition and the name, blob and
// patch of every file it reads. An equal hash reuses the stored verdict.
func scanInputHash(c check, files []prFile) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%s\x00", c.definition)
	for _, f := range files {
		_, _ = fmt.Fprintf(h, "%s\x00%s\x00%d\x00%s\x00", f.Filename, f.SHA, len(f.Patch), f.Patch)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// workspace is where one PR's scan inputs live.
type workspace struct {
	dir   string // the run's work directory for this PR
	added string // added/<path>: the lines the PR adds
	head  string // head/: the PR's head, checked out sparsely
	tree  string // tree/: a repository holding only the changed files
	main  string // main's tree
	bin   string // main's built tools and the installed scanners
	// remote is the git host the PR's head is fetched from: https://github.com, or another host's URL.
	remote string
	// token authenticates the fetch of the head, through the environment, never on the command line.
	token string
	// data is the run's data for the PR as JSON, for a scan that reads it ({data}); self is this program ({self}).
	data, self string
}

// prepareWorkspace writes the inputs the given scans read: the added lines, a sparse checkout of the PR's head with
// the changed files and every checkout path the scans list, the overlays from main, and the tree of changed files.
func prepareWorkspace(ctx context.Context, d *data, scans []check, ws workspace, log io.Writer) error {
	need := map[string]bool{}
	var patterns, overlays []string
	for _, c := range scans {
		need[c.Scan.Input] = true
		if c.Scan.Input != inputAdded {
			need["head"] = true
		}
		for _, p := range c.Scan.Checkout {
			if p != "/*" {
				p = "/" + p
			}
			patterns = append(patterns, p)
		}
		overlays = append(overlays, c.Scan.Overlay...)
	}
	var changedFiles, whole []string
	for _, f := range d.Files {
		if f.Status == "removed" {
			continue
		}
		changedFiles = append(changedFiles, f.Filename)
		if !need[inputAdded] {
			continue
		}
		if f.whole() {
			whole = append(whole, f.Filename)
			need["head"] = true
			continue
		}
		target, err := inside(ws.added, f.Filename)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		lines := addedLines(f.Patch)
		if err := os.WriteFile(target, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			return err
		}
	}
	if !need["head"] {
		return nil
	}
	for _, f := range changedFiles {
		patterns = append(patterns, "/"+f)
	}
	slices.Sort(patterns)
	patterns = slices.Compact(patterns)
	start := time.Now()
	remote := strings.TrimSuffix(ws.remote, "/") + "/" + d.Repo
	ref := fmt.Sprintf("refs/pull/%d/head", d.PR)
	steps := [][]string{
		{"init", "-q", ws.head},
		{"-C", ws.head, "fetch", "-q", "--depth=1", "--filter=blob:none", remote, ref},
		append([]string{"-C", ws.head, "sparse-checkout", "set", "--no-cone"}, patterns...),
		{"-C", ws.head, "checkout", "-q", "--detach", "FETCH_HEAD"},
	}
	if slices.Contains(patterns, "/*") {
		// The whole tree: one fetch of every blob is faster than fetching them lazily at checkout.
		steps = [][]string{steps[0], {"-C", ws.head, "fetch", "-q", "--depth=1", remote, ref}, steps[3]}
	}
	for _, args := range steps {
		if out, err := gitAuth(ctx, ws.remote, ws.token, args...); err != nil {
			return fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, out)
		}
	}
	got, err := git(ctx, "-C", ws.head, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if got = strings.TrimSpace(got); got != d.Pull.Head.SHA {
		return fmt.Errorf("%s is at %s, the evaluated head is %s; the PR moved, the next run takes it", ref, short(got), short(d.Pull.Head.SHA))
	}
	logf(log, "head %s checked out (%d path(s), %s)", short(got), len(patterns), time.Since(start).Round(time.Millisecond))
	for _, rel := range slices.Compact(slices.Sorted(slices.Values(overlays))) {
		if err := overlay(ws.main, ws.head, rel); err != nil {
			return err
		}
	}
	if err := copyFromHead(ws.head, ws.added, whole); err != nil {
		return err
	}
	if need[inputTree] {
		if err := copyFromHead(ws.head, ws.tree, changedFiles); err != nil {
			return err
		}
		for _, args := range [][]string{{"-C", ws.tree, "init", "-q"}, {"-C", ws.tree, "add", "-A"}} {
			if out, err := git(ctx, args...); err != nil {
				return fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, out)
			}
		}
	}
	return nil
}

// overlay copies main's file over the head's, or removes the head's when main has none.
func overlay(mainRoot, head, rel string) error {
	target, err := inside(head, rel)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(filepath.Join(mainRoot, filepath.FromSlash(rel)))
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.WriteFile(target, raw, 0o600)
}

func git(ctx context.Context, args ...string) (string, error) {
	//nolint:gosec // G204: the program is literal git; the arguments are this run's directories, the PR's repository and ref, and paths of the PR.
	out, err := exec.CommandContext(ctx, "git", args...).CombinedOutput()
	return string(out), err
}

// mergeTree asks git whether the PR's head merges into its base branch without conflicts, for when GitHub has not
// computed mergeable yet. It fetches only the two commits' history (--filter=tree:0); git merge-tree fetches the
// trees and blobs it needs.
func mergeTree(ctx context.Context, ws workspace, d *data) (bool, error) {
	dir := filepath.Join(ws.dir, "merge")
	remote := strings.TrimSuffix(ws.remote, "/") + "/" + d.Repo
	steps := [][]string{
		{"init", "-q", dir},
		{"-C", dir, "remote", "add", "origin", remote},
		{"-C", dir, "fetch", "-q", "--filter=tree:0", "origin", fmt.Sprintf("+refs/pull/%d/head:refs/preflight/head", d.PR), "+refs/heads/" + d.Pull.Base.Ref + ":refs/preflight/base"},
	}
	if err := os.RemoveAll(dir); err != nil {
		return false, err
	}
	for _, args := range steps {
		if out, err := gitAuth(ctx, ws.remote, ws.token, args...); err != nil {
			return false, fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, out)
		}
	}
	got, err := git(ctx, "-C", dir, "rev-parse", "refs/preflight/head")
	if err != nil {
		return false, err
	}
	if got = strings.TrimSpace(got); got != d.Pull.Head.SHA {
		return false, fmt.Errorf("the PR's head is at %s, the evaluated head is %s", short(got), short(d.Pull.Head.SHA))
	}
	out, err := gitAuth(ctx, ws.remote, ws.token, "-C", dir, "merge-tree", "--write-tree", "--quiet", "refs/preflight/base", "refs/preflight/head")
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return true, nil
	case errors.As(err, &exitErr) && exitErr.ExitCode() == 1:
		return false, nil
	default:
		return false, fmt.Errorf("git merge-tree: %w\n%s", err, out)
	}
}

// gitAuth runs git with the token as an HTTP authorization header for remote's host only, passed in the environment
// (GIT_CONFIG_*), so it never shows on a command line or in an error and is never offered to another host. A remote
// that is not http(s) gets no header.
func gitAuth(ctx context.Context, remote, token string, args ...string) (string, error) {
	//nolint:gosec // G204: as git above.
	cmd := exec.CommandContext(ctx, "git", args...)
	if u, err := url.Parse(remote); err == nil && token != "" && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" {
		basic := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
		key := "http." + u.Scheme + "://" + u.Host + "/.extraHeader"
		cmd.Env = append(os.Environ(), "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0="+key, "GIT_CONFIG_VALUE_0=Authorization: Basic "+basic)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// inside joins name under root and refuses a name that would leave it.
func inside(root, name string) (string, error) {
	target := filepath.Join(root, filepath.FromSlash(name))
	if rel, err := filepath.Rel(root, target); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("file name %q leaves %s", name, root)
	}
	return target, nil
}

// finding is one thing a scan reported.
type finding struct {
	file    string
	line    int
	message string
}

func (f finding) String() string {
	switch {
	case f.file == "":
		return f.message
	case f.line > 0:
		return fmt.Sprintf("%s:%d %s", f.file, f.line, f.message)
	}
	return f.file + " " + f.message
}

// runScan runs one scan check and returns its outcome.
func runScan(ctx context.Context, c check, d *data, ws workspace, log io.Writer) outcome {
	s := c.Scan
	files := selectedFiles(d, s)
	if s.selectRE != nil && len(files) == 0 {
		return pass("no file it scans changed")
	}
	out := filepath.Join(ws.dir, c.Name+".report")
	input := map[string]string{inputAdded: ws.added, inputChanged: ws.head, inputTree: ws.tree}[s.Input]
	expand := func(arg string) string {
		return strings.NewReplacer("{main}", ws.main, "{bin}", ws.bin, "{head}", ws.head, "{input}", input, "{out}", out, "{data}", ws.data, "{self}", ws.self).Replace(arg)
	}
	var args []string
	for _, a := range s.Run {
		if a == "{files}" {
			for _, f := range files {
				args = append(args, f.Filename)
			}
			continue
		}
		args = append(args, expand(a))
	}
	program := args[0]
	if !strings.Contains(program, "/") {
		if p := filepath.Join(ws.bin, program); fileExists(p) {
			program = p
		}
	}
	runCtx, cancel := context.WithTimeout(ctx, scanTimeout)
	defer cancel()
	//nolint:gosec // G204: the program and arguments are config.yaml's on main; the PR supplies only file names, passed as arguments, never through a shell.
	cmd := exec.CommandContext(runCtx, program, args[1:]...)
	cmd.Dir = ws.dir
	if s.Dir != "" {
		cmd.Dir = expand(s.Dir)
	}
	cmd.Env = append(os.Environ(), "PATH="+ws.bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for k, v := range s.Env {
		cmd.Env = append(cmd.Env, k+"="+expand(v))
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	start := time.Now()
	err := cmd.Run()
	code := 0
	var exitErr *exec.ExitError
	switch {
	case errors.As(err, &exitErr):
		code = exitErr.ExitCode()
	case err != nil:
		return outcome{result: resultError, reason: fmt.Sprintf("%s could not run: %v", args[0], err)}
	}
	logf(log, "scan %s: %s exited %d after %s", c.Name, args[0], code, time.Since(start).Round(time.Millisecond))
	if !slices.Contains(s.Exit.Clean, code) && !slices.Contains(s.Exit.Findings, code) {
		return outcome{result: resultError, reason: fmt.Sprintf("%s exited %d: %s", args[0], code, firstLines(stderr.String()+stdout.String(), 3))}
	}
	report := stdout.Bytes()
	if s.Report.In == "out" {
		report, err = os.ReadFile(out)
		if errors.Is(err, os.ErrNotExist) {
			report, err = nil, nil
		}
		if err != nil {
			return outcome{result: resultError, reason: err.Error()}
		}
	}
	found, err := parseReport(s.Report, report)
	if err != nil {
		return outcome{result: resultError, reason: fmt.Sprintf("%s report: %v", args[0], err)}
	}
	roots := []string{ws.added, ws.head, ws.tree, input}
	for i := range found {
		found[i].file = relativeTo(found[i].file, roots)
	}
	if s.Input == inputAdded {
		toHeadLines(found, d)
	}
	if slices.Contains(s.Exit.Findings, code) && len(found) == 0 && s.Report.UnreadFindings != "pass" {
		return outcome{result: resultError, reason: fmt.Sprintf("%s exited %d for findings, but none could be read from its report: %s", args[0], code, firstLines(stderr.String()+stdout.String(), 3))}
	}
	read := len(found)
	if s.AddedLinesOnly {
		found = onAddedLines(found, d)
	}
	switch {
	case len(found) > 0:
		shown := found[:min(len(found), 5)]
		var parts []string
		for _, f := range shown {
			parts = append(parts, f.String())
		}
		more := ""
		if len(found) > len(shown) {
			more = fmt.Sprintf(" (and %d more)", len(found)-len(shown))
		}
		return fail("%d finding(s): %s%s", len(found), strings.Join(parts, " | "), more)
	case read > 0:
		return pass(fmt.Sprintf("%d finding(s), none on a line the PR adds", read))
	case slices.Contains(s.Exit.Findings, code):
		return pass(fmt.Sprintf("no finding (%s exited %d)", args[0], code))
	}
	return pass(fmt.Sprintf("clean (%d file(s))", len(files)))
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// relativeTo strips the workspace directory a tool reported a path under, so findings name the repository path.
func relativeTo(file string, roots []string) string {
	clean := filepath.Clean(file)
	for _, root := range roots {
		if root == "" {
			continue
		}
		if rel, err := filepath.Rel(root, clean); err == nil && !strings.HasPrefix(rel, "..") && filepath.IsAbs(clean) {
			return filepath.ToSlash(rel)
		}
	}
	return strings.TrimPrefix(filepath.ToSlash(clean), "./")
}

// copyFromHead copies files of the checked-out head into dir, at the same paths.
func copyFromHead(head, dir string, files []string) error {
	for _, f := range files {
		raw, err := os.ReadFile(filepath.Join(head, filepath.FromSlash(f)))
		if err != nil {
			return err
		}
		target, err := inside(dir, f)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, raw, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// toHeadLines turns the line numbers of findings in the added-lines files into the lines of the head they were taken
// from. A file read whole keeps its numbers.
func toHeadLines(found []finding, d *data) {
	added := map[string][]numberedLine{}
	for _, f := range d.Files {
		if !f.whole() {
			added[f.Filename] = addedNumbered(f.Patch)
		}
	}
	for i, f := range found {
		if lines, ok := added[f.file]; ok && f.line >= 1 && f.line <= len(lines) {
			found[i].line = lines[f.line-1].n
		}
	}
}

// onAddedLines keeps the findings on lines the PR adds and the findings that name no line.
func onAddedLines(found []finding, d *data) []finding {
	added, whole := map[string]bool{}, map[string]bool{}
	for _, f := range d.Files {
		whole[f.Filename] = f.whole()
		for _, l := range addedNumbered(f.Patch) {
			added[fmt.Sprintf("%s:%d", f.Filename, l.n)] = true
		}
	}
	var keep []finding
	for _, f := range found {
		if f.line == 0 || whole[f.file] || added[fmt.Sprintf("%s:%d", f.file, f.line)] {
			keep = append(keep, f)
		}
	}
	return keep
}

// parseReport reads the findings from a tool's report as its report spec describes.
func parseReport(r reportSpec, report []byte) ([]finding, error) {
	var found []finding
	switch r.Format {
	case "lines":
		sc := bufio.NewScanner(bytes.NewReader(report))
		sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
		for sc.Scan() {
			line := sc.Text()
			if r.ignore != nil && r.ignore.MatchString(line) {
				continue
			}
			m := r.pattern.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			var f finding
			for i, name := range r.pattern.SubexpNames() {
				switch name {
				case "file":
					f.file = m[i]
				case "line":
					f.line, _ = strconv.Atoi(m[i])
				case "message":
					f.message = strings.TrimSpace(m[i])
				}
			}
			found = append(found, f)
		}
		return found, sc.Err()
	case "json-lines":
		sc := bufio.NewScanner(bytes.NewReader(report))
		sc.Buffer(make([]byte, 0, 64<<10), 16<<20)
		for sc.Scan() {
			if strings.TrimSpace(sc.Text()) == "" {
				continue
			}
			var v any
			if err := json.Unmarshal(sc.Bytes(), &v); err != nil {
				return nil, err
			}
			found = append(found, jsonFindings(r, v)...)
		}
		return found, sc.Err()
	default:
		if len(bytes.TrimSpace(report)) == 0 {
			return nil, nil
		}
		var v any
		if err := json.Unmarshal(report, &v); err != nil {
			return nil, err
		}
		return jsonFindings(r, v), nil
	}
}

func jsonFindings(r reportSpec, v any) []finding {
	items := []any{v}
	if r.List != "" {
		items = jsonPath(v, r.List)
	} else if list, ok := v.([]any); ok {
		items = list
	}
	var found []finding
	for _, item := range items {
		f := finding{message: jsonString(item, r.Message)}
		if r.File != "" {
			f.file = jsonString(item, r.File)
		}
		if r.Line != "" {
			f.line, _ = strconv.Atoi(jsonString(item, r.Line))
		}
		if f.message != "" || f.file != "" {
			found = append(found, f)
		}
	}
	return found
}

// jsonPath walks a dot path; a segment ending in [] fans out over an array.
func jsonPath(v any, p string) []any {
	current := []any{v}
	for seg := range strings.SplitSeq(p, ".") {
		fan := strings.HasSuffix(seg, "[]")
		seg = strings.TrimSuffix(seg, "[]")
		var next []any
		for _, c := range current {
			if seg != "" {
				m, ok := c.(map[string]any)
				if !ok {
					continue
				}
				c = m[seg]
			}
			if fan {
				if list, ok := c.([]any); ok {
					next = append(next, list...)
				}
				continue
			}
			if c != nil {
				next = append(next, c)
			}
		}
		current = next
	}
	return current
}

func jsonString(v any, p string) string {
	values := jsonPath(v, p)
	if len(values) == 0 {
		return ""
	}
	switch x := values[0].(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	default:
		raw, _ := json.Marshal(x)
		return string(raw)
	}
}

func firstLines(text string, n int) string {
	var keep []string
	for line := range strings.SplitSeq(strings.TrimSpace(text), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			keep = append(keep, line)
		}
		if len(keep) == n {
			break
		}
	}
	return strings.Join(keep, " | ")
}

// runScans prepares the workspace and runs the scans that are still to decide, in parallel, writing each outcome
// into the evaluation.
func runScans(ctx context.Context, cfg config, d *data, ev *evaluation, ws workspace, log io.Writer) error {
	var scans []check
	for _, c := range cfg.Checks {
		if slices.Contains(ev.Scans, c.Name) {
			scans = append(scans, c)
		}
	}
	if len(scans) == 0 {
		return nil
	}
	ws.added = filepath.Join(ws.dir, "added")
	ws.head = filepath.Join(ws.dir, "head")
	ws.tree = filepath.Join(ws.dir, "tree")
	if err := os.MkdirAll(ws.dir, 0o755); err != nil {
		return err
	}
	if err := prepareWorkspace(ctx, d, scans, ws, log); err != nil {
		return err
	}
	ws.data = filepath.Join(ws.dir, "data.json")
	if err := writeJSON(ws.data, d); err != nil {
		return err
	}
	if self, err := os.Executable(); err == nil {
		ws.self = self
	}
	results := make([]outcome, len(scans))
	var wg sync.WaitGroup
	for i, c := range scans {
		wg.Go(func() { results[i] = runScan(ctx, c, d, ws, log) })
	}
	wg.Wait()
	for i, c := range scans {
		v := ev.Checks[c.Name]
		v.Result, v.Reason = results[i].result, results[i].reason
		ev.Checks[c.Name] = v
	}
	ev.finish()
	return nil
}
