// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"
)

// commentMarker identifies the pre-flight's one comment on a PR, which every run updates.
const commentMarker = "<!-- pig-preflight -->"

// statusContext is the commit status the pre-flight sets on a PR's head.
const statusContext = "preflight"

var resultIcon = map[string]string{resultPass: "✅", resultFail: "❌", resultScan: "⏳", resultError: "⚠️", resultPending: "⏳"}

// markdown is the report for the step summary and the PR comment.
func markdown(cfg config, d *data, ev evaluation) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n### Pre-flight: %s %s\n\n", commentMarker, resultIcon[ev.Result], ev.Result)
	// Only what a verdict depends on: a run that only moved the base or reused its state sends nothing new.
	fmt.Fprintf(&b, "Head `%s`.\n\n", short(ev.Head))
	b.WriteString("| | Check | Result |\n|---|---|---|\n")
	var acks []string
	for _, c := range cfg.Checks {
		v := ev.Checks[c.Name]
		fmt.Fprintf(&b, "| %s | %s | %s |\n", resultIcon[v.Result], c.Name, cell(v.Reason))
		for _, w := range v.Warnings {
			if w.Acknowledged {
				fmt.Fprintf(&b, "| ☑️ | %s | acknowledged: %s |\n", c.Name, cell(w.where()))
				continue
			}
			fmt.Fprintf(&b, "| 🔶 | %s | %s |\n", c.Name, cell(w.where()))
			acks = append(acks, ackEntry(c.Name, w))
		}
	}
	if len(acks) > 0 {
		fmt.Fprintf(&b, "\n<details><summary>To acknowledge a warning, add its entry to <code>%s</code></summary>\n\n```toml\n%s```\n</details>\n", acknowledgedPath, strings.Join(acks, "\n"))
	}
	return b.String()
}

// cell keeps a reason inside one Markdown table cell.
func cell(s string) string {
	return strings.NewReplacer("|", `\|`, "\n", " ").Replace(s)
}

// statusFor maps the evaluation to a commit status state and a description of at most 140 characters.
func statusFor(cfg config, ev evaluation) (state, description string) {
	var failed, waiting []string
	passed := 0
	for _, c := range cfg.Checks {
		switch ev.Checks[c.Name].Result {
		case resultFail, resultError:
			failed = append(failed, c.Name)
		case resultPending:
			waiting = append(waiting, c.Name)
		case resultPass:
			passed++
		}
	}
	switch ev.Result {
	case resultPass:
		state, description = "success", fmt.Sprintf("%d checks passed", passed)
	case resultScan:
		state, description = "pending", "scans still to run: "+strings.Join(ev.Scans, ", ")
	case resultPending:
		state, description = "pending", "waits for the next run: "+strings.Join(waiting, ", ")
	default:
		state, description = "failure", fmt.Sprintf("%d failed: %s", len(failed), strings.Join(failed, ", "))
	}
	if len(description) > 140 {
		description = description[:137] + "..."
	}
	return state, description
}

// send writes to the GitHub API: POST or PATCH a JSON body, with the same retries as reads.
func (a *api) send(ctx context.Context, method, path string, body, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	r, err := a.call(ctx, method, a.base+"/"+strings.TrimPrefix(path, "/"), method+" "+path, raw, 1<<20)
	if err != nil || out == nil {
		return err
	}
	return json.Unmarshal(r.body, out)
}

type recordOptions struct {
	apiBase, token string
	dryRun         bool
	// comment is the pre-flight's comment as the run found it; its ID is 0 when there is none yet.
	comment prComment
}

// appendFile adds text at the end of path in one write: the PRs of a run record at the same time, and O_APPEND keeps
// one report from overwriting another.
func appendFile(path, text string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = f.WriteString(text)
	return errors.Join(err, f.Close())
}

// alertsFor returns the checks with an alert that fail now and were not alerted since they last passed, and the
// alerted list for the state.
func alertsFor(cfg config, ev evaluation, prev *state) (due []check, alerted []string) {
	var before []string
	if prev != nil {
		before = prev.Alerted
	}
	for _, c := range cfg.Checks {
		if c.Alert == "" || ev.Checks[c.Name].Result != resultFail {
			continue
		}
		alerted = append(alerted, c.Name)
		if !slices.Contains(before, c.Name) {
			due = append(due, c)
		}
	}
	return due, alerted
}

func alertBody(c check, d *data, ev evaluation) string {
	return fmt.Sprintf("@%s %s\n\n%s on `%s`: %s", d.Pull.User.Login, c.Alert, c.Name, short(ev.Head), ev.Checks[c.Name].Reason)
}

// record writes the step summary, the commit status, the PR's one pre-flight comment with the state for the next run in
// it, and any alert. The status is set only when it differs from the one the previous run set on the same head, the
// comment only when its text differs. A reused run keeps the previous time it was checked, so an unchanged PR is not
// written at all. With dryRun it writes the summary and prints what it would send.
func record(ctx context.Context, cfg config, d *data, ev evaluation, o recordOptions, log io.Writer) error {
	report := markdown(cfg, d, ev)
	status, description := statusFor(cfg, ev)
	prev := d.Previous
	st := state{Version: ev.Version, Engine: ev.Engine, Head: ev.Head, Base: ev.Base, Checks: ev.Checks, Body: bodyHash(d.Pull.Body), Status: status + " " + description, Checked: time.Now().UTC()}
	if d.Mode == modeReuse && prev != nil {
		st.Checked = prev.Checked
	}
	due, alerted := alertsFor(cfg, ev, prev)
	st.Alerted = alerted
	if summary := os.Getenv("GITHUB_STEP_SUMMARY"); summary != "" {
		if err := appendFile(summary, report); err != nil {
			return err
		}
	}
	setStatus := prev == nil || prev.Head != st.Head || prev.Status != st.Status
	body := withState(report, st)
	writeBody := body != o.comment.Body
	if o.dryRun {
		if setStatus {
			logf(log, "record (dry run): status %s %q on %s", status, description, short(ev.Head))
		}
		if writeBody {
			logf(log, "record (dry run): comment\n%s", report)
		}
		if !setStatus && !writeBody {
			logf(log, "record (dry run): nothing changed, nothing to send")
		}
		for _, c := range due {
			logf(log, "record (dry run): alert\n%s", alertBody(c, d, ev))
		}
		return nil
	}
	a := newAPI(o.apiBase, o.token)
	a.log = log
	prefix := "repos/" + d.Repo
	for _, c := range due {
		if err := a.send(ctx, http.MethodPost, fmt.Sprintf("%s/issues/%d/comments", prefix, d.PR), map[string]string{"body": alertBody(c, d, ev)}, nil); err != nil {
			return err
		}
		logf(log, "record: alert for %s posted", c.Name)
	}
	if setStatus {
		if err := a.send(ctx, http.MethodPost, fmt.Sprintf("%s/statuses/%s", prefix, ev.Head), map[string]string{"state": status, "description": description, "context": statusContext}, nil); err != nil {
			return err
		}
		logf(log, "record: status %s on %s", status, short(ev.Head))
	}
	if !writeBody {
		if !setStatus {
			logf(log, "record: nothing changed, nothing sent")
		}
		return nil
	}
	return writeComment(ctx, a, prefix, d.PR, o.comment.ID, body, log)
}

// writeComment updates the pre-flight's comment, or posts it when there is none or it was deleted.
func writeComment(ctx context.Context, a *api, prefix string, pr int, id int64, body string, log io.Writer) error {
	payload := map[string]string{"body": body}
	if id != 0 {
		err := a.send(ctx, http.MethodPatch, fmt.Sprintf("%s/issues/comments/%d", prefix, id), payload, nil)
		if err == nil {
			logf(log, "record: comment %d updated", id)
			return nil
		}
		if !isNotFound(err) {
			return err
		}
		logf(log, "record: comment %d is gone, posting it again", id)
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := a.send(ctx, http.MethodPost, fmt.Sprintf("%s/issues/%d/comments", prefix, pr), payload, &created); err != nil {
		return err
	}
	logf(log, "record: comment %d posted", created.ID)
	return nil
}
