// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// commentMarker identifies the pre-flight's one comment on a PR, which every run updates.
const commentMarker = "<!-- pig-preflight -->"

// statusContext is the commit status the pre-flight sets on a PR's head.
const statusContext = "preflight"

var resultIcon = map[string]string{resultPass: "✅", resultFail: "❌", resultScan: "⏳", resultError: "⚠️"}

// markdown is the report for the step summary and the PR comment.
func markdown(cfg config, d *data, ev evaluation) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n### Pre-flight: %s %s\n\n", commentMarker, resultIcon[ev.Result], ev.Result)
	fmt.Fprintf(&b, "Head `%s`, base `%s`, mode %s (%s).\n\n", short(ev.Head), short(ev.Base), d.Mode, d.Reason)
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
	var failed []string
	passed := 0
	for _, c := range cfg.Checks {
		switch ev.Checks[c.Name].Result {
		case resultFail, resultError:
			failed = append(failed, c.Name)
		case resultPass:
			passed++
		}
	}
	switch ev.Result {
	case resultPass:
		state, description = "success", fmt.Sprintf("%d checks passed", passed)
	case resultScan:
		state, description = "pending", "scans still to run: "+strings.Join(ev.Scans, ", ")
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
	target := a.base + "/" + strings.TrimPrefix(path, "/")
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(raw))
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+a.token)
		a.mu.Lock()
		a.calls++
		a.mu.Unlock()
		resp, err := a.client.Do(req)
		status := 0
		var respBody []byte
		if err == nil {
			respBody, err = io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			_ = resp.Body.Close()
			status = resp.StatusCode
		}
		if err == nil && status >= 200 && status < 300 {
			if out != nil {
				return json.Unmarshal(respBody, out)
			}
			return nil
		}
		if err == nil {
			err = &httpError{path: path, status: status, body: strings.TrimSpace(string(respBody))}
		}
		if ctx.Err() != nil || attempt >= len(a.retryDelays) || !transient(status, errOrNil(status, err)) {
			return fmt.Errorf("%s %s: %w", method, path, err)
		}
		if a.log != nil {
			logf(a.log, "retry %d/%d of %s %s in %s: %v", attempt+1, len(a.retryDelays), method, path, a.retryDelays[attempt], err)
		}
		if err := sleep(ctx, a.retryDelays[attempt]); err != nil {
			return err
		}
	}
}

type recordOptions struct {
	apiBase, token string
	statePath      string
	dryRun         bool
}

// record writes the state for the next run, the step summary, the commit status and the PR's one pre-flight comment.
// With dryRun it writes the state and the summary and prints what it would send.
func record(ctx context.Context, cfg config, d *data, ev evaluation, o recordOptions, log io.Writer) error {
	st := state{Version: ev.Version, Head: ev.Head, Base: ev.Base, Checks: ev.Checks}
	if err := writeJSON(o.statePath, st); err != nil {
		return err
	}
	logf(log, "record: state for %s written to %s", short(ev.Head), o.statePath)
	report := markdown(cfg, d, ev)
	if summary := os.Getenv("GITHUB_STEP_SUMMARY"); summary != "" {
		f, err := os.OpenFile(summary, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		_, err = f.WriteString(report)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
	state, description := statusFor(cfg, ev)
	if o.dryRun {
		logf(log, "record (dry run): status %s %q on %s", state, description, short(ev.Head))
		logf(log, "record (dry run): comment\n%s", report)
		return nil
	}
	a := newAPI(o.apiBase, o.token)
	a.log = log
	prefix := "repos/" + d.Repo
	if err := a.send(ctx, http.MethodPost, fmt.Sprintf("%s/statuses/%s", prefix, ev.Head), map[string]string{"state": state, "description": description, "context": statusContext}, nil); err != nil {
		return err
	}
	logf(log, "record: status %s on %s", state, short(ev.Head))
	var comments []struct {
		ID   int64  `json:"id"`
		Body string `json:"body"`
	}
	if err := a.get(ctx, fmt.Sprintf("%s/issues/%d/comments?per_page=%d", prefix, d.PR, perPage), &comments, true); err != nil {
		return err
	}
	for _, c := range comments {
		if strings.HasPrefix(c.Body, commentMarker) {
			if c.Body == report {
				logf(log, "record: comment unchanged")
				return nil
			}
			if err := a.send(ctx, http.MethodPatch, fmt.Sprintf("%s/issues/comments/%d", prefix, c.ID), map[string]string{"body": report}, nil); err != nil {
				return err
			}
			logf(log, "record: comment %d updated", c.ID)
			return nil
		}
	}
	if err := a.send(ctx, http.MethodPost, fmt.Sprintf("%s/issues/%d/comments", prefix, d.PR), map[string]string{"body": report}, nil); err != nil {
		return err
	}
	logf(log, "record: comment posted")
	return nil
}
