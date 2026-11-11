// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// issueMarker identifies the one issue the verify job keeps open while the pre-flight left PRs without a verdict.
const issueMarker = "<!-- pig-preflight-verify -->"

const issueTitle = "Pre-flight: PRs without a verdict"

// missing is a PR the pre-flight did not deliver for, and what is missing.
type missing struct {
	PR   int
	Head string
	What string
}

type commitStatus struct {
	Context string `json:"context"`
}

// where names the PR and head of a missing line; a line for the whole run has neither.
func (m missing) where() string {
	switch {
	case m.PR == 0:
		return "the run"
	case m.Head == "":
		return fmt.Sprintf("#%d", m.PR)
	}
	return fmt.Sprintf("#%d `%s`", m.PR, short(m.Head))
}

// verifyHandled checks that every PR the jobs of a run reported has what the pre-flight owes it: a "preflight" status
// on the head it judged and its comment with the state for that head. A PR a job could not run for, and a PR the
// first job passed on that no next job reported, are missing too. A PR deferred by a next job's time budget is left
// for the next run.
func verifyHandled(ctx context.Context, a *api, repo, bot string, hs []handled) ([]missing, error) {
	byPR := map[int][]handled{}
	for _, h := range hs {
		byPR[h.PR] = append(byPR[h.PR], h)
	}
	prs := make([]int, 0, len(byPR))
	for pr := range byPR {
		prs = append(prs, pr)
	}
	slices.Sort(prs)
	var out []missing
	for _, pr := range prs {
		entries := byPR[pr]
		i := slices.IndexFunc(entries, func(h handled) bool { return h.Result != resultDeferred })
		if i < 0 {
			if !slices.ContainsFunc(entries, func(h handled) bool { return !h.PassedOn }) {
				out = append(out, missing{PR: pr, Head: entries[0].Head, What: "passed on to a next job, which did not report it"})
			}
			continue
		}
		h := entries[i]
		if h.Error != "" {
			out = append(out, missing{PR: pr, Head: h.Head, What: "the pre-flight could not run: " + clip(h.Error, maxReason)})
			continue
		}
		var statuses []commitStatus
		if err := a.get(ctx, fmt.Sprintf("repos/%s/commits/%s/statuses?per_page=%d", repo, h.Head, perPage), &statuses, true); err != nil {
			return nil, err
		}
		var what []string
		if !slices.ContainsFunc(statuses, func(s commitStatus) bool { return s.Context == statusContext }) {
			what = append(what, "no "+statusContext+" status")
		}
		c, err := loadComment(ctx, a, repo, pr, bot)
		switch {
		case err != nil:
			return nil, err
		case c.ID == 0:
			what = append(what, "no pre-flight comment")
		case c.State == nil || c.State.Head != h.Head:
			what = append(what, "the comment holds no state for this head")
		}
		if len(what) > 0 {
			out = append(out, missing{PR: pr, Head: h.Head, What: strings.Join(what, ", ")})
		}
	}
	return out, nil
}

// issueBody lists the PRs without a verdict.
func issueBody(ms []missing, runURL string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\nThe pre-flight left these PRs without a verdict. The PRs are not at fault: the pre-flight is. This issue closes itself once every PR has one again.\n\n", issueMarker)
	if runURL != "" {
		fmt.Fprintf(&b, "Run: %s\n\n", runURL)
	}
	b.WriteString("| PR | What is missing |\n|---|---|\n")
	for _, m := range ms {
		fmt.Fprintf(&b, "| %s | %s |\n", m.where(), cell(m.What))
	}
	return b.String()
}

// keepIssue opens or updates the verify issue while PRs lack a verdict, and closes it once none does.
func keepIssue(ctx context.Context, a *api, repo, bot string, ms []missing, runURL string, log io.Writer) error {
	var issues []struct {
		Number      int             `json:"number"`
		Body        string          `json:"body"`
		PullRequest json.RawMessage `json:"pull_request"`
	}
	if err := a.get(ctx, fmt.Sprintf("repos/%s/issues?state=open&creator=%s&per_page=%d", repo, url.QueryEscape(bot), perPage), &issues, true); err != nil {
		return err
	}
	open := 0
	var body string
	for _, i := range issues {
		if i.PullRequest == nil && strings.HasPrefix(i.Body, issueMarker) {
			open, body = i.Number, i.Body
			break
		}
	}
	switch {
	case len(ms) == 0 && open == 0:
		return nil
	case len(ms) == 0:
		if err := a.send(ctx, http.MethodPost, fmt.Sprintf("repos/%s/issues/%d/comments", repo, open), map[string]string{"body": "Every PR has a verdict again. " + runURL}, nil); err != nil {
			return err
		}
		if err := a.send(ctx, http.MethodPatch, fmt.Sprintf("repos/%s/issues/%d", repo, open), map[string]string{"state": "closed"}, nil); err != nil {
			return err
		}
		logf(log, "verify: issue #%d closed", open)
		return nil
	}
	want := issueBody(ms, runURL)
	if open == 0 {
		var created struct {
			Number int `json:"number"`
		}
		if err := a.send(ctx, http.MethodPost, fmt.Sprintf("repos/%s/issues", repo), map[string]string{"title": issueTitle, "body": want}, &created); err != nil {
			return err
		}
		logf(log, "verify: issue #%d opened", created.Number)
		return nil
	}
	if body != want {
		if err := a.send(ctx, http.MethodPatch, fmt.Sprintf("repos/%s/issues/%d", repo, open), map[string]string{"body": want}, nil); err != nil {
			return err
		}
	}
	logf(log, "verify: issue #%d updated", open)
	return nil
}

// readHandled reads every job's report in dir.
func readHandled(dir string) ([]handled, error) {
	names, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, errors.New("no job reported what it did: " + dir + " holds no report")
	}
	var all []handled
	for _, n := range names {
		raw, err := os.ReadFile(n)
		if err != nil {
			return nil, err
		}
		var hs []handled
		if err := json.Unmarshal(raw, &hs); err != nil {
			return nil, fmt.Errorf("%s: %w", n, err)
		}
		all = append(all, hs...)
	}
	return all, nil
}

// runVerify is `preflight verify`: exit 0 when every PR has its verdict, 1 when some PR lacks it (the issue says
// which), 2 when the check itself could not run.
func runVerify(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	repo := fs.String("repo", os.Getenv("GITHUB_REPOSITORY"), "owner/repo")
	dir := fs.String("handled", "", "the directory with every job's --handled report")
	apiBase := fs.String("api", "https://api.github.com", "GitHub API base URL")
	bot := fs.String("bot", defaultBot, "the account the pre-flight comments as")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	tok := token()
	if *repo == "" || *dir == "" || tok == "" {
		_, _ = fmt.Fprintln(stderr, "preflight verify: --repo, --handled and GITHUB_TOKEN or GH_TOKEN are required")
		return 2
	}
	runURL := ""
	if id := os.Getenv("GITHUB_RUN_ID"); id != "" {
		runURL = fmt.Sprintf("%s/%s/actions/runs/%s", os.Getenv("GITHUB_SERVER_URL"), *repo, id)
	}
	a := newAPI(*apiBase, tok)
	a.log = stdout
	// A run whose jobs reported nothing is one missing line of its own.
	hs, err := readHandled(*dir)
	var ms []missing
	if err != nil {
		ms = []missing{{What: err.Error()}}
	} else if ms, err = verifyHandled(ctx, a, *repo, *bot, hs); err != nil {
		_, _ = fmt.Fprintln(stderr, "preflight verify:", err)
		return 2
	}
	for _, m := range ms {
		logf(stdout, "::error title=pre-flight verify::%s: %s", strings.ReplaceAll(m.where(), "`", ""), m.What)
	}
	if err := keepIssue(ctx, a, *repo, *bot, ms, runURL, stdout); err != nil {
		_, _ = fmt.Fprintln(stderr, "preflight verify:", err)
		return 2
	}
	if len(ms) > 0 {
		logf(stdout, "verify: %d PR(s) without a verdict", len(ms))
		return 1
	}
	logf(stdout, "verify: every PR of the run has its verdict (%d)", len(hs))
	return 0
}
