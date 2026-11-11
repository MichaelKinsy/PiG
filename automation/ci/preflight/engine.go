// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
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
	bin string
	// states holds one state file per PR, read as the previous run and written for the next.
	states string
	work   string
	prs    []int
	jobs   int
	// dryRun sets no status and posts no comment.
	dryRun bool
	// forceScans runs the scans even when a cheap check failed.
	forceScans bool
	// budget is how many PRs a run fully checks; a PR it reuses does not count. Zero is no limit.
	budget int
	// timeBudget is how long a run starts PRs; the ones running then finish. Zero is no limit.
	timeBudget time.Duration
}

// longestWaiting orders PRs by when they were last checked, never checked first, then by number.
func longestWaiting(prs []int, states string) []int {
	checked := map[int]time.Time{}
	for _, pr := range prs {
		if st := readState(filepath.Join(states, strconv.Itoa(pr)+".json")); st != nil {
			checked[pr] = st.Checked
		}
	}
	slices.SortStableFunc(prs, func(a, b int) int {
		if c := checked[a].Compare(checked[b]); c != 0 {
			return c
		}
		return a - b
	})
	return prs
}

func readState(path string) *state {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var s state
	if json.Unmarshal(raw, &s) != nil {
		return nil
	}
	return &s
}

// runPR runs the whole pre-flight for one PR and returns its result. An error means the pre-flight itself could not
// run for it.
func runPR(ctx context.Context, cfg config, env *ruleEnv, o runOptions, version string, pr int, admit func() bool, log io.Writer) (string, error) {
	statePath := filepath.Join(o.states, strconv.Itoa(pr)+".json")
	d, err := fetch(ctx, cfg, fetchOptions{apiBase: o.apiBase, token: o.token, repo: o.repo, pr: pr, version: version, previous: readState(statePath), admit: admit}, log)
	if err != nil {
		return resultError, err
	}
	if d.Mode == modeDeferred {
		return resultDeferred, nil
	}
	ws := workspace{dir: filepath.Join(o.work, strconv.Itoa(pr)), main: o.root, bin: o.bin, remote: o.gitBase, token: o.token}
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
	if err := record(ctx, cfg, d, ev, recordOptions{apiBase: o.apiBase, token: o.token, statePath: statePath, dryRun: o.dryRun}, log); err != nil {
		return resultError, err
	}
	return ev.Result, nil
}

// runBatch runs the pre-flight over every PR of the run, o.jobs at a time, each PR's log printed as one group when it
// is done. It returns an error only when the pre-flight could not run for some PR; failing PRs are its normal output.
func runBatch(ctx context.Context, cfg config, o runOptions, out io.Writer) error {
	version, err := bindMain(&cfg, o.root)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(o.states, 0o755); err != nil {
		return err
	}
	prs := o.prs
	if len(prs) == 0 {
		var open []struct {
			Number int `json:"number"`
		}
		a := newAPI(o.apiBase, o.token)
		if err := a.get(ctx, fmt.Sprintf("repos/%s/pulls?state=open&per_page=%d", o.repo, perPage), &open, true); err != nil {
			return err
		}
		for _, p := range open {
			prs = append(prs, p.Number)
		}
		prs = longestWaiting(prs, o.states)
	}
	logf(out, "pre-flight %s over %d PR(s) of %s, %d at a time", short(version), len(prs), o.repo, o.jobs)
	deadline := time.Now().Add(o.timeBudget)
	var admitted atomic.Int32
	admit := func() bool {
		return o.budget <= 0 || int(admitted.Add(1)) <= o.budget
	}
	env := &ruleEnv{root: o.root}
	type done struct {
		pr     int
		result string
		err    error
		log    bytes.Buffer
		took   time.Duration
	}
	results := make([]*done, len(prs))
	next := make(chan int)
	var wg sync.WaitGroup
	for range max(o.jobs, 1) {
		wg.Go(func() {
			for i := range next {
				r := &done{pr: prs[i]}
				start := time.Now()
				if o.timeBudget > 0 && !start.Before(deadline) {
					r.result = resultDeferred
					logf(&r.log, "the run's time budget of %s is spent; the next run takes it", o.timeBudget)
				} else {
					r.result, r.err = runPR(ctx, cfg, env, o, version, r.pr, admit, &r.log)
				}
				r.took = time.Since(start)
				results[i] = r
			}
		})
	}
	for i := range prs {
		next <- i
	}
	close(next)
	wg.Wait()
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
	deferred := 0
	for _, r := range results {
		logf(out, "PR #%d: %s", r.pr, r.result)
		if r.result == resultDeferred {
			deferred++
		}
	}
	if deferred > 0 {
		logf(out, "::notice title=pre-flight::%d PR(s) deferred to the next run by the budget", deferred)
	}
	if failedToRun > 0 {
		return fmt.Errorf("the pre-flight could not run for %d PR(s)", failedToRun)
	}
	return nil
}
