// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// runOptions are one pre-flight run over a repository's PRs.
type runOptions struct {
	apiBase, token string
	// gitBase is where the PRs' heads are fetched from, https://github.com unless the host is another.
	gitBase string
	repo    string
	// root is main's tree: the code and configuration that judge the PRs.
	root string
	// bin holds main's built tools and the installed scanners.
	bin  string
	work string
	prs  []int
	jobs int
	// bot is the account the pre-flight comments as; only its comment holds a state.
	bot string
	// dryRun sets no status and posts no comment.
	dryRun bool
	// forceScans runs the scans even when a cheap check failed.
	forceScans bool
	// budget is how many PRs a job fully checks before it leaves the rest to next jobs; a PR it reuses does not count.
	// Zero is no limit; PRs named in prs are all run.
	budget int
	// timeBudget is how long a run starts PRs; the ones running then finish. Zero is no limit.
	timeBudget time.Duration
}

// pending is one PR of a run with its comment as loaded.
type pending struct {
	pr      int
	comment prComment
	err     error
}

// runPR runs the whole pre-flight for one PR and returns its result. An error means the pre-flight itself could not
// run for it.
func runPR(ctx context.Context, cfg config, env *ruleEnv, o runOptions, version string, p pending, admit func() bool, t *tips, log io.Writer) (string, error) {
	if p.err != nil {
		return resultError, fmt.Errorf("its pre-flight comment could not be read: %w", p.err)
	}
	d, err := fetch(ctx, cfg, fetchOptions{apiBase: o.apiBase, token: o.token, repo: o.repo, pr: p.pr, version: version, previous: p.comment.State, admit: admit, tips: t}, log)
	if err != nil {
		return resultError, err
	}
	if d.Mode == modeDeferred {
		return resultDeferred, nil
	}
	ws := workspace{dir: filepath.Join(o.work, strconv.Itoa(p.pr)), main: o.root, bin: o.bin, remote: o.gitBase, token: o.token}
	if d.Pull.Mergeable == nil && d.Mode != modeReuse {
		if m, err := mergeTree(ctx, ws, d); err != nil {
			logf(log, "mergeable: git merge-tree could not tell: %v", err)
		} else {
			d.Pull.Mergeable = &m
			logf(log, "mergeable from git merge-tree: %v", m)
		}
	}
	ev := evaluate(cfg, d, env)
	if len(ev.Scans) > 0 && d.Mode != modeReuse {
		if ev.cheapFailed(cfg) && !o.forceScans {
			logf(log, "scans skipped: a cheap check failed")
		} else {
			if err := runScans(ctx, cfg, d, &ev, ws, log); err != nil {
				for _, name := range ev.Scans {
					v := ev.Checks[name]
					v.Result, v.Reason = resultError, "the scan inputs could not be prepared: "+err.Error()
					ev.Checks[name] = v
				}
				ev.finish()
			}
		}
	}
	report(log, cfg, ev)
	if err := record(ctx, cfg, d, ev, recordOptions{apiBase: o.apiBase, token: o.token, dryRun: o.dryRun, comment: p.comment}, log); err != nil {
		return resultError, err
	}
	return ev.Result, nil
}

// each runs fn over 0..n-1, jobs at a time.
func each(n, jobs int, fn func(i int)) {
	next := make(chan int)
	var wg sync.WaitGroup
	for range max(jobs, 1) {
		wg.Go(func() {
			for i := range next {
				fn(i)
			}
		})
	}
	for i := range n {
		next <- i
	}
	close(next)
	wg.Wait()
}

// runBatch runs the pre-flight over every PR of the run, o.jobs at a time, each PR's log printed as one group when it
// is done. It returns the PRs the budget deferred, for a next job, and an error only when the pre-flight could not run
// for some PR; failing PRs are its normal output.
func runBatch(ctx context.Context, cfg config, o runOptions, out io.Writer) ([]int, error) {
	version, err := bindMain(&cfg, o.root)
	if err != nil {
		return nil, err
	}
	a := newAPI(o.apiBase, o.token)
	numbers := o.prs
	if len(numbers) == 0 {
		var open []struct {
			Number int `json:"number"`
		}
		if err := a.get(ctx, fmt.Sprintf("repos/%s/pulls?state=open&per_page=%d", o.repo, perPage), &open, true); err != nil {
			return nil, err
		}
		for _, p := range open {
			numbers = append(numbers, p.Number)
		}
	}
	// PRs in number order, each with its comment, which holds its state.
	slices.Sort(numbers)
	prs := make([]pending, len(numbers))
	each(len(numbers), o.jobs, func(i int) {
		prs[i].pr = numbers[i]
		prs[i].comment, prs[i].err = loadComment(ctx, a, o.repo, numbers[i], o.bot)
	})
	budget := o.budget
	if len(o.prs) > 0 {
		budget = 0
	}
	logf(out, "pre-flight %s over %d PR(s) of %s, %d at a time", short(version), len(prs), o.repo, o.jobs)
	deadline := time.Now().Add(o.timeBudget)
	var admitted atomic.Int32
	admit := func() bool {
		return budget <= 0 || int(admitted.Add(1)) <= budget
	}
	env := &ruleEnv{root: o.root}
	t := newTips(a)
	type done struct {
		pr     int
		result string
		err    error
		log    bytes.Buffer
		took   time.Duration
	}
	results := make([]*done, len(prs))
	each(len(prs), o.jobs, func(i int) {
		r := &done{pr: prs[i].pr}
		start := time.Now()
		if o.timeBudget > 0 && !start.Before(deadline) {
			r.result = resultDeferred
			logf(&r.log, "the run's time budget of %s is spent", o.timeBudget)
		} else {
			r.result, r.err = runPR(ctx, cfg, env, o, version, prs[i], admit, t, &r.log)
		}
		r.took = time.Since(start)
		results[i] = r
	})
	failedToRun := 0
	for _, r := range results {
		logf(out, "::group::PR #%d: %s (%s)", r.pr, r.result, r.took.Round(time.Millisecond))
		_, _ = out.Write(r.log.Bytes())
		if r.err != nil {
			failedToRun++
			logf(out, "::error title=pre-flight #%d::the pre-flight could not run: %v", r.pr, r.err)
		}
		logf(out, "::endgroup::")
	}
	var deferred []int
	for _, r := range results {
		logf(out, "PR #%d: %s", r.pr, r.result)
		if r.result == resultDeferred {
			deferred = append(deferred, r.pr)
		}
	}
	if len(deferred) > 0 {
		logf(out, "%d PR(s) deferred by the budget, for a next job: %v", len(deferred), deferred)
	}
	if failedToRun > 0 {
		return deferred, fmt.Errorf("the pre-flight could not run for %d PR(s)", failedToRun)
	}
	return deferred, nil
}

// chunks splits PRs into groups of size, each as the space-separated list `preflight run --prs` takes.
func chunks(prs []int, size int) []string {
	var out []string
	for c := range slices.Chunk(prs, max(size, 1)) {
		var b strings.Builder
		for i, n := range c {
			if i > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(strconv.Itoa(n))
		}
		out = append(out, b.String())
	}
	return out
}
