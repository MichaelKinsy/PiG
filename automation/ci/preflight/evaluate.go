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

// evaluation is the outcome of every check for one PR head.
type evaluation struct {
	Repo    string             `json:"repo"`
	PR      int                `json:"pr"`
	Head    string             `json:"head"`
	Base    string             `json:"base"`
	Version string             `json:"version"`
	Engine  int                `json:"engine"`
	Result  string             `json:"result"`
	Checks  map[string]verdict `json:"checks"`
	// Scans are the checks a scan still has to decide.
	Scans []string `json:"scans"`
}

// sourceValue returns the part of the data a source name stands for, as it is hashed.
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

// cheapInputHash is the SHA-256 of a cheap check's definition and every source it reads, so an unchanged input and rule
// reuse the stored verdict and any change decides it again.
func cheapInputHash(d *data, c check) string {
	parts := map[string]any{"definition": c.definition}
	for _, s := range c.Reads {
		parts[s] = sourceValue(d, s)
	}
	raw, err := json.Marshal(parts)
	if err != nil {
		panic(err) // every value is plain decoded JSON
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// evaluate decides every cheap check from the data and marks the scans still to run. A check no changed file makes
// relevant passes; a check whose input hash equals the stored one keeps its stored pass or fail.
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
		switch {
		case d.Limit != "":
			v.Result, v.Reason = resultFail, "the PR is too large for the pre-flight: "+d.Limit
		case !slices.Contains(d.Relevant, c.Name):
			v.Result, v.Reason = resultPass, "no file it covers changed"
		case had && stored.Input == v.Input && (stored.Result == resultPass || stored.Result == resultFail):
			v = stored
		case c.Stage == stageScan:
			v.Result, v.Reason = resultScan, "waits for its scan"
		default:
			o := decide(d, c, env)
			v.Result, v.Reason, v.Warnings = o.result, o.reason, o.warnings
		}
		ev.Checks[c.Name] = v
	}
	if d.Acknowledged != "" {
		acks, err := parseAcknowledged(d.Acknowledged)
		if err != nil {
			// A broken acknowledgement file must not hide warnings, so the check that warns most reports it.
			v := ev.Checks["licenses"]
			v.Result, v.Reason = resultFail, err.Error()
			ev.Checks["licenses"] = v
		} else {
			applyAcknowledged(&ev, d, acks)
		}
	}
	ev.finish()
	return ev
}

// decide runs a cheap check's rule, or reports the source it reads that failed after its retries.
func decide(d *data, c check, env *ruleEnv) outcome {
	for _, s := range c.Reads {
		if msg, bad := d.SourceErrors[s]; bad {
			return outcome{result: resultError, reason: fmt.Sprintf("source %s failed after retries: %s", s, msg)}
		}
	}
	return builtinRules[c.Rule].run(d, params(c.With), env)
}

// finish derives the overall result: fail when any check failed or errored, scan while scans remain, pending while a
// check waits for the next run, else pass.
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

// cheapFailed reports whether a cheap check failed or errored, which keeps the scans from running.
func (ev evaluation) cheapFailed(cfg config) bool {
	return slices.ContainsFunc(cfg.Checks, func(c check) bool {
		r := ev.Checks[c.Name].Result
		return c.Stage == stageCheap && (r == resultFail || r == resultError)
	})
}

var resultMark = map[string]string{resultPass: "PASS ", resultFail: "FAIL ", resultScan: "SCAN ", resultError: "ERROR", resultPending: "WAIT "}

// report prints one line per check in config order, the warnings, the entries that would acknowledge them, and the
// GitHub annotations.
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
			open = append(open, ackEntry(c.Name, x))
		}
	}
	if len(open) > 0 {
		logf(w, "to acknowledge a warning, add its entry to %s:\n%s", acknowledgedPath, strings.Join(open, "\n"))
	}
	for _, c := range cfg.Checks {
		v := ev.Checks[c.Name]
		if v.Result == resultFail || v.Result == resultError {
			logf(w, "::error title=pre-flight %s #%d::%s", c.Name, ev.PR, strings.ReplaceAll(v.Reason, "\n", " "))
		}
		for _, x := range v.Warnings {
			if !x.Acknowledged {
				logf(w, "::warning title=pre-flight %s #%d::%s", c.Name, ev.PR, x.where())
			}
		}
	}
}

// where is a warning as one line: "file:line text: notice".
func (w warning) where() string {
	at := w.File
	if w.Line > 0 {
		at = fmt.Sprintf("%s:%d", w.File, w.Line)
	}
	return fmt.Sprintf("%s %s: %q", at, w.Text, w.Notice)
}
