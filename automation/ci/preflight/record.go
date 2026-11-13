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

// send takes a POST as not repeatable: it creates a comment, and a second one would be a duplicate. A PATCH sets a
// comment's whole body, so it is repeatable.
func (a *api) send(ctx context.Context, method, path string, body, out any) error {
	return a.sendWhen(ctx, method, path, method != http.MethodPost, body, out)
}

func (a *api) sendWhen(ctx context.Context, method, path string, repeatable bool, body, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	r, err := a.call(ctx, method, a.base+"/"+strings.TrimPrefix(path, "/"), method+" "+path, repeatable, raw, 1<<20)
	if err != nil || out == nil {
		return err
	}
	return json.Unmarshal(r.body, out)
}

// setStatus posts the commit status. GitHub keeps one current status per head and context, so the same state posted
// twice changes nothing: unlike a comment's POST, this one is repeated after a network error, a timeout or a 5xx, as
// well as after a 429. Any other 4xx is an answer and ends the call at once.
//
// The bound is the one of every call here: len(defaultRetryDelays)+1 = 4 attempts of at most apiTimeout (30 s) each,
// 2, 4 and 6 s apart, 132 s at worst. That is inside the 5 minutes the job's timeout-minutes (30) leaves after the
// run's --time-budget (25m) in preflight.yml, and inside the 900 s wait-preflight.sh waits for the verdict: a status
// that needed every attempt still reaches the waiting CI jobs. A cancelled context ends the wait between two attempts.
func (a *api) setStatus(ctx context.Context, prefix, head, state, description string) error {
	body := map[string]string{"state": state, "description": description, "context": statusContext}
	return a.sendWhen(ctx, http.MethodPost, fmt.Sprintf("%s/statuses/%s", prefix, head), true, body, nil)
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

// alertsFor returns the alerts to post and the checks that count as alerted. An alert is due when its check fails, the
// previous state does not list it, and no alert comment for the check and this head is on the PR.
func alertsFor(cfg config, ev evaluation, prev *state, posted []string) (due []check, alerted []string) {
	var before []string
	if prev != nil {
		before = prev.Alerted
	}
	for _, c := range cfg.Checks {
		if c.Alert == "" || ev.Checks[c.Name].Result != resultFail {
			continue
		}
		alerted = append(alerted, c.Name)
		if !slices.Contains(before, c.Name) && !slices.Contains(posted, alertMark(c.Name, ev.Head)) {
			due = append(due, c)
		}
	}
	return due, alerted
}

func alertBody(c check, d *data, ev evaluation) string {
	return fmt.Sprintf("@%s %s\n\n%s on `%s`: %s\n\n%s", d.Pull.User.Login, c.Alert, c.Name, short(ev.Head), ev.Checks[c.Name].Reason, alertMark(c.Name, ev.Head))
}

func record(ctx context.Context, cfg config, d *data, ev evaluation, o recordOptions, log io.Writer) error {
	r := newRecording(cfg, d, ev, o.comment)
	if o.dryRun {
		return r.show(log)
	}
	return r.send(ctx, o, log)
}

type recording struct {
	d                   *data
	ev                  evaluation
	report, body        string
	status, description string
	bodyErr             error
	setStatus           bool
	writeBody           bool
	due                 []check
	commentID           int64
}

func newRecording(cfg config, d *data, ev evaluation, comment prComment) recording {
	r := recording{d: d, ev: ev, report: markdown(cfg, d, ev), commentID: comment.ID}
	r.status, r.description = statusFor(cfg, ev)
	st := state{Version: ev.Version, Engine: ev.Engine, Head: ev.Head, Base: ev.Base, Checks: ev.Checks, Body: bodyHash(d.Pull.Body), Status: r.status + " " + r.description}
	r.due, st.Alerted = alertsFor(cfg, ev, d.Previous, comment.Alerts)
	prev := d.Previous
	r.setStatus = prev == nil || prev.Head != st.Head || prev.Status != st.Status
	r.body, r.bodyErr = commentBody(cfg, d, ev, r.report, st)
	r.writeBody = r.bodyErr == nil && r.body != comment.Body
	return r
}

func (r recording) summary() error {
	if path := os.Getenv("GITHUB_STEP_SUMMARY"); path != "" {
		return appendFile(path, r.report)
	}
	return nil
}

func (r recording) show(log io.Writer) error {
	if err := r.summary(); err != nil {
		return err
	}
	if r.setStatus {
		logf(log, "record (dry run): status %s %q on %s", r.status, r.description, short(r.ev.Head))
	}
	if r.writeBody {
		shown, _, _ := strings.Cut(r.body, stateOpen)
		logf(log, "record (dry run): comment\n%s", shown)
	}
	if !r.setStatus && !r.writeBody && r.bodyErr == nil {
		logf(log, "record (dry run): nothing changed, nothing to send")
	}
	for _, c := range r.due {
		logf(log, "record (dry run): alert\n%s", alertBody(c, r.d, r.ev))
	}
	return r.bodyErr
}

func (r recording) send(ctx context.Context, o recordOptions, log io.Writer) error {
	a := newAPI(o.apiBase, o.token)
	a.log = log
	return r.sendVia(ctx, a, log)
}

func (r recording) sendVia(ctx context.Context, a *api, log io.Writer) error {
	prefix := "repos/" + r.d.Repo
	// The status first: it gates the PR, so nothing after it may keep it from being set.
	if r.setStatus {
		if err := a.setStatus(ctx, prefix, r.ev.Head, r.status, r.description); err != nil {
			return err
		}
		logf(log, "record: status %s on %s", r.status, short(r.ev.Head))
	}
	if err := errors.Join(r.summary(), r.bodyErr); err != nil {
		return err
	}
	for _, c := range r.due {
		if err := a.send(ctx, http.MethodPost, fmt.Sprintf("%s/issues/%d/comments", prefix, r.d.PR), map[string]string{"body": alertBody(c, r.d, r.ev)}, nil); err != nil {
			return err
		}
		logf(log, "record: alert for %s posted", c.Name)
	}
	if r.writeBody {
		return writeComment(ctx, a, prefix, r.d.PR, r.commentID, r.body, log)
	}
	if !r.setStatus {
		logf(log, "record: nothing changed, nothing sent")
	}
	return nil
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
