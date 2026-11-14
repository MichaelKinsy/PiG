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
	"io/fs"
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

const scanTimeout = 5 * time.Minute

var hunkHeaderRE = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

type numberedLine struct {
	n    int
	text string
}

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

func selectedFiles(d *data, s *scanSpec) []prFile {
	var out []prFile
	for _, f := range d.Files {
		if f.Status != "removed" && (s.selectRE == nil || s.selectRE.MatchString(f.Filename)) {
			out = append(out, f)
		}
	}
	return out
}

func scanInputHash(c check, files []prFile) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%s\x00", c.definition)
	for _, f := range files {
		_, _ = fmt.Fprintf(h, "%s\x00%s\x00%d\x00%s\x00", f.Filename, f.SHA, len(f.Patch), f.Patch)
	}
	return hex.EncodeToString(h.Sum(nil))
}

type workspace struct {
	dir        string
	added      string
	head       string
	tree       string
	main       string
	bin        string
	remote     string
	token      string
	data, self string
}

func prepareWorkspace(ctx context.Context, d *data, scans []check, ws workspace, log io.Writer) error {
	need, patterns, overlays := scanNeeds(scans)
	changed, whole, err := writeAdded(d, ws, need[inputAdded])
	if err != nil {
		return err
	}
	if !need["head"] && len(whole) == 0 {
		return nil
	}
	for _, f := range changed {
		p, err := sparsePattern(f)
		if err != nil {
			return err
		}
		patterns = append(patterns, p)
	}
	if err := checkoutHead(ctx, d, ws, patterns, log); err != nil {
		return err
	}
	for _, rel := range slices.Compact(slices.Sorted(slices.Values(overlays))) {
		if err := overlay(ws.main, ws.head, rel); err != nil {
			return err
		}
	}
	if err := copyFromHead(ws.head, ws.added, whole); err != nil {
		return err
	}
	if !need[inputTree] {
		return nil
	}
	if err := copyFromHead(ws.head, ws.tree, changed); err != nil {
		return err
	}
	return runGit(ctx, "", "", [][]string{{"-C", ws.tree, "init", "-q"}, {"-C", ws.tree, "add", "-A"}})
}

func scanNeeds(scans []check) (need map[string]bool, patterns, overlays []string) {
	need = map[string]bool{}
	for _, c := range scans {
		need[c.Scan.Input] = true
		need["head"] = need["head"] || c.Scan.Input != inputAdded
		for _, p := range c.Scan.Checkout {
			if p != "/*" {
				p = "/" + p
			}
			patterns = append(patterns, p)
		}
		overlays = append(overlays, c.Scan.Overlay...)
	}
	return need, patterns, overlays
}

// sparsePattern is the non-cone sparse-checkout pattern that matches the one path name and nothing else. git reads a
// pattern by gitignore's rules: the leading "/" anchors it at the root and keeps a "!" or "#" from opening the line, and
// a backslash takes the meaning from "*", "?", "[" and a backslash and keeps a trailing space. A line break ends a
// pattern, so a name with one has no pattern.
func sparsePattern(name string) (string, error) {
	if strings.ContainsAny(name, "\n\r") {
		return "", fmt.Errorf("file name %q has a line break, which no sparse-checkout pattern can name", name)
	}
	kept := len(strings.TrimRight(name, " "))
	var b strings.Builder
	b.WriteByte('/')
	for i := range len(name) {
		if c := name[i]; c == '*' || c == '?' || c == '[' || c == '\\' || (c == ' ' && i >= kept) {
			b.WriteByte('\\')
		}
		b.WriteByte(name[i])
	}
	return b.String(), nil
}

func writeAdded(d *data, ws workspace, added bool) (changed, whole []string, err error) {
	for _, f := range d.Files {
		if f.Status == "removed" {
			continue
		}
		changed = append(changed, f.Filename)
		switch {
		case !added:
		case f.whole():
			whole = append(whole, f.Filename)
		default:
			if err := writeFileUnder(ws.added, f.Filename, []byte(strings.Join(addedLines(f.Patch), "\n")+"\n")); err != nil {
				return nil, nil, err
			}
		}
	}
	return changed, whole, nil
}

func writeFileUnder(dir, name string, content []byte) error {
	target, err := inside(dir, name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.WriteFile(target, content, 0o600)
}

func checkoutHead(ctx context.Context, d *data, ws workspace, patterns []string, log io.Writer) error {
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
		steps = [][]string{steps[0], {"-C", ws.head, "fetch", "-q", "--depth=1", remote, ref}, steps[3]}
	}
	if err := runGit(ctx, ws.remote, ws.token, steps); err != nil {
		return err
	}
	got, err := git(ctx, "-C", ws.head, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if got = strings.TrimSpace(got); got != d.Pull.Head.SHA {
		return fmt.Errorf("%s is at %s, the evaluated head is %s; the PR moved, the next run takes it", ref, short(got), short(d.Pull.Head.SHA))
	}
	logf(log, "head %s checked out (%d path(s), %s)", short(got), len(patterns), time.Since(start).Round(time.Millisecond))
	return flattenSymlinks(ws.head)
}

func runGit(ctx context.Context, remote, token string, steps [][]string) error {
	for _, args := range steps {
		if out, err := gitAuth(ctx, remote, token, args...); err != nil {
			return fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, out)
		}
	}
	return nil
}

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
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = childEnv()
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// childEnv is the environment of every program the pre-flight starts: its own without the variables whose name ends in
// _TOKEN. Those are the token the run itself holds (GITHUB_TOKEN, GH_TOKEN) and the job's other credentials
// (ACTIONS_RUNTIME_TOKEN, ACTIONS_ID_TOKEN_REQUEST_TOKEN). The programs read what a PR wrote, and none of them needs one.
func childEnv() []string {
	return slices.DeleteFunc(os.Environ(), func(kv string) bool {
		name, _, _ := strings.Cut(kv, "=")
		return strings.HasSuffix(strings.ToUpper(name), "_TOKEN")
	})
}

// mergeRepo fetches the commits of the PR's head and of its base branch without their trees and blobs; git reads from the
// remote the ones a merge needs.
func mergeRepo(ctx context.Context, ws workspace, d *data) (string, error) {
	dir := filepath.Join(ws.dir, "merge")
	remote := strings.TrimSuffix(ws.remote, "/") + "/" + d.Repo
	steps := [][]string{
		{"init", "-q", dir},
		{"-C", dir, "remote", "add", "origin", remote},
		{"-C", dir, "fetch", "-q", "--filter=tree:0", "origin", fmt.Sprintf("+refs/pull/%d/head:refs/preflight/head", d.PR), "+refs/heads/" + d.Pull.Base.Ref + ":refs/preflight/base"},
	}
	if err := os.RemoveAll(dir); err != nil {
		return "", err
	}
	if err := runGit(ctx, ws.remote, ws.token, steps); err != nil {
		return "", err
	}
	got, err := git(ctx, "-C", dir, "rev-parse", "refs/preflight/head")
	if err != nil {
		return "", err
	}
	if got = strings.TrimSpace(got); got != d.Pull.Head.SHA {
		return "", fmt.Errorf("the PR's head is at %s, the evaluated head is %s", short(got), short(d.Pull.Head.SHA))
	}
	return dir, nil
}

func mergeable(ctx context.Context, ws workspace, dir string) (bool, error) {
	_, clean, err := mergeTreeOf(ctx, ws, dir, "refs/preflight/base", "refs/preflight/head")
	return clean, err
}

var treeIDRE = regexp.MustCompile(`(?m)^[0-9a-f]{40}(?:[0-9a-f]{24})?$`)

// mergeTreeOf is git's merge of two commits of mergeRepo's repository: the tree it writes, or clean false when the merge
// conflicts.
func mergeTreeOf(ctx context.Context, ws workspace, dir, ours, theirs string) (tree string, clean bool, err error) {
	// No --quiet: git learned it in 2.50 and an older one exits 129 on it. The exit status says the same without it.
	out, err := gitAuth(ctx, ws.remote, ws.token, "-C", dir, "merge-tree", "--write-tree", ours, theirs)
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		// The output is git's stdout and stderr: the tree is the one line that is an object id.
		if tree = treeIDRE.FindString(out); tree == "" {
			return "", false, fmt.Errorf("git merge-tree printed no tree:\n%s", out)
		}
		return tree, true, nil
	case errors.As(err, &exitErr) && exitErr.ExitCode() == 1:
		return "", false, nil
	default:
		return "", false, fmt.Errorf("git merge-tree: %w\n%s", err, out)
	}
}

// The token goes in the environment, never on a command line, and only as the header git sends to the remote's host.
func gitAuth(ctx context.Context, remote, token string, args ...string) (string, error) {
	//nolint:gosec // G204: as git above.
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = childEnv()
	if u, err := url.Parse(remote); err == nil && token != "" && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" {
		basic := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
		key := "http." + u.Scheme + "://" + u.Host + "/.extraHeader"
		cmd.Env = append(cmd.Env, "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0="+key, "GIT_CONFIG_VALUE_0=Authorization: Basic "+basic)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func inside(root, name string) (string, error) {
	target := filepath.Join(root, filepath.FromSlash(name))
	if rel, err := filepath.Rel(root, target); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("file name %q leaves %s", name, root)
	}
	return target, nil
}

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

func runScan(ctx context.Context, c check, d *data, ws workspace, log io.Writer) outcome {
	s := c.Scan
	files := selectedFiles(d, s)
	if s.selectRE != nil && len(files) == 0 {
		return pass("no file it scans changed")
	}
	input := map[string]string{inputAdded: ws.added, inputChanged: ws.head, inputTree: ws.tree}[s.Input]
	out := filepath.Join(ws.dir, c.Name+".report")
	runCtx, cancel := context.WithTimeout(ctx, scanTimeout)
	defer cancel()
	cmd, name := scanCommand(runCtx, s, files, ws, input, out)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	start := time.Now()
	code, err := exitCode(cmd.Run())
	if err != nil {
		return outcome{result: resultError, reason: fmt.Sprintf("%s could not run: %v", name, err)}
	}
	logf(log, "scan %s: %s exited %d after %s", c.Name, name, code, time.Since(start).Round(time.Millisecond))
	said := firstLines(stderr.String()+stdout.String(), 3)
	findings := slices.Contains(s.Exit.Findings, code)
	if !findings && !slices.Contains(s.Exit.Clean, code) {
		return outcome{result: resultError, reason: fmt.Sprintf("%s exited %d: %s", name, code, said)}
	}
	found, err := readFindings(s.Report, out, stdout.Bytes())
	if err != nil {
		return outcome{result: resultError, reason: fmt.Sprintf("%s report: %v", name, err)}
	}
	for i := range found {
		found[i].file = relativeTo(found[i].file, []string{ws.added, ws.head, ws.tree, input})
	}
	if s.Input == inputAdded {
		toHeadLines(found, d)
	}
	if findings && len(found) == 0 && s.Report.UnreadFindings != "pass" {
		return outcome{result: resultError, reason: fmt.Sprintf("%s exited %d for findings, but none could be read from its report: %s", name, code, said)}
	}
	return judgeFindings(s, found, d, len(files), fmt.Sprintf("%s exited %d", name, code), findings)
}

func scanCommand(ctx context.Context, s *scanSpec, files []prFile, ws workspace, input, out string) (cmd *exec.Cmd, name string) {
	expand := strings.NewReplacer("{main}", ws.main, "{bin}", ws.bin, "{head}", ws.head, "{input}", input, "{out}", out, "{data}", ws.data, "{self}", ws.self).Replace
	var args []string
	for _, a := range s.Run {
		if a != "{files}" {
			args = append(args, expand(a))
			continue
		}
		for _, f := range files {
			args = append(args, f.Filename)
		}
	}
	program := args[0]
	if p := filepath.Join(ws.bin, program); !strings.Contains(program, "/") && fileExists(p) {
		program = p
	}
	//nolint:gosec // G204: the program and arguments are config.yaml's on main; the PR supplies only file names, passed as arguments, never through a shell.
	cmd = exec.CommandContext(ctx, program, args[1:]...)
	cmd.Dir = ws.dir
	if s.Dir != "" {
		cmd.Dir = expand(s.Dir)
	}
	cmd.Env = append(childEnv(), "PATH="+ws.bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for k, v := range s.Env {
		cmd.Env = append(cmd.Env, k+"="+expand(v))
	}
	return cmd, args[0]
}

func exitCode(err error) (int, error) {
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		return exitErr.ExitCode(), nil
	}
	return 0, err
}

func readFindings(r reportSpec, out string, stdout []byte) ([]finding, error) {
	report := stdout
	if r.In == "out" {
		var err error
		report, err = os.ReadFile(out)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	return parseReport(r, report)
}

func judgeFindings(s *scanSpec, found []finding, d *data, files int, exited string, findingsExit bool) outcome {
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
	case findingsExit:
		return pass(fmt.Sprintf("no finding (%s)", exited))
	}
	return pass(fmt.Sprintf("clean (%d file(s))", files))
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

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

// A PR's symlink may point anywhere on the runner: each becomes a file holding its target, as git stores it.
func flattenSymlinks(head string) error {
	return filepath.WalkDir(head, func(p string, e fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case e.IsDir() && e.Name() == ".git":
			return filepath.SkipDir
		case e.Type()&fs.ModeSymlink == 0:
			return nil
		}
		target, err := os.Readlink(p)
		if err != nil {
			return err
		}
		if err := os.Remove(p); err != nil {
			return err
		}
		return os.WriteFile(p, []byte(target), 0o600)
	})
}

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

func parseReport(r reportSpec, report []byte) ([]finding, error) {
	switch r.Format {
	case "lines":
		return lineFindings(r, report)
	case "json-lines":
		return jsonLineFindings(r, report)
	}
	if len(bytes.TrimSpace(report)) == 0 {
		return nil, nil
	}
	var v any
	if err := json.Unmarshal(report, &v); err != nil {
		return nil, err
	}
	return jsonFindings(r, v), nil
}

func lineFindings(r reportSpec, report []byte) ([]finding, error) {
	var found []finding
	sc := bufio.NewScanner(bytes.NewReader(report))
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		line := sc.Text()
		if r.ignore != nil && r.ignore.MatchString(line) {
			continue
		}
		if m := r.pattern.FindStringSubmatch(line); m != nil {
			found = append(found, namedFinding(r.pattern.SubexpNames(), m))
		}
	}
	return found, sc.Err()
}

func namedFinding(names, m []string) finding {
	var f finding
	for i, name := range names {
		switch name {
		case "file":
			f.file = m[i]
		case "line":
			f.line, _ = strconv.Atoi(m[i])
		case "message":
			f.message = strings.TrimSpace(m[i])
		}
	}
	return f
}

func jsonLineFindings(r reportSpec, report []byte) ([]finding, error) {
	var found []finding
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
		v.Result, v.Reason = results[i].result, clip(results[i].reason, maxReason)
		ev.Checks[c.Name] = v
	}
	ev.finish()
	return nil
}
