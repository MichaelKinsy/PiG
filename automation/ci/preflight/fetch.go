// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// state is what a run leaves for the next run of the same PR.
type state struct {
	Version string             `json:"version"`
	Head    string             `json:"head"`
	Base    string             `json:"base"`
	Checks  map[string]verdict `json:"checks"`
}

// verdict is one check's outcome and the hash of the inputs it was decided on. Warnings do not fail the check; they are
// shown to the reviewer.
type verdict struct {
	Input    string    `json:"input"`
	Result   string    `json:"result"`
	Reason   string    `json:"reason,omitempty"`
	Warnings []warning `json:"warnings,omitempty"`
}

// warning is something a reviewer should know that does not fail its check. Notice is the exact text it is about; with
// the file's blob it is what an acknowledgement in acknowledged.toml matches.
type warning struct {
	File   string `json:"file"`
	Line   int    `json:"line,omitempty"`
	Notice string `json:"notice"`
	Text   string `json:"text"`
	Blob   string `json:"blob"`
	// Acknowledged is set when acknowledged.toml holds a matching entry for this blob of the file.
	Acknowledged bool `json:"acknowledged,omitempty"`
}

// Modes of a run.
const (
	modeFull  = "full"
	modeDelta = "delta"
	modeReuse = "reuse"
)

// data is everything fetch collects for one PR; evaluate decides every check from it alone.
type data struct {
	Repo      string `json:"repo"`
	PR        int    `json:"pr"`
	Version   string `json:"version"`
	Mode      string `json:"mode"`
	Reason    string `json:"reason"`
	MainMoved bool   `json:"main_moved"`
	// Limit names a GitHub REST limit the PR exceeds; the PR fails with it instead of being judged on truncated data.
	Limit        string             `json:"limit,omitempty"`
	Pull         pull               `json:"pull"`
	Files        []prFile           `json:"files"`
	Commits      []prCommit         `json:"commits"`
	CompareMain  comparison         `json:"compare_main"`
	Delta        []string           `json:"delta,omitempty"`
	DependsOn    *pull              `json:"depends_on,omitempty"`
	Dependencies []dependencyChange `json:"dependencies,omitempty"`
	Relevant     []string           `json:"relevant"`
	Previous     *state             `json:"previous,omitempty"`
	// SourceErrors holds each source that still failed after its retries; the checks reading it cannot pass, the others
	// are decided as usual.
	SourceErrors map[string]string `json:"source_errors,omitempty"`
	// Acknowledged is acknowledged.toml as it is at the PR's head; empty when the file does not exist there.
	Acknowledged string `json:"acknowledged,omitempty"`
}

// failed records a source that failed after its retries.
func (d *data) failed(mu *sync.Mutex, log io.Writer, source string, err error) {
	mu.Lock()
	defer mu.Unlock()
	if d.SourceErrors == nil {
		d.SourceErrors = map[string]string{}
	}
	d.SourceErrors[source] = err.Error()
	logf(log, "::warning::source %s failed after retries: %v", source, err)
}

var dependsOnRE = regexp.MustCompile(`(?mi)^[ \t]*Depends on:[ \t]*(\S.*?)[ \t]*$`)

// parseDependsOn reads the template's "Depends on:" line: "None" or "#<n>". ok is false when the line is missing or holds
// anything else.
func parseDependsOn(body string) (number int, ok bool) {
	m := dependsOnRE.FindStringSubmatch(body)
	if m == nil {
		return 0, false
	}
	if m[1] == "None" {
		return 0, true
	}
	if n, err := strconv.Atoi(m[1][1:]); err == nil && m[1][0] == '#' && n > 0 {
		return n, true
	}
	return 0, false
}

// decideMode compares this run with the previous state. cmpPrevious is the compare of the previous head with the new one,
// nil when the previous head no longer exists.
func decideMode(prev *state, version, head, base string, cmpPrevious *comparison) (mode, reason string, mainMoved bool) {
	switch {
	case prev == nil:
		return modeFull, "no previous state", false
	case prev.Version != version:
		return modeFull, "the pre-flight changed since the previous run", false
	}
	mainMoved = base != prev.Base
	switch {
	case head == prev.Head && !mainMoved:
		return modeReuse, "same head and base as the previous run", false
	case head == prev.Head:
		return modeDelta, "main moved, the head did not", true
	case cmpPrevious == nil:
		return modeFull, "the previous head no longer exists (force-push or rebase)", mainMoved
	case cmpPrevious.Status == "ahead":
		return modeDelta, fmt.Sprintf("%d new commit(s) since the previous run", cmpPrevious.AheadBy), mainMoved
	default:
		return modeFull, "history rewritten since the previous run (compare status " + cmpPrevious.Status + ")", mainMoved
	}
}

type fetchOptions struct {
	apiBase  string
	token    string
	repo     string
	pr       int
	version  string
	root     string
	previous *state
}

func fetch(ctx context.Context, cfg config, o fetchOptions, log io.Writer) (*data, error) {
	a := newAPI(o.apiBase, o.token)
	a.log = log
	d := &data{Repo: o.repo, PR: o.pr, Version: o.version, Previous: o.previous}
	prefix := "repos/" + o.repo

	logf(log, "::group::fetch wave 1")
	if err := a.get(ctx, fmt.Sprintf("%s/pulls/%d", prefix, o.pr), &d.Pull, false); err != nil {
		return nil, err
	}
	if d.Pull.Commits > maxCommits {
		d.Limit = fmt.Sprintf("%d commits, more than the %d the API lists", d.Pull.Commits, maxCommits)
	} else if d.Pull.ChangedFiles > maxFiles {
		d.Limit = fmt.Sprintf("%d changed files, more than the %d the API lists", d.Pull.ChangedFiles, maxFiles)
	}
	head, base := d.Pull.Head.SHA, d.Pull.Base.SHA
	if p := o.previous; p != nil && p.Version == o.version && p.Head == head && p.Base == base {
		d.Mode, d.Reason = modeReuse, "same head and base as the previous run"
		logf(log, "pull #%d head %s base %s: unchanged", o.pr, short(head), short(base))
		logf(log, "::endgroup::")
		logf(log, "mode %s: %s", d.Mode, d.Reason)
		logf(log, "fetch: %d API call(s)", a.callCount())
		return d, nil
	}
	var (
		wg          sync.WaitGroup
		mu          sync.Mutex
		cmpPrevious *comparison
		previousErr error
	)
	// Each source is fetched and retried on its own; one that still fails is recorded and the others go on.
	wg.Go(func() {
		if err := a.get(ctx, fmt.Sprintf("%s/pulls/%d/files?per_page=%d", prefix, o.pr, perPage), &d.Files, true); err != nil {
			d.failed(&mu, log, "files", err)
		}
	})
	wg.Go(func() {
		if err := a.get(ctx, fmt.Sprintf("%s/pulls/%d/commits?per_page=%d", prefix, o.pr, perPage), &d.Commits, true); err != nil {
			d.failed(&mu, log, "commits", err)
		}
	})
	wg.Go(func() {
		if err := a.get(ctx, fmt.Sprintf("%s/compare/%s...%s", prefix, pathEscape(d.Pull.Base.Ref), head), &d.CompareMain, false); err != nil {
			d.failed(&mu, log, "compare-main", err)
		}
	})
	if p := o.previous; p != nil && p.Head != "" && p.Head != head {
		wg.Go(func() {
			var c comparison
			err := a.get(ctx, fmt.Sprintf("%s/compare/%s...%s", prefix, p.Head, head), &c, false)
			switch {
			case err == nil:
				cmpPrevious = &c
			case !isNotFound(err):
				previousErr = err
				d.failed(&mu, log, "compare-previous", err)
			}
		})
	}
	wg.Wait()
	logf(log, "pull #%d head %s base %s (%s): %d commit(s), %d file(s), behind main by %d", o.pr, short(head), short(base), d.Pull.Base.Ref, len(d.Commits), len(d.Files), d.CompareMain.BehindBy)
	logf(log, "::endgroup::")

	d.Mode, d.Reason, d.MainMoved = decideMode(o.previous, o.version, head, base, cmpPrevious)
	if previousErr != nil {
		d.Mode, d.Reason = modeFull, "the compare with the previous head failed"
	}
	if cmpPrevious != nil && d.Mode == modeDelta {
		for _, f := range cmpPrevious.Files {
			d.Delta = append(d.Delta, f.Filename)
		}
	}
	logf(log, "mode %s: %s", d.Mode, d.Reason)

	changed := make([]string, 0, len(d.Files))
	for _, f := range d.Files {
		changed = append(changed, f.Filename)
		if f.PreviousFilename != "" {
			changed = append(changed, f.PreviousFilename)
		}
	}
	for _, c := range cfg.Checks {
		if c.relevant(changed) {
			d.Relevant = append(d.Relevant, c.Name)
		}
	}
	logf(log, "relevant checks: %v", d.Relevant)
	if d.Mode == modeReuse {
		logf(log, "fetch: %d API call(s)", a.callCount())
		return d, nil
	}

	reads := map[string]bool{}
	for _, c := range cfg.Checks {
		if slices.Contains(d.Relevant, c.Name) {
			for _, r := range c.Reads {
				reads[r] = true
			}
		}
	}
	logf(log, "::group::fetch wave 2")
	if n, ok := parseDependsOn(d.Pull.Body); ok && n > 0 && reads["depends-on"] {
		wg.Go(func() {
			var p pull
			if err := a.get(ctx, fmt.Sprintf("%s/pulls/%d", prefix, n), &p, false); err != nil {
				d.failed(&mu, log, "depends-on", err)
				return
			}
			d.DependsOn = &p
			logf(log, "depends on #%d: state %s, merged %v", n, p.State, p.Merged)
		})
	}
	if reads["dependency-graph"] {
		wg.Go(func() {
			if err := a.get(ctx, fmt.Sprintf("%s/dependency-graph/compare/%s...%s", prefix, base, head), &d.Dependencies, false); err != nil {
				d.failed(&mu, log, "dependency-graph", err)
				return
			}
			logf(log, "dependency changes: %d", len(d.Dependencies))
		})
	}
	{
		wg.Go(func() {
			var file struct {
				Content  string `json:"content"`
				Encoding string `json:"encoding"`
			}
			err := a.get(ctx, fmt.Sprintf("%s/contents/%s?ref=%s", prefix, acknowledgedPath, head), &file, false)
			switch {
			case isNotFound(err):
			case err != nil:
				d.failed(&mu, log, "acknowledged", err)
			case file.Encoding != "base64":
				d.failed(&mu, log, "acknowledged", fmt.Errorf("%s has encoding %q", acknowledgedPath, file.Encoding))
			default:
				raw, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(file.Content, "\n", ""))
				if err != nil {
					d.failed(&mu, log, "acknowledged", err)
					return
				}
				d.Acknowledged = string(raw)
			}
		})
	}
	if d.Pull.Mergeable == nil {
		wg.Go(func() {
			// GitHub computes mergeable in the background and answers null until it is done.
			select {
			case <-ctx.Done():
				return
			case <-time.After(3 * time.Second):
			}
			var p pull
			if err := a.get(ctx, fmt.Sprintf("%s/pulls/%d", prefix, o.pr), &p, false); err != nil {
				d.failed(&mu, log, "pull-mergeable", err)
				return
			}
			d.Pull.Mergeable = p.Mergeable
			logf(log, "mergeable after retry: %v", formatBool(p.Mergeable))
		})
	}
	wg.Wait()
	logf(log, "::endgroup::")
	logf(log, "fetch: %d API call(s)", a.callCount())
	return d, nil
}

func writeJSON(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o600)
}

var logMu sync.Mutex

func logf(w io.Writer, format string, args ...any) {
	logMu.Lock()
	defer logMu.Unlock()
	_, _ = fmt.Fprintf(w, format+"\n", args...)
}

func short(sha string) string { return sha[:min(len(sha), 9)] }

func formatBool(b *bool) string {
	if b == nil {
		return "unknown"
	}
	return strconv.FormatBool(*b)
}
