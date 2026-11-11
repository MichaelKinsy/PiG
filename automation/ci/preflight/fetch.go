// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

type state struct {
	Version string             `json:"version"`
	Engine  int                `json:"engine"`
	Head    string             `json:"head"`
	Base    string             `json:"base"`
	Checks  map[string]verdict `json:"checks"`
	Body    string             `json:"body"`
	Status  string             `json:"status,omitempty"`
	Alerted []string           `json:"alerted,omitempty"`
}

type verdict struct {
	Input    string    `json:"input"`
	Result   string    `json:"result"`
	Reason   string    `json:"reason,omitempty"`
	Warnings []warning `json:"warnings,omitempty"`
}

type warning struct {
	File         string `json:"file"`
	Line         int    `json:"line,omitempty"`
	Notice       string `json:"notice"`
	Text         string `json:"text"`
	Blob         string `json:"blob"`
	More         int    `json:"more,omitempty"`
	Acknowledged bool   `json:"acknowledged,omitempty"`
}

const (
	modeFull     = "full"
	modeReuse    = "reuse"
	modeDeferred = "deferred"
)

type data struct {
	Repo    string `json:"repo"`
	PR      int    `json:"pr"`
	Version string `json:"version"`
	// GitHub's base.sha stays where the PR was last updated; it does not follow the base branch.
	BaseTip      string             `json:"base_tip"`
	Mode         string             `json:"mode"`
	Reason       string             `json:"reason"`
	Limit        string             `json:"limit,omitempty"`
	Pull         pull               `json:"pull"`
	Files        []prFile           `json:"files"`
	Commits      []prCommit         `json:"commits"`
	CompareMain  comparison         `json:"compare_main"`
	DependsOn    *pull              `json:"depends_on,omitempty"`
	Dependencies []dependencyChange `json:"dependencies,omitempty"`
	Relevant     []string           `json:"relevant"`
	Previous     *state             `json:"previous,omitempty"`
	SourceErrors map[string]string  `json:"source_errors,omitempty"`
	Acknowledged string             `json:"acknowledged,omitempty"`
}

func (d *data) failed(mu *sync.Mutex, log io.Writer, source string, err error) {
	mu.Lock()
	defer mu.Unlock()
	if d.SourceErrors == nil {
		d.SourceErrors = map[string]string{}
	}
	d.SourceErrors[source] = err.Error()
	logf(log, "::warning::source %s failed after retries: %v", source, err)
}

var mergeableRetryDelay = 3 * time.Second

var dependsOnRE = regexp.MustCompile(`(?mi)^[ \t]*Depends on:[ \t]*(\S.*?)[ \t]*$`)

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

func decideMode(prev *state, version, head, base, body string) (mode, reason string) {
	switch {
	case prev == nil:
		return modeFull, "no previous state"
	case prev.Version != version:
		return modeFull, "the pre-flight changed since the previous run"
	case prev.Head != head || prev.Base != base || base == "":
		return modeFull, "the head or main moved since the previous run"
	case prev.Body != body:
		return modeFull, "the description changed since the previous run"
	}
	for _, v := range prev.Checks {
		if v.Result == resultError || v.Result == resultPending {
			return modeFull, "the previous run left a check undecided"
		}
	}
	return modeReuse, "same head, base and description as the previous run"
}

func bodyHash(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

type tips struct {
	a  *api
	mu sync.Mutex
	at map[string]*tip
}

type tip struct {
	once sync.Once
	sha  string
	err  error
}

func newTips(a *api) *tips { return &tips{a: a, at: map[string]*tip{}} }

func (t *tips) get(ctx context.Context, repo, ref string) (string, error) {
	t.mu.Lock()
	e := t.at[ref]
	if e == nil {
		e = &tip{}
		t.at[ref] = e
	}
	t.mu.Unlock()
	e.once.Do(func() {
		var r struct {
			Object struct {
				SHA string `json:"sha"`
			} `json:"object"`
		}
		e.err = t.a.get(ctx, fmt.Sprintf("repos/%s/git/ref/heads/%s", repo, refPath(ref)), &r, false)
		e.sha = r.Object.SHA
	})
	return e.sha, e.err
}

type fetchOptions struct {
	apiBase  string
	token    string
	repo     string
	pr       int
	version  string
	previous *state
	admit    func() bool
	tips     *tips
}

func fetch(ctx context.Context, cfg config, o fetchOptions, log io.Writer) (*data, error) {
	a := newAPI(o.apiBase, o.token)
	a.log = log
	d := &data{Repo: o.repo, PR: o.pr, Version: o.version, Previous: o.previous}
	prefix := "repos/" + o.repo

	// Not ::group::: GitHub does not nest groups, and the batch already wraps each PR in one.
	logf(log, "── fetch wave 1")
	if err := a.get(ctx, fmt.Sprintf("%s/pulls/%d", prefix, o.pr), &d.Pull, false); err != nil {
		return nil, err
	}
	if d.Pull.Commits > maxCommits {
		d.Limit = fmt.Sprintf("%d commits, more than the %d the API lists", d.Pull.Commits, maxCommits)
	} else if d.Pull.ChangedFiles > maxFiles {
		d.Limit = fmt.Sprintf("%d changed files, more than the %d the API lists", d.Pull.ChangedFiles, maxFiles)
	}
	head, base := d.Pull.Head.SHA, d.Pull.Base.SHA
	if o.tips != nil {
		var err error
		if base, err = o.tips.get(ctx, o.repo, d.Pull.Base.Ref); err != nil {
			logf(log, "the tip of %s could not be read, so nothing is reused: %v", d.Pull.Base.Ref, err)
		}
	}
	d.BaseTip = base
	d.Mode, d.Reason = decideMode(o.previous, o.version, head, base, bodyHash(d.Pull.Body))
	if d.Limit != "" && d.Mode != modeReuse {
		logf(log, "pull #%d head %s: too large for the pre-flight (%s), nothing more fetched", o.pr, short(head), d.Limit)
		logf(log, "mode %s: %s", d.Mode, d.Reason)
		return d, nil
	}
	if d.Mode != modeReuse && o.admit != nil && !o.admit() {
		d.Mode, d.Reason = modeDeferred, "the run's budget is spent; a next job takes it"
		logf(log, "mode %s: %s", d.Mode, d.Reason)
		return d, nil
	}
	if d.Mode == modeReuse {
		logf(log, "pull #%d head %s base %s: unchanged", o.pr, short(head), short(base))
		logf(log, "mode %s: %s", d.Mode, d.Reason)
		logf(log, "fetch: %d API call(s)", a.callCount())
		return d, nil
	}
	var (
		wg sync.WaitGroup
		mu sync.Mutex
	)
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
		if err := a.get(ctx, fmt.Sprintf("%s/compare/%s...%s", prefix, refPath(d.Pull.Base.Ref), head), &d.CompareMain, false); err != nil {
			d.failed(&mu, log, "compare-main", err)
		}
	})
	wg.Wait()
	logf(log, "pull #%d head %s base %s (%s): %d commit(s), %d file(s), behind main by %d", o.pr, short(head), short(base), d.Pull.Base.Ref, len(d.Commits), len(d.Files), d.CompareMain.BehindBy)

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
	logf(log, "── fetch wave 2")
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
			if err := a.get(ctx, fmt.Sprintf("%s/dependency-graph/compare/%s...%s", prefix, d.Pull.Base.SHA, head), &d.Dependencies, false); err != nil {
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
			case hasStatus(err, http.StatusNotFound):
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
			case <-time.After(mergeableRetryDelay):
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
