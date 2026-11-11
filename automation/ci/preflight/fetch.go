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
	pull     *pull
	pulledAt time.Time
}

func fetch(ctx context.Context, cfg config, o fetchOptions, log io.Writer) (*data, error) {
	f := &fetcher{a: newAPI(o.apiBase, o.token), o: o, log: log, prefix: "repos/" + o.repo}
	f.a.log = log
	f.d = &data{Repo: o.repo, PR: o.pr, Version: o.version, Previous: o.previous}
	// Not ::group::: GitHub does not nest groups, and the batch already wraps each PR in one.
	logf(log, "── fetch wave 1")
	if err := f.readPull(ctx); err != nil {
		return nil, err
	}
	if f.settled(ctx) {
		return f.d, nil
	}
	f.waveOne(ctx)
	f.d.Relevant = relevantChecks(cfg, f.d.Files)
	logf(log, "relevant checks: %v", f.d.Relevant)
	logf(log, "── fetch wave 2")
	f.waveTwo(ctx, readsOf(cfg, f.d.Relevant))
	logf(log, "fetch: %d API call(s)", f.a.callCount())
	return f.d, nil
}

type fetcher struct {
	a      *api
	o      fetchOptions
	d      *data
	prefix string
	log    io.Writer
	wg     sync.WaitGroup
	mu     sync.Mutex
}

func (f *fetcher) readPull(ctx context.Context) error {
	d := f.d
	if f.o.pull == nil {
		if err := f.a.get(ctx, fmt.Sprintf("%s/pulls/%d", f.prefix, f.o.pr), &d.Pull, false); err != nil {
			return err
		}
		f.o.pulledAt = time.Now()
	} else {
		d.Pull = *f.o.pull
	}
	switch {
	case d.Pull.Commits > maxCommits:
		d.Limit = fmt.Sprintf("%d commits, more than the %d the API lists", d.Pull.Commits, maxCommits)
	case d.Pull.ChangedFiles > maxFiles:
		d.Limit = fmt.Sprintf("%d changed files, more than the %d the API lists", d.Pull.ChangedFiles, maxFiles)
	}
	d.BaseTip = d.Pull.Base.SHA
	if f.o.tips != nil {
		tip, err := f.o.tips.get(ctx, f.o.repo, d.Pull.Base.Ref)
		if err != nil {
			logf(f.log, "the tip of %s could not be read, so nothing is reused: %v", d.Pull.Base.Ref, err)
		}
		d.BaseTip = tip
	}
	d.Mode, d.Reason = decideMode(f.o.previous, f.o.version, d.Pull.Head.SHA, d.BaseTip, bodyHash(d.Pull.Body))
	return nil
}

func (f *fetcher) settled(ctx context.Context) bool {
	d := f.d
	switch {
	case d.Mode == modeReuse:
		logf(f.log, "pull #%d head %s base %s: unchanged", f.o.pr, short(d.Pull.Head.SHA), short(d.BaseTip))
		logf(f.log, "mode %s: %s", d.Mode, d.Reason)
		logf(f.log, "fetch: %d API call(s)", f.a.callCount())
		return true
	case d.Limit != "":
		logf(f.log, "pull #%d head %s: too large for the pre-flight (%s), nothing more fetched", f.o.pr, short(d.Pull.Head.SHA), d.Limit)
	case f.o.admit != nil && !f.o.admit():
		d.Mode, d.Reason = modeDeferred, "the run's budget is spent; a next job takes it"
	default:
		return false
	}
	logf(f.log, "mode %s: %s", d.Mode, d.Reason)
	return true
}

func (f *fetcher) source(name string, fetch func() error) {
	f.wg.Go(func() {
		if err := fetch(); err != nil {
			f.d.failed(&f.mu, f.log, name, err)
		}
	})
}

func (f *fetcher) waveOne(ctx context.Context) {
	d, head := f.d, f.d.Pull.Head.SHA
	f.source("files", func() error {
		return f.a.get(ctx, fmt.Sprintf("%s/pulls/%d/files?per_page=%d", f.prefix, f.o.pr, perPage), &d.Files, true)
	})
	f.source("commits", func() error {
		return f.a.get(ctx, fmt.Sprintf("%s/pulls/%d/commits?per_page=%d", f.prefix, f.o.pr, perPage), &d.Commits, true)
	})
	f.source("compare-main", func() error {
		return f.a.get(ctx, fmt.Sprintf("%s/compare/%s...%s", f.prefix, refPath(d.Pull.Base.Ref), head), &d.CompareMain, false)
	})
	f.wg.Wait()
	logf(f.log, "pull #%d head %s base %s (%s): %d commit(s), %d file(s), behind main by %d", f.o.pr, short(head), short(d.BaseTip), d.Pull.Base.Ref, len(d.Commits), len(d.Files), d.CompareMain.BehindBy)
	logf(f.log, "mode %s: %s", d.Mode, d.Reason)
}

func (f *fetcher) waveTwo(ctx context.Context, reads map[string]bool) {
	d, head := f.d, f.d.Pull.Head.SHA
	if n, ok := parseDependsOn(d.Pull.Body); ok && n > 0 && reads["depends-on"] {
		f.source("depends-on", func() error {
			var p pull
			if err := f.a.get(ctx, fmt.Sprintf("%s/pulls/%d", f.prefix, n), &p, false); err != nil {
				return err
			}
			d.DependsOn = &p
			logf(f.log, "depends on #%d: state %s, merged %v", n, p.State, p.Merged)
			return nil
		})
	}
	if reads["dependency-graph"] {
		f.source("dependency-graph", func() error {
			if err := f.a.get(ctx, fmt.Sprintf("%s/dependency-graph/compare/%s...%s", f.prefix, d.Pull.Base.SHA, head), &d.Dependencies, false); err != nil {
				return err
			}
			logf(f.log, "dependency changes: %d", len(d.Dependencies))
			return nil
		})
	}
	f.source("acknowledged", func() error {
		text, err := f.acknowledged(ctx, head)
		if err == nil {
			d.Acknowledged = text
		}
		return err
	})
	if d.Pull.Mergeable == nil {
		f.source("pull-mergeable", func() error { return f.mergeableAgain(ctx) })
	}
	f.wg.Wait()
}

func (f *fetcher) acknowledged(ctx context.Context, head string) (string, error) {
	var file struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	err := f.a.get(ctx, fmt.Sprintf("%s/contents/%s?ref=%s", f.prefix, acknowledgedPath, head), &file, false)
	switch {
	case hasStatus(err, http.StatusNotFound):
		return "", nil
	case err != nil:
		return "", err
	case file.Encoding != "base64":
		return "", fmt.Errorf("%s has encoding %q", acknowledgedPath, file.Encoding)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(file.Content, "\n", ""))
	return string(raw), err
}

func (f *fetcher) mergeableAgain(ctx context.Context) error {
	// GitHub computes mergeable in the background and answers null until it is done.
	select {
	case <-ctx.Done():
		return nil
	case <-time.After(mergeableRetryDelay - time.Since(f.o.pulledAt)):
	}
	var p pull
	if err := f.a.get(ctx, fmt.Sprintf("%s/pulls/%d", f.prefix, f.o.pr), &p, false); err != nil {
		return err
	}
	f.d.Pull.Mergeable = p.Mergeable
	logf(f.log, "mergeable after retry: %v", formatBool(p.Mergeable))
	return nil
}

func relevantChecks(cfg config, files []prFile) []string {
	changed := make([]string, 0, len(files))
	for _, f := range files {
		changed = append(changed, f.Filename)
		if f.PreviousFilename != "" {
			changed = append(changed, f.PreviousFilename)
		}
	}
	var relevant []string
	for _, c := range cfg.Checks {
		if c.relevant(changed) {
			relevant = append(relevant, c.Name)
		}
	}
	return relevant
}

func readsOf(cfg config, relevant []string) map[string]bool {
	reads := map[string]bool{}
	for _, c := range cfg.Checks {
		if slices.Contains(relevant, c.Name) {
			for _, r := range c.Reads {
				reads[r] = true
			}
		}
	}
	return reads
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
