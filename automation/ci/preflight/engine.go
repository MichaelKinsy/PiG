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
	"time"
)

// runOptions are one pre-flight run over a repository's PRs.
type runOptions struct {
	apiBase, token string
	repo           string
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
func runPR(ctx context.Context, cfg config, env *ruleEnv, o runOptions, version string, pr int, log io.Writer) (string, error) {
	statePath := filepath.Join(o.states, strconv.Itoa(pr)+".json")
	d, err := fetch(ctx, cfg, fetchOptions{apiBase: o.apiBase, token: o.token, repo: o.repo, pr: pr, version: version, previous: readState(statePath)}, log)
	if err != nil {
		return resultError, err
	}
	ev := evaluate(cfg, d, env)
	if len(ev.Scans) > 0 && d.Mode != modeReuse {
		if ev.cheapFailed(cfg) && !o.forceScans {
			logf(log, "scans skipped: a cheap check failed")
		} else {
			ws := workspace{dir: filepath.Join(o.work, strconv.Itoa(pr)), main: o.root, bin: o.bin}
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
	version, err := preflightVersion(o.root)
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
		slices.Sort(prs)
	}
	logf(out, "pre-flight %s over %d PR(s) of %s, %d at a time", short(version), len(prs), o.repo, o.jobs)
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
				r.result, r.err = runPR(ctx, cfg, env, o, version, r.pr, &r.log)
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
	for _, r := range results {
		logf(out, "PR #%d: %s", r.pr, r.result)
	}
	if failedToRun > 0 {
		return fmt.Errorf("the pre-flight could not run for %d PR(s)", failedToRun)
	}
	return nil
}
