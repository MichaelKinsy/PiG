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

type runOptions struct {
	apiBase, token string
	gitBase        string
	repo           string
	root           string
	bin            string
	work           string
	prs            []int
	jobs           int
	bot            string
	dryRun         bool
	forceScans     bool
	budget         int
	timeBudget     time.Duration
}

type pending struct {
	pr      int
	comment prComment
	err     error
	// pull is read while the comments load, so GitHub starts computing mergeable early; nil when that read failed.
	pull     *pull
	pulledAt time.Time
}

// loadJobs is how many PRs load their comment and pull at once: light reads, all before any PR runs.
const loadJobs = 16

func runPR(ctx context.Context, cfg config, env *ruleEnv, o runOptions, version string, p pending, admit func() bool, t *tips, log io.Writer) (result, head string, err error) {
	if p.err != nil {
		return resultError, "", fmt.Errorf("its pre-flight comment could not be read: %w", p.err)
	}
	d, err := fetch(ctx, cfg, fetchOptions{apiBase: o.apiBase, token: o.token, repo: o.repo, pr: p.pr, version: version, previous: p.comment.State, admit: admit, tips: t, pull: p.pull, pulledAt: p.pulledAt}, log)
	if err != nil {
		return resultError, "", err
	}
	head = d.Pull.Head.SHA
	if d.Mode == modeDeferred {
		return resultDeferred, head, nil
	}
	ws := workspace{dir: filepath.Join(o.work, strconv.Itoa(p.pr)), main: o.root, bin: o.bin, remote: o.gitBase, token: o.token}
	if d.Mode != modeReuse && d.Limit == "" {
		askGit(ctx, ws, d, log)
	}
	ev := evaluate(cfg, d, env)
	if len(ev.Scans) > 0 && d.Mode != modeReuse {
		if ev.cheapFailed(cfg) && !o.forceScans {
			logf(log, "scans skipped: a cheap check failed")
		} else {
			if err := runScans(ctx, cfg, d, &ev, ws, log); err != nil {
				for _, name := range ev.Scans {
					v := ev.Checks[name]
					v.Result, v.Reason = resultError, clip("the scan inputs could not be prepared: "+err.Error(), maxReason)
					ev.Checks[name] = v
				}
				ev.finish()
			}
		}
	}
	report(log, cfg, ev)
	if err := record(ctx, cfg, d, ev, recordOptions{apiBase: o.apiBase, token: o.token, dryRun: o.dryRun, comment: p.comment}, log); err != nil {
		return resultError, head, err
	}
	return ev.Result, head, nil
}

type handled struct {
	PR       int    `json:"pr"`
	Head     string `json:"head,omitempty"`
	Result   string `json:"result"`
	Error    string `json:"error,omitempty"`
	PassedOn bool   `json:"passed_on,omitempty"`
}

func deferredPRs(hs []handled) []int {
	var prs []int
	for _, h := range hs {
		if h.Result == resultDeferred {
			prs = append(prs, h.PR)
		}
	}
	return prs
}

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

func runBatch(ctx context.Context, cfg config, o runOptions, out io.Writer) ([]handled, error) {
	version, err := bindMain(&cfg, o.root)
	if err != nil {
		return nil, err
	}
	a := newAPI(o.apiBase, o.token)
	numbers, err := prNumbers(ctx, a, o)
	if err != nil {
		return nil, err
	}
	t := newTips(a)
	prs := make([]pending, len(numbers))
	each(len(numbers), loadJobs, func(i int) { prs[i] = load(ctx, a, o, numbers[i], t) })
	logf(out, "pre-flight %s over %d PR(s) of %s, %d at a time", short(version), len(prs), o.repo, o.jobs)
	runs := runAll(ctx, cfg, o, version, prs, t)
	hs, failedToRun := reportRuns(runs, out)
	if failedToRun > 0 && failedToRun == len(runs) {
		return hs, fmt.Errorf("the pre-flight could not run for any of the %d PR(s)", failedToRun)
	}
	return hs, nil
}

func load(ctx context.Context, a *api, o runOptions, pr int, t *tips) pending {
	p := pending{pr: pr}
	var wg sync.WaitGroup
	wg.Go(func() { p.comment, p.err = loadComment(ctx, a, o.repo, pr, o.bot) })
	wg.Go(func() {
		var pl pull
		if a.get(ctx, fmt.Sprintf("repos/%s/pulls/%d", o.repo, pr), &pl, false) == nil {
			p.pull, p.pulledAt = &pl, time.Now()
			_, _ = t.get(ctx, o.repo, pl.Base.Ref)
		}
	})
	wg.Wait()
	return p
}

func prNumbers(ctx context.Context, a *api, o runOptions) ([]int, error) {
	numbers := slices.Clone(o.prs)
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
	slices.Sort(numbers)
	return numbers, nil
}

type prRun struct {
	pr     int
	result string
	head   string
	err    error
	log    bytes.Buffer
	took   time.Duration
}

func runAll(ctx context.Context, cfg config, o runOptions, version string, prs []pending, t *tips) []*prRun {
	budget := o.budget
	if len(o.prs) > 0 {
		budget = 0
	}
	var admitted atomic.Int32
	admit := func() bool {
		return budget <= 0 || int(admitted.Add(1)) <= budget
	}
	deadline := time.Now().Add(o.timeBudget)
	env := &ruleEnv{root: o.root}
	runs := make([]*prRun, len(prs))
	each(len(prs), o.jobs, func(i int) {
		r := &prRun{pr: prs[i].pr}
		start := time.Now()
		if o.timeBudget > 0 && !start.Before(deadline) {
			r.result = resultDeferred
			logf(&r.log, "the run's time budget of %s is spent", o.timeBudget)
		} else {
			r.result, r.head, r.err = runPR(ctx, cfg, env, o, version, prs[i], admit, t, &r.log)
		}
		r.took = time.Since(start)
		runs[i] = r
	})
	return runs
}

func reportRuns(runs []*prRun, out io.Writer) (hs []handled, failedToRun int) {
	for _, r := range runs {
		logf(out, "::group::PR #%d: %s (%s)", r.pr, r.result, r.took.Round(time.Millisecond))
		_, _ = out.Write(r.log.Bytes())
		if r.err != nil {
			failedToRun++
			logf(out, "::error title=pre-flight #%d::%s", r.pr, annotation("the pre-flight could not run: "+r.err.Error()))
		}
		logf(out, "::endgroup::")
	}
	hs = make([]handled, 0, len(runs))
	for _, r := range runs {
		logf(out, "PR #%d: %s", r.pr, r.result)
		h := handled{PR: r.pr, Head: r.head, Result: r.result}
		if r.err != nil {
			h.Error = r.err.Error()
		}
		hs = append(hs, h)
	}
	if deferred := deferredPRs(hs); len(deferred) > 0 {
		logf(out, "%d PR(s) deferred by the budget, for a next job: %v", len(deferred), deferred)
	}
	if failedToRun > 0 {
		logf(out, "the pre-flight could not run for %d of %d PR(s)", failedToRun, len(runs))
	}
	return hs, failedToRun
}

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
