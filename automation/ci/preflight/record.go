// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	statePath      string
	dryRun         bool
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

// record writes the state for the next run, the step summary, the commit status, the PR's one pre-flight comment and
// any alert. Status and comment are sent only when they differ from what the previous run sent. With dryRun it writes
// the state and the summary and prints what it would send.
func record(ctx context.Context, cfg config, d *data, ev evaluation, o recordOptions, log io.Writer) error {
	report := markdown(cfg, d, ev)
	status, description := statusFor(cfg, ev)
	sum := sha256.Sum256([]byte(status + "\x00" + description + "\x00" + report))
	st := state{Version: ev.Version, Engine: ev.Engine, Head: ev.Head, Base: ev.Base, Checks: ev.Checks, Sent: hex.EncodeToString(sum[:]), Checked: time.Now().UTC()}
	prev := d.Previous
	if prev != nil {
		st.Comment = prev.Comment
	}
	due, alerted := alertsFor(cfg, ev, prev)
	st.Alerted = alerted
	if summary := os.Getenv("GITHUB_STEP_SUMMARY"); summary != "" {
		// The step summary may already hold the other PRs' reports: this one is added after them.
		before, err := os.ReadFile(summary)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.WriteFile(summary, append(before, report...), 0o600); err != nil {
			return err
		}
	}
	if err := send(ctx, d, ev, o, &st, prev, report, status, description, due, log); err != nil {
		return err
	}
	if err := writeJSON(o.statePath, st); err != nil {
		return err
	}
	logf(log, "record: state for %s written to %s", short(ev.Head), o.statePath)
	return nil
}

// send posts the status, the comment and the alerts; st.Comment is set to the comment's id.
func send(ctx context.Context, d *data, ev evaluation, o recordOptions, st, prev *state, report, status, description string, due []check, log io.Writer) error {
	unchanged := prev != nil && prev.Sent == st.Sent
	if o.dryRun {
		if unchanged {
			logf(log, "record (dry run): status and comment unchanged, nothing to send")
		} else {
			logf(log, "record (dry run): status %s %q on %s", status, description, short(ev.Head))
			logf(log, "record (dry run): comment\n%s", report)
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
	if unchanged {
		logf(log, "record: status and comment unchanged, nothing sent")
		return nil
	}
	if err := a.send(ctx, http.MethodPost, fmt.Sprintf("%s/statuses/%s", prefix, ev.Head), map[string]string{"state": status, "description": description, "context": statusContext}, nil); err != nil {
		return err
	}
	logf(log, "record: status %s on %s", status, short(ev.Head))
	id, err := writeComment(ctx, a, prefix, d.PR, st.Comment, report, log)
	st.Comment = id
	return err
}

// writeComment updates the pre-flight's comment: by its stored id when there is one, else the one with the marker
// among the PR's comments, else a new one. It returns the comment's id.
func writeComment(ctx context.Context, a *api, prefix string, pr int, id int64, report string, log io.Writer) (int64, error) {
	body := map[string]string{"body": report}
	if id != 0 {
		err := a.send(ctx, http.MethodPatch, fmt.Sprintf("%s/issues/comments/%d", prefix, id), body, nil)
		if err == nil {
			logf(log, "record: comment %d updated", id)
			return id, nil
		}
		if !isNotFound(err) {
			return id, err
		}
		logf(log, "record: comment %d is gone, looking for another", id)
	}
	var comments []struct {
		ID   int64  `json:"id"`
		Body string `json:"body"`
	}
	if err := a.get(ctx, fmt.Sprintf("%s/issues/%d/comments?per_page=%d", prefix, pr, perPage), &comments, true); err != nil {
		return 0, err
	}
	for _, c := range comments {
		if !strings.HasPrefix(c.Body, commentMarker) {
			continue
		}
		if c.Body == report {
			logf(log, "record: comment %d unchanged", c.ID)
			return c.ID, nil
		}
		if err := a.send(ctx, http.MethodPatch, fmt.Sprintf("%s/issues/comments/%d", prefix, c.ID), body, nil); err != nil {
			return 0, err
		}
		logf(log, "record: comment %d updated", c.ID)
		return c.ID, nil
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := a.send(ctx, http.MethodPost, fmt.Sprintf("%s/issues/%d/comments", prefix, pr), body, &created); err != nil {
		return 0, err
	}
	logf(log, "record: comment %d posted", created.ID)
	return created.ID, nil
}
