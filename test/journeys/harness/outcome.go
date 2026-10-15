package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// Outcome is the result of running one plan on PiG and Pi.
type Outcome struct {
	Journey  string
	Plan     Plan
	Failures []Failure
	Pig, Pi  *Run
	Elapsed  time.Duration
}

// Failed reports whether any checker or comparison failed.
func (o *Outcome) Failed() bool { return len(o.Failures) > 0 }

// Kinds returns the sorted distinct failure kinds.
func (o *Outcome) Kinds() []string {
	seen := map[string]bool{}
	for _, f := range o.Failures {
		seen[f.Kind] = true
	}
	var out []string
	for k := range seen {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// RunJourney executes plan on PiG and (unless PigOnly) Pi concurrently, then runs the cross-product comparisons.
func RunJourney(cfg *Config, j *Journey, plan Plan, root string) (*Outcome, error) {
	o := &Outcome{Journey: j.Name, Plan: plan}
	start := time.Now()
	kinds := []Kind{PiG, Pi}
	if j.PigOnly {
		kinds = kinds[:1]
	}
	runs := make([]*Run, len(kinds))
	for i, k := range kinds {
		r, err := NewRun(cfg, j, k, plan, filepath.Join(root, map[Kind]string{PiG: "k1", Pi: "k2"}[k]))
		if err != nil {
			return nil, err
		}
		runs[i] = r
	}
	var wg sync.WaitGroup
	for _, r := range runs {
		wg.Go(r.Execute)
	}
	wg.Wait()
	o.Pig = runs[0]
	if len(runs) > 1 {
		o.Pi = runs[1]
	}
	for _, r := range runs {
		for _, f := range r.Failures {
			f.Msg = fmt.Sprintf("[%s] %s", r.Kind, f.Msg)
			o.Failures = append(o.Failures, f)
		}
	}
	// Comparison is meaningless once either side aborted: the frames stop at different points.
	if o.Pi != nil && len(o.Pig.Failures) == 0 && len(o.Pi.Failures) == 0 {
		o.Failures = append(o.Failures, compareFrames(j, o.Pig, o.Pi)...)
		if !j.SkipRequests {
			o.Failures = append(o.Failures, compareRequests(o.Pig, o.Pi)...)
		}
		if !j.SkipSession {
			o.Failures = append(o.Failures, compareSessions(o.Pig, o.Pi)...)
		}
		o.Failures = append(o.Failures, compareAgentFiles(j, o.Pig, o.Pi)...)
	}
	o.Elapsed = time.Since(start)
	if o.Failed() || os.Getenv("JOURNEY_ALWAYS_ARTIFACTS") != "" {
		o.writeArtifacts(cfg)
	}
	return o, nil
}

// writeArtifacts saves the evidence of a failing run: frames, requests and session files of each product.
func (o *Outcome) writeArtifacts(cfg *Config) {
	for _, r := range []*Run{o.Pig, o.Pi} {
		if r == nil {
			continue
		}
		dir := r.artifacts
		_ = os.RemoveAll(dir)
		_ = os.MkdirAll(dir, 0o755)
		for _, s := range r.Snaps {
			name := fmt.Sprintf("frame-%02d-%s.txt", s.Index, strings.NewReplacer("/", "_", " ", "_").Replace(s.Name))
			_ = os.WriteFile(filepath.Join(dir, name), []byte(s.Plain+"\n"), 0o644)
		}
		var reqs []json.RawMessage
		for _, q := range r.P.Requests() {
			reqs = append(reqs, q.Raw)
		}
		b, _ := json.MarshalIndent(reqs, "", " ")
		_ = os.WriteFile(filepath.Join(dir, "requests.json"), b, 0o644)
		for i, f := range r.SB.SessionFiles() {
			if data, err := os.ReadFile(f); err == nil {
				_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("session-%d.jsonl", i)), data, 0o644)
			}
		}
		_ = os.WriteFile(filepath.Join(dir, "final-screen.txt"), []byte(r.finalScreen), 0o644)
		for i := 1; i <= r.launches; i++ {
			if data, err := os.ReadFile(filepath.Join(r.SB.Root, fmt.Sprintf("term%d", i), "stderr.log")); err == nil {
				_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("stderr-%d.log", i)), data, 0o644)
			}
		}
	}
}

// Shrink reduces a failing plan: it drops pool actions (and what depends on them) while the same failure kinds persist, running at most budget extra executions. It returns the smallest failing plan found.
func Shrink(cfg *Config, j *Journey, failing *Outcome, rootFor func() string, budget int) (Plan, int) {
	best := failing.Plan
	want := failing.Kinds()
	runs := 0
	for changed := true; changed && runs < budget; {
		changed = false
		for _, name := range slices.Clone(best.Pool) {
			if runs >= budget {
				break
			}
			cand := j.Prune(best, name)
			if len(cand.Pool) == len(best.Pool) {
				continue
			}
			runs++
			shrinkCfg := *cfg
			shrinkCfg.ArtifactDir = filepath.Join(cfg.ArtifactDir, "shrink")
			o, err := RunJourney(&shrinkCfg, j, cand, rootFor())
			if err != nil || !o.Failed() {
				continue
			}
			if slices.Equal(o.Kinds(), want) || sharesKind(o.Kinds(), want) {
				best = cand
				changed = true
				break
			}
		}
	}
	return best, runs
}

func sharesKind(a, b []string) bool {
	for _, x := range a {
		if slices.Contains(b, x) {
			return true
		}
	}
	return false
}
