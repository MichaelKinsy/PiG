// SPDX-License-Identifier: MIT

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
)

type evaluation struct {
	Repo    string             `json:"repo"`
	PR      int                `json:"pr"`
	Head    string             `json:"head"`
	Base    string             `json:"base"`
	Version string             `json:"version"`
	Engine  int                `json:"engine"`
	Result  string             `json:"result"`
	Checks  map[string]verdict `json:"checks"`
	Scans   []string           `json:"scans"`
}

func sourceValue(d *data, name string) any {
	switch name {
	case "pull":
		return d.Pull
	case "files":
		return d.Files
	case "commits":
		return d.Commits
	case "compare-main":
		return map[string]any{"behind_by": d.CompareMain.BehindBy, "status": d.CompareMain.Status}
	case "depends-on":
		return d.DependsOn
	case "dependency-graph":
		return d.Dependencies
	}
	return nil
}

func cheapInputHash(d *data, c check) string {
	parts := map[string]any{"definition": c.definition}
	for _, s := range c.Reads {
		parts[s] = sourceValue(d, s)
	}
	raw, err := json.Marshal(parts)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func evaluate(cfg config, d *data, env *ruleEnv) evaluation {
	ev := evaluation{Repo: d.Repo, PR: d.PR, Head: d.Pull.Head.SHA, Base: d.BaseTip, Version: d.Version, Engine: cfg.EngineRevision, Checks: map[string]verdict{}}
	var prev map[string]verdict
	if d.Previous != nil && d.Previous.Engine == cfg.EngineRevision {
		prev = d.Previous.Checks
	}
	if d.Mode == modeReuse && prev != nil {
		ev.Checks = prev
		ev.finish()
		return ev
	}
	for _, c := range cfg.Checks {
		v := verdict{}
		if c.Stage == stageCheap {
			v.Input = cheapInputHash(d, c)
		} else {
			v.Input = scanInputHash(c, selectedFiles(d, c.Scan))
		}
		stored, had := prev[c.Name]
		source, failed := failedSource(d, c)
		switch {
		case d.Limit != "":
			v.Result, v.Reason = resultFail, "the PR is too large for the pre-flight: "+d.Limit
		case failed:
			v.Result, v.Reason = resultError, clip(fmt.Sprintf("source %s failed after retries: %s", source, d.SourceErrors[source]), maxReason)
		case !slices.Contains(d.Relevant, c.Name):
			v.Result, v.Reason = resultPass, "no file it covers changed"
		case had && stored.Input == v.Input && (stored.Result == resultPass || stored.Result == resultFail):
			v = stored
		case c.Stage == stageScan:
			v.Result, v.Reason = resultScan, "waits for its scan"
		default:
			o := decide(d, c, env)
			v.Result, v.Reason, v.Warnings = o.result, clip(o.reason, maxReason), perFile(o.warnings)
		}
		ev.Checks[c.Name] = v
	}
	if d.Acknowledged != "" {
		acks, err := parseAcknowledged(d.Acknowledged)
		if err != nil {
			v := ev.Checks["licenses"]
			v.Result, v.Reason = resultFail, clip(err.Error(), maxReason)
			ev.Checks["licenses"] = v
		} else {
			applyAcknowledged(&ev, d, acks)
		}
	}
	for name, v := range ev.Checks {
		v.Warnings = capWarnings(v.Warnings)
		ev.Checks[name] = v
	}
	ev.finish()
	return ev
}

// failedSource names a source the check cannot be decided without and that could not be read. A failed source is not an
// empty one: the check is neither run on it nor answered from a stored verdict. Besides the sources a check reads, the
// file list decides whether a path-gated check applies and what a scan gets.
func failedSource(d *data, c check) (string, bool) {
	needs := c.Reads
	if !c.When.Always || c.Stage == stageScan {
		needs = append([]string{"files"}, needs...)
	}
	for _, s := range needs {
		if _, bad := d.SourceErrors[s]; bad {
			return s, true
		}
	}
	return "", false
}

func decide(d *data, c check, env *ruleEnv) outcome {
	return builtinRules[c.Rule].run(d, params(c.With), env)
}

func (ev *evaluation) finish() {
	ev.Scans = nil
	ev.Result = resultPass
	pending := false
	names := make([]string, 0, len(ev.Checks))
	for n := range ev.Checks {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		switch ev.Checks[n].Result {
		case resultFail, resultError:
			ev.Result = resultFail
		case resultScan:
			ev.Scans = append(ev.Scans, n)
		case resultPending:
			pending = true
		}
	}
	switch {
	case ev.Result != resultPass:
	case len(ev.Scans) > 0:
		ev.Result = resultScan
	case pending:
		ev.Result = resultPending
	}
}

func (ev evaluation) cheapFailed(cfg config) bool {
	return slices.ContainsFunc(cfg.Checks, func(c check) bool {
		r := ev.Checks[c.Name].Result
		return c.Stage == stageCheap && (r == resultFail || r == resultError)
	})
}

var resultMark = map[string]string{resultPass: "PASS ", resultFail: "FAIL ", resultScan: "SCAN ", resultError: "ERROR", resultPending: "WAIT "}

func report(w io.Writer, cfg config, ev evaluation) {
	var open []string
	for _, c := range cfg.Checks {
		v := ev.Checks[c.Name]
		logf(w, "%s %-20s %s", resultMark[v.Result], c.Name, v.Reason)
	}
	for _, c := range cfg.Checks {
		for _, x := range ev.Checks[c.Name].Warnings {
			if x.Acknowledged {
				logf(w, "ACK   %-20s %s", c.Name, x.where())
				continue
			}
			logf(w, "WARN  %-20s %s", c.Name, x.where())
			if x.File != "" {
				open = append(open, ackEntry(c.Name, x))
			}
		}
	}
	if len(open) > 0 {
		logf(w, "to acknowledge a warning, add its entry to %s:\n%s", acknowledgedPath, strings.Join(open, "\n"))
	}
	for _, c := range cfg.Checks {
		v := ev.Checks[c.Name]
		if v.Result == resultFail || v.Result == resultError {
			logf(w, "::error title=pre-flight %s #%d::%s", c.Name, ev.PR, annotation(v.Reason))
		}
		for _, x := range v.Warnings {
			if !x.Acknowledged {
				logf(w, "::warning title=pre-flight %s #%d::%s", c.Name, ev.PR, annotation(x.where()))
			}
		}
	}
}

func (w warning) where() string {
	if w.File == "" {
		return w.Text
	}
	at := w.File
	if w.Line > 0 {
		at = fmt.Sprintf("%s:%d", w.File, w.Line)
	}
	s := at + " " + w.Text
	if w.Notice != "" {
		s += fmt.Sprintf(": %q", w.Notice)
	}
	if w.More > 0 {
		s += fmt.Sprintf(" (and %d more in this file)", w.More)
	}
	return strings.TrimSpace(s)
}

// GitHub refuses a comment over 65536 characters.
const (
	maxReason        = 1000
	maxNotice        = 200
	maxWarnedFiles   = 20
	maxCommentLength = 65536
)

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

func perFile(ws []warning) []warning {
	var out []warning
	at := map[string]int{}
	for _, w := range ws {
		if i, ok := at[w.File]; ok {
			out[i].More++
			continue
		}
		at[w.File] = len(out)
		w.Notice = clip(w.Notice, maxNotice)
		out = append(out, w)
	}
	return out
}

func capWarnings(ws []warning) []warning {
	var kept []warning
	hidden := 0
	for _, w := range ws {
		if w.File == "" {
			hidden += w.More
			continue
		}
		kept = append(kept, w)
	}
	hidden += max(len(kept)-maxWarnedFiles, 0)
	kept = kept[:min(len(kept), maxWarnedFiles):min(len(kept), maxWarnedFiles)]
	if hidden == 0 {
		return kept
	}
	return append(kept, warning{Text: fmt.Sprintf("and %d more file(s) with warnings", hidden), More: hidden})
}
