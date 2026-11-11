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
)

const commentMarker = "<!-- pig-preflight -->"

const statusContext = "preflight"

var resultIcon = map[string]string{resultPass: "✅", resultFail: "❌", resultScan: "⏳", resultError: "⚠️", resultPending: "⏳"}

func markdown(cfg config, d *data, ev evaluation) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n### Pre-flight: %s %s\n\n", commentMarker, resultIcon[ev.Result], ev.Result)
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
			if w.File != "" {
				acks = append(acks, ackEntry(c.Name, w))
			}
		}
	}
	if len(acks) > 0 {
		fmt.Fprintf(&b, "\n<details><summary>To acknowledge a warning, add its entry to <code>%s</code></summary>\n\n```toml\n%s```\n</details>\n", acknowledgedPath, strings.Join(acks, "\n"))
	}
	return b.String()
}

func cell(s string) string {
	return strings.NewReplacer("|", `\|`, "\n", " ").Replace(s)
}

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
	comment        prComment
}

// O_APPEND: the PRs of a run write the summary at the same time.
func appendFile(path, text string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = f.WriteString(text)
	return errors.Join(err, f.Close())
}

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

func record(ctx context.Context, cfg config, d *data, ev evaluation, o recordOptions, log io.Writer) error {
	report := markdown(cfg, d, ev)
	status, description := statusFor(cfg, ev)
	prev := d.Previous
	st := state{Version: ev.Version, Engine: ev.Engine, Head: ev.Head, Base: ev.Base, Checks: ev.Checks, Body: bodyHash(d.Pull.Body), Status: status + " " + description}
	due, alerted := alertsFor(cfg, ev, prev)
	st.Alerted = alerted
	setStatus := prev == nil || prev.Head != st.Head || prev.Status != st.Status
	body, bodyErr := commentBody(cfg, d, ev, report, st)
	writeBody := bodyErr == nil && body != o.comment.Body
	summary := func() error {
		if path := os.Getenv("GITHUB_STEP_SUMMARY"); path != "" {
			return appendFile(path, report)
		}
		return nil
	}
	if o.dryRun {
		if err := summary(); err != nil {
			return err
		}
		if setStatus {
			logf(log, "record (dry run): status %s %q on %s", status, description, short(ev.Head))
		}
		if writeBody {
			shown, _, _ := strings.Cut(body, stateOpen)
			logf(log, "record (dry run): comment\n%s", shown)
		}
		if !setStatus && !writeBody && bodyErr == nil {
			logf(log, "record (dry run): nothing changed, nothing to send")
		}
		for _, c := range due {
			logf(log, "record (dry run): alert\n%s", alertBody(c, d, ev))
		}
		return bodyErr
	}
	a := newAPI(o.apiBase, o.token)
	a.log = log
	prefix := "repos/" + d.Repo
	// The status first: it gates the PR, so nothing after it may keep it from being set.
	if setStatus {
		if err := a.send(ctx, http.MethodPost, fmt.Sprintf("%s/statuses/%s", prefix, ev.Head), map[string]string{"state": status, "description": description, "context": statusContext}, nil); err != nil {
			return err
		}
		logf(log, "record: status %s on %s", status, short(ev.Head))
	}
	if err := errors.Join(summary(), bodyErr); err != nil {
		return err
	}
	for _, c := range due {
		if err := a.send(ctx, http.MethodPost, fmt.Sprintf("%s/issues/%d/comments", prefix, d.PR), map[string]string{"body": alertBody(c, d, ev)}, nil); err != nil {
			return err
		}
		logf(log, "record: alert for %s posted", c.Name)
	}
	if !writeBody {
		if !setStatus {
			logf(log, "record: nothing changed, nothing sent")
		}
		return nil
	}
	return writeComment(ctx, a, prefix, d.PR, o.comment.ID, body, log)
}

const shortened = "\nThe warnings are left out: with them the report is too long for a comment. The run's log and step summary hold them.\n"

func fits(body string) bool { return len(body) <= maxCommentLength }

func commentBody(cfg config, d *data, ev evaluation, report string, st state) (string, error) {
	body := withState(report, st)
	if fits(body) {
		return body, nil
	}
	bare := ev
	bare.Checks = map[string]verdict{}
	for name, v := range ev.Checks {
		v.Warnings = nil
		bare.Checks[name] = v
	}
	st.Checks = bare.Checks
	body = withState(markdown(cfg, d, bare)+shortened, st)
	if !fits(body) {
		return "", fmt.Errorf("the comment would be %d characters even without its warnings, more than the %d GitHub takes", len(body), maxCommentLength)
	}
	return body, nil
}

func writeComment(ctx context.Context, a *api, prefix string, pr int, id int64, body string, log io.Writer) error {
	payload := map[string]string{"body": body}
	if id != 0 {
		err := a.send(ctx, http.MethodPatch, fmt.Sprintf("%s/issues/comments/%d", prefix, id), payload, nil)
		if err == nil {
			logf(log, "record: comment %d updated", id)
			return nil
		}
		// Only a 404 means the comment is gone; a 422 is a body GitHub refuses.
		if !hasStatus(err, http.StatusNotFound) {
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
