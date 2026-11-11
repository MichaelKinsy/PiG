// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// headScans are the scans that need the PR's files as they are at its head, not only the lines it adds.
var headScans = []string{"reuse", "workflows", "fixtures", "pins", "provenance"}

// prepare writes what the scan steps read into dir:
//
//	added/<path>   the lines the PR adds to each file, for the secret and fixture scans
//	files.txt      the PR's added and modified files, one per line
//	added-lines.txt  "path:line" of every line the PR adds, to keep only findings on those lines
//	head/          the PR's head commit without history, only the changed files and the licensing configuration
//	               checked out, only when a scan reads whole files
//
// Nothing from the PR is executed; head/ is only read by the scanners.
func prepare(ctx context.Context, cfg config, d *data, ev evaluation, dir string, log io.Writer) error {
	if len(ev.Scans) == 0 {
		logf(log, "prepare: no scan to run")
		return nil
	}
	added := filepath.Join(dir, "added")
	var list []string
	for _, f := range d.Files {
		if f.Status == "removed" {
			continue
		}
		list = append(list, f.Filename)
		lines := addedLines(f.Patch)
		if len(lines) == 0 {
			continue
		}
		target, err := inside(added, f.Filename)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "files.txt"), []byte(strings.Join(list, "\n")+"\n"), 0o600); err != nil {
		return err
	}
	if err := writeAddedLines(d, filepath.Join(dir, "added-lines.txt")); err != nil {
		return err
	}
	logf(log, "prepare: added lines of %d file(s) in %s", len(list), added)
	if !slices.ContainsFunc(ev.Scans, func(s string) bool { return slices.Contains(headScans, s) }) {
		return nil
	}
	head := filepath.Join(dir, "head")
	remote := fmt.Sprintf("https://github.com/%s", d.Repo)
	ref := fmt.Sprintf("refs/pull/%d/head", d.PR)
	start := time.Now()
	// Only the changed files and what the scans to run list under checkout are checked out; other blobs are never
	// downloaded.
	patterns := rooted(list)
	for _, c := range cfg.Checks {
		if slices.Contains(ev.Scans, c.Name) {
			for _, p := range c.Checkout {
				if p != "/*" {
					p = "/" + p
				}
				patterns = append(patterns, p)
			}
		}
	}
	slices.Sort(patterns)
	steps := [][]string{
		{"init", "-q", head},
		{"-C", head, "fetch", "-q", "--depth=1", "--filter=blob:none", remote, ref},
		append([]string{"-C", head, "sparse-checkout", "set", "--no-cone"}, slices.Compact(patterns)...),
		{"-C", head, "checkout", "-q", "--detach", "FETCH_HEAD"},
	}
	if slices.Contains(patterns, "/*") {
		// The whole tree: one fetch of every blob is faster than fetching them lazily at checkout.
		steps = [][]string{steps[0], {"-C", head, "fetch", "-q", "--depth=1", remote, ref}, steps[3]}
	}
	for _, args := range steps {
		//nolint:gosec // G204: the program is literal git; the arguments are this run's directory, the repository of the PR's base and the PR's ref.
		cmd := exec.CommandContext(ctx, "git", args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, out)
		}
	}
	//nolint:gosec // G204: literal git reading the commit just checked out.
	out, err := exec.CommandContext(ctx, "git", "-C", head, "rev-parse", "HEAD").Output()
	if err != nil {
		return err
	}
	if got := strings.TrimSpace(string(out)); got != d.Pull.Head.SHA {
		return fmt.Errorf("prepare: %s is at %s, the evaluated head is %s; the PR moved, run again", ref, short(got), short(d.Pull.Head.SHA))
	}
	logf(log, "prepare: head %s checked out in %s (%s)", short(d.Pull.Head.SHA), head, time.Since(start).Round(time.Millisecond))
	return nil
}

// rooted anchors each path at the repository root, as a no-cone sparse-checkout pattern.
func rooted(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, "/"+p)
	}
	return out
}

// inside joins name under root and refuses a name that would leave it.
func inside(root, name string) (string, error) {
	target := filepath.Join(root, filepath.FromSlash(name))
	if rel, err := filepath.Rel(root, target); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("prepare: file name %q leaves the scan directory", name)
	}
	return target, nil
}

// recordVerdict stores a scan step's outcome for one check in the evaluation: status 0 passes, anything else fails
// with the report's first lines as the reason.
func recordVerdict(ev *evaluation, name string, status int, report string) error {
	v, ok := ev.Checks[name]
	if !ok {
		return fmt.Errorf("verdict: no check %q in the evaluation", name)
	}
	if v.Result != resultScan {
		return fmt.Errorf("verdict: check %q is %s, not waiting for a scan", name, v.Result)
	}
	if status == 0 {
		v.Result, v.Reason = resultPass, "scan found nothing"
		if open := slices.IndexFunc(v.Warnings, func(w warning) bool { return !w.Acknowledged }); open >= 0 {
			v.Reason += ", with warnings"
		}
	} else {
		v.Result = resultFail
		v.Reason = fmt.Sprintf("scan exited %d", status)
		if lines := firstLines(report, 5); lines != "" {
			v.Reason += ": " + lines
		}
	}
	ev.Checks[name] = v
	ev.finish()
	return nil
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

func readEvaluation(path string) (evaluation, error) {
	var ev evaluation
	raw, err := os.ReadFile(path)
	if err == nil {
		err = json.Unmarshal(raw, &ev)
	}
	return ev, err
}

func runPrepare(cfg config, args []string, stdout, stderr io.Writer) int {
	flags, err := parseFlags(args, "--data", "--eval", "--dir")
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "preflight prepare:", err)
		return 2
	}
	var d data
	raw, err := os.ReadFile(flags["--data"])
	if err == nil {
		err = json.Unmarshal(raw, &d)
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "preflight prepare:", err)
		return 2
	}
	ev, err := readEvaluation(flags["--eval"])
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "preflight prepare:", err)
		return 2
	}
	if err := prepare(context.Background(), cfg, &d, ev, flags["--dir"], stdout); err != nil {
		_, _ = fmt.Fprintln(stderr, "preflight prepare:", err)
		return 1
	}
	return 0
}

func runVerdict(args []string, stdout, stderr io.Writer) int {
	flags, err := parseFlags(args, "--eval", "--check", "--status", "--report?")
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "preflight verdict:", err)
		return 2
	}
	status, err := strconv.Atoi(flags["--status"])
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "preflight verdict: --status %q is not an exit status\n", flags["--status"])
		return 2
	}
	ev, err := readEvaluation(flags["--eval"])
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "preflight verdict:", err)
		return 2
	}
	report := ""
	if p := flags["--report"]; p != "" {
		raw, err := os.ReadFile(p)
		if err != nil && !os.IsNotExist(err) {
			_, _ = fmt.Fprintln(stderr, "preflight verdict:", err)
			return 2
		}
		report = string(raw)
	}
	if err := recordVerdict(&ev, flags["--check"], status, report); err != nil {
		_, _ = fmt.Fprintln(stderr, "preflight", err)
		return 2
	}
	v := ev.Checks[flags["--check"]]
	logf(stdout, "%s %-18s %s", resultMark[v.Result], flags["--check"], v.Reason)
	if v.Result == resultFail {
		logf(stdout, "::error title=pre-flight %s::%s", flags["--check"], v.Reason)
	}
	if err := writeJSON(flags["--eval"], ev); err != nil {
		_, _ = fmt.Fprintln(stderr, "preflight verdict:", err)
		return 2
	}
	return 0
}

// parseFlags reads "--name value" pairs; a name ending in "?" is optional, every other name is required.
func parseFlags(args []string, names ...string) (map[string]string, error) {
	known := map[string]bool{}
	for _, n := range names {
		known[strings.TrimSuffix(n, "?")] = true
	}
	got := map[string]string{}
	for i := 0; i < len(args); i += 2 {
		if !known[args[i]] {
			return nil, fmt.Errorf("unknown flag %s", args[i])
		}
		if i+1 >= len(args) {
			return nil, fmt.Errorf("%s needs a value", args[i])
		}
		got[args[i]] = args[i+1]
	}
	for _, n := range names {
		if !strings.HasSuffix(n, "?") && got[n] == "" {
			return nil, fmt.Errorf("%s is required", n)
		}
	}
	return got, nil
}

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

// addedLineNumbers returns the head line numbers a unified diff patch adds.
func addedLineNumbers(patch string) []int {
	var out []int
	for _, l := range addedNumbered(patch) {
		out = append(out, l.n)
	}
	return out
}

// writeAddedLines writes "path:line" for every line the PR adds, so a scan of whole files reports only what the PR
// changed: a finding on an untouched line is the base's, not this PR's.
func writeAddedLines(d *data, path string) error {
	var b strings.Builder
	for _, f := range d.Files {
		for _, n := range addedLineNumbers(f.Patch) {
			fmt.Fprintf(&b, "%s:%d\n", f.Filename, n)
		}
	}
	return os.WriteFile(path, []byte(b.String()), 0o600)
}
