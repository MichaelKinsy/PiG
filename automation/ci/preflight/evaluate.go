// SPDX-License-Identifier: MIT

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	Result  string             `json:"result"`
	Checks  map[string]verdict `json:"checks"`
	// Scans are the checks a scan step still has to decide.
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

// inputHash is the SHA-256 of the canonical JSON of everything a check reads, with its version, so an unchanged input
// and rule reuse the stored verdict and any change decides it again.
func inputHash(d *data, c check) string {
	parts := map[string]any{"check": c.Name, "version": c.Version}
	for _, s := range c.Sources {
		parts[s] = sourceValue(d, s)
	}
	raw, err := json.Marshal(parts)
	if err != nil {
		panic(err) // every value is plain decoded JSON
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func evaluate(cfg config, d *data) evaluation {
	ev := evaluation{Repo: d.Repo, PR: d.PR, Head: d.Pull.Head.SHA, Base: d.Pull.Base.SHA, Version: d.Version, Checks: map[string]verdict{}}
	var prev map[string]verdict
	if d.Previous != nil && d.Previous.Version == d.Version {
		prev = d.Previous.Checks
	}
	if d.Mode == modeReuse && prev != nil {
		ev.Checks = prev
		ev.finish()
		return ev
	}
	for _, c := range cfg.Checks {
		v := verdict{Input: inputHash(d, c)}
		switch o, failedSource := decide(d, c); {
		case d.Limit != "":
			v.Result, v.Reason = resultFail, "the PR is too large for the pre-flight: "+d.Limit
		case failedSource != "":
			v.Result, v.Reason = resultError, failedSource
		case !slices.Contains(d.Relevant, c.Name):
			v.Result, v.Reason = resultPass, "no file it covers changed"
		case prev != nil && prev[c.Name].Input == v.Input && (prev[c.Name].Result == resultPass || prev[c.Name].Result == resultFail):
			v = prev[c.Name]
		default:
			v.Result, v.Reason, v.Warnings = o.result, o.reason, o.warnings
		}
		ev.Checks[c.Name] = v
	}
	if d.Acknowledged != "" {
		acks, err := parseAcknowledged(d.Acknowledged)
		if err != nil {
			for _, name := range []string{"reuse", "gitleaks"} {
				if v, ok := ev.Checks[name]; ok && slices.Contains(d.Relevant, name) {
					v.Result, v.Reason = resultFail, err.Error()
					ev.Checks[name] = v
				}
			}
		} else {
			applyAcknowledged(&ev, d, acks)
		}
	}
	ev.finish()
	return ev
}

// decide runs a check's rule, or marks it for its scan step. failedSource names a source the check reads that failed
// after its retries.
func decide(d *data, c check) (outcome, string) {
	for _, s := range c.Sources {
		if msg, bad := d.SourceErrors[s]; bad {
			return outcome{}, fmt.Sprintf("source %s failed after retries: %s", s, msg)
		}
	}
	if rule, ok := rules[c.Name]; ok {
		return rule(d), ""
	}
	return scan("decided by its scan step"), ""
}

// finish derives the overall result: fail when any check failed or errored, scan while scans remain, else pass.
func (ev *evaluation) finish() {
	ev.Scans = nil
	ev.Result = resultPass
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
		}
	}
	if ev.Result == resultPass && len(ev.Scans) > 0 {
		ev.Result = resultScan
	}
}

var resultMark = map[string]string{resultPass: "PASS ", resultFail: "FAIL ", resultScan: "SCAN ", resultError: "ERROR"}

// report prints one line per check in config order, then the overall result.
func report(w io.Writer, cfg config, ev evaluation) {
	logf(w, "::group::evaluate PR #%d at %s", ev.PR, short(ev.Head))
	for _, c := range cfg.Checks {
		v := ev.Checks[c.Name]
		logf(w, "%s %-18s %s", resultMark[v.Result], c.Name, v.Reason)
	}
	var open []string
	for _, c := range cfg.Checks {
		for _, x := range ev.Checks[c.Name].Warnings {
			if x.Acknowledged {
				logf(w, "ACK   %-18s %s", c.Name, x.where())
				continue
			}
			logf(w, "WARN  %-18s %s", c.Name, x.where())
			open = append(open, ackEntry(c.Name, x))
		}
	}
	logf(w, "::endgroup::")
	if len(open) > 0 {
		logf(w, "::group::to acknowledge a warning, add its entry to %s", acknowledgedPath)
		logf(w, "%s", strings.Join(open, "\n"))
		logf(w, "::endgroup::")
	}
	for _, c := range cfg.Checks {
		v := ev.Checks[c.Name]
		if v.Result == resultFail || v.Result == resultError {
			logf(w, "::error title=pre-flight %s::%s", c.Name, strings.ReplaceAll(v.Reason, "\n", " "))
		}
		for _, x := range v.Warnings {
			if !x.Acknowledged {
				logf(w, "::warning title=pre-flight %s::%s", c.Name, x.where())
			}
		}
	}
	switch ev.Result {
	case resultFail:
		logf(w, "pre-flight: FAIL, scans skipped")
	case resultScan:
		logf(w, "pre-flight: cheap checks pass; scans to run: %s", strings.Join(ev.Scans, ", "))
	default:
		logf(w, "pre-flight: PASS")
	}
}

// readLicenses lists the license identifiers of LICENSES/ under root.
func readLicenses(root string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(root, "LICENSES"))
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if name, ok := strings.CutSuffix(e.Name(), ".txt"); ok {
			ids = append(ids, name)
		}
	}
	return ids, nil
}

func runEvaluate(cfg config, args []string, stdout, stderr io.Writer) int {
	in, out, root := "preflight-data.json", "preflight-evaluation.json", "."
	for i := 0; i+1 < len(args); i += 2 {
		switch args[i] {
		case "--data":
			in = args[i+1]
		case "--out":
			out = args[i+1]
		case "--root":
			root = args[i+1]
		default:
			_, _ = fmt.Fprintf(stderr, "preflight evaluate: unknown flag %s\n", args[i])
			return 2
		}
	}
	raw, err := os.ReadFile(in)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "preflight evaluate:", err)
		return 2
	}
	var d data
	if err := json.Unmarshal(raw, &d); err != nil {
		_, _ = fmt.Fprintf(stderr, "preflight evaluate: %s: %v\n", in, err)
		return 2
	}
	if d.Licenses, err = readLicenses(root); err != nil {
		_, _ = fmt.Fprintln(stderr, "preflight evaluate: main's licenses:", err)
		return 2
	}
	ev := evaluate(cfg, &d)
	report(stdout, cfg, ev)
	if err := writeJSON(out, ev); err != nil {
		_, _ = fmt.Fprintln(stderr, "preflight evaluate:", err)
		return 2
	}
	if ev.Result == resultFail {
		return 1
	}
	return 0
}

// where is a warning as one line: "file:line text: notice".
func (w warning) where() string {
	at := w.File
	if w.Line > 0 {
		at = fmt.Sprintf("%s:%d", w.File, w.Line)
	}
	return fmt.Sprintf("%s %s: %q", at, w.Text, w.Notice)
}
