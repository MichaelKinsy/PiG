// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const issueMarker = "<!-- pig-preflight-verify -->"

const issueTitle = "Pre-flight: PRs without a verdict"

const firstReport = "preflight-handled-0.json"

type missing struct {
	PR   int
	Head string
	What string
}

type commitStatus struct {
	Context string `json:"context"`
}

func (m missing) where() string {
	switch {
	case m.PR == 0:
		return "the run"
	case m.Head == "":
		return fmt.Sprintf("#%d", m.PR)
	}
	return fmt.Sprintf("#%d `%s`", m.PR, short(m.Head))
}

func verifyHandled(ctx context.Context, a *api, repo, bot string, hs []handled) []missing {
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
			if slices.ContainsFunc(entries, func(h handled) bool { return !h.PassedOn }) {
				continue
			}
			what := "passed on to a next job, which did not report it"
			if head := entries[0].Head; head != "" {
				what = verifyPR(ctx, a, repo, bot, entries[0])
			}
			if what != "" {
				out = append(out, missing{PR: pr, Head: entries[0].Head, What: what})
			}
			continue
		}
		h := entries[i]
		if h.Error != "" {
			out = append(out, missing{PR: pr, Head: h.Head, What: "the pre-flight could not run: " + clip(h.Error, maxReason)})
			continue
		}
		if what := verifyPR(ctx, a, repo, bot, h); what != "" {
			out = append(out, missing{PR: pr, Head: h.Head, What: what})
		}
	}
	return out
}

func verifyPR(ctx context.Context, a *api, repo, bot string, h handled) string {
	var statuses []commitStatus
	err := a.get(ctx, fmt.Sprintf("repos/%s/commits/%s/statuses?per_page=%d", repo, h.Head, perPage), &statuses, true)
	switch {
	case hasStatus(err, http.StatusNotFound) || hasStatus(err, http.StatusUnprocessableEntity):
		// 404 or 422: GitHub no longer knows the head (a force-push). An existing head without a status answers [], so a real shortfall is never skipped.
		if a.log != nil {
			logf(a.log, "verify: PR #%d: head %s is gone, its new head is judged next run", h.PR, short(h.Head))
		}
		return ""
	case err != nil:
		return "could not be checked: " + clip(err.Error(), maxReason)
	}
	var what []string
	if !slices.ContainsFunc(statuses, func(s commitStatus) bool { return s.Context == statusContext }) {
		what = append(what, "no "+statusContext+" status")
	}
	c, err := loadComment(ctx, a, repo, h.PR, bot)
	switch {
	case err != nil:
		what = append(what, "its comment could not be checked: "+clip(err.Error(), maxReason))
	case c.ID == 0:
		what = append(what, "no pre-flight comment")
	case c.State == nil || c.State.Head != h.Head:
		what = append(what, "the comment holds no state for this head")
	}
	return strings.Join(what, ", ")
}

func issueText(s string) string {
	return "<code>" + strings.NewReplacer("|", "&#124;", "\n", " ", "\r", " ").Replace(html.EscapeString(s)) + "</code>"
}

func issueBody(ms []missing, runURL string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\nThe pre-flight left these PRs without a verdict. The PRs are not at fault: the pre-flight is. This issue closes itself once every PR has one again.\n\n", issueMarker)
	if runURL != "" {
		fmt.Fprintf(&b, "Run: %s\n\n", runURL)
	}
	b.WriteString("| PR | What is missing |\n|---|---|\n")
	for _, m := range ms {
		fmt.Fprintf(&b, "| %s | %s |\n", m.where(), issueText(m.What))
	}
	return b.String()
}

func sameList(a, b string) bool {
	table := func(s string) string {
		if _, rest, ok := strings.Cut(s, "| PR |"); ok {
			return rest
		}
		return s
	}
	return table(a) == table(b)
}

type foundIssue struct {
	Number int    `json:"number"`
	Body   string `json:"body"`
	State  string `json:"state"`
	User   struct {
		Login string `json:"login"`
	} `json:"user"`
}

// Search trails a new issue: a second one made meanwhile is closed as a duplicate by the next run.
func findIssues(ctx context.Context, a *api, repo, bot string) ([]foundIssue, error) {
	q := fmt.Sprintf("repo:%s is:issue in:title %q", repo, issueTitle)
	if name, ok := strings.CutSuffix(bot, "[bot]"); ok {
		q += " author:app/" + name
	} else {
		q += " author:" + bot
	}
	var result struct {
		Items []foundIssue `json:"items"`
	}
	if err := a.get(ctx, fmt.Sprintf("search/issues?q=%s&sort=created&order=asc&per_page=%d", url.QueryEscape(q), perPage), &result, false); err != nil {
		return nil, err
	}
	var out []foundIssue
	for _, i := range result.Items {
		if i.User.Login == bot && strings.HasPrefix(i.Body, issueMarker) {
			out = append(out, i)
		}
	}
	slices.SortFunc(out, func(x, y foundIssue) int { return x.Number - y.Number })
	return out, nil
}

func keepIssue(ctx context.Context, a *api, repo, bot string, ms []missing, runURL string, log io.Writer) error {
	issues, err := findIssues(ctx, a, repo, bot)
	if err != nil {
		return err
	}
	var one *foundIssue
	if i := slices.IndexFunc(issues, func(i foundIssue) bool { return i.State == "open" }); i >= 0 {
		one = &issues[i]
	} else if len(issues) > 0 {
		one = &issues[0]
	}
	for _, i := range issues {
		if i.State == "open" && i.Number != one.Number {
			if err := closeIssue(ctx, a, repo, i.Number, fmt.Sprintf("A duplicate of #%d.", one.Number)); err != nil {
				return err
			}
			logf(log, "verify: issue #%d closed as a duplicate of #%d", i.Number, one.Number)
		}
	}
	if len(ms) == 0 {
		if one != nil && one.State == "open" {
			if err := closeIssue(ctx, a, repo, one.Number, strings.TrimSpace("Every PR has a verdict again. "+runURL)); err != nil {
				return err
			}
			logf(log, "verify: issue #%d closed", one.Number)
		}
		return nil
	}
	want := issueBody(ms, runURL)
	switch {
	case one == nil:
		var created struct {
			Number int `json:"number"`
		}
		if err := a.send(ctx, http.MethodPost, fmt.Sprintf("repos/%s/issues", repo), map[string]string{"title": issueTitle, "body": want}, &created); err != nil {
			return err
		}
		logf(log, "verify: issue #%d opened", created.Number)
	case one.State != "open":
		if err := a.send(ctx, http.MethodPatch, fmt.Sprintf("repos/%s/issues/%d", repo, one.Number), map[string]string{"state": "open", "body": want}, nil); err != nil {
			return err
		}
		logf(log, "verify: issue #%d reopened", one.Number)
	case !sameList(one.Body, want):
		if err := a.send(ctx, http.MethodPatch, fmt.Sprintf("repos/%s/issues/%d", repo, one.Number), map[string]string{"body": want}, nil); err != nil {
			return err
		}
		logf(log, "verify: issue #%d updated", one.Number)
	default:
		logf(log, "verify: issue #%d already lists these PRs", one.Number)
	}
	return nil
}

func closeIssue(ctx context.Context, a *api, repo string, number int, why string) error {
	if err := a.send(ctx, http.MethodPost, fmt.Sprintf("repos/%s/issues/%d/comments", repo, number), map[string]string{"body": why}, nil); err != nil {
		return err
	}
	return a.send(ctx, http.MethodPatch, fmt.Sprintf("repos/%s/issues/%d", repo, number), map[string]string{"state": "closed"}, nil)
}

func readHandled(dir string) ([]handled, []missing) {
	names, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, []missing{{What: err.Error()}}
	}
	var (
		all      []handled
		problems []missing
	)
	if !slices.Contains(names, filepath.Join(dir, firstReport)) {
		problems = append(problems, missing{What: "the first job's report is missing, so the PRs it judged are not checked"})
	}
	for _, n := range names {
		raw, err := os.ReadFile(n)
		var hs []handled
		if err == nil {
			err = json.Unmarshal(raw, &hs)
		}
		if err != nil {
			problems = append(problems, missing{What: fmt.Sprintf("the report %s could not be read: %v", filepath.Base(n), err)})
			continue
		}
		all = append(all, hs...)
	}
	return all, problems
}

// A newline would end the workflow command, and what follows could be read as one.
func annotation(s string) string {
	return strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A").Replace(s)
}

func runVerify(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	repo := fs.String("repo", os.Getenv("GITHUB_REPOSITORY"), "owner/repo")
	dir := fs.String("handled", "", "the directory with every job's --handled report, the first as "+firstReport)
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
	hs, ms := readHandled(*dir)
	ms = append(ms, verifyHandled(ctx, a, *repo, *bot, hs)...)
	for _, m := range ms {
		logf(stdout, "::error title=pre-flight verify::%s", annotation(strings.ReplaceAll(m.where(), "`", "")+": "+m.What))
	}
	switch err := keepIssue(ctx, a, *repo, *bot, ms, runURL, stdout); {
	case hasStatus(err, http.StatusGone):
		// Issues are off (a fork): GitHub answers 410.
		logf(stdout, "::warning title=pre-flight verify::issues are disabled in %s, so no issue is kept; the lines above say what is missing", *repo)
	case err != nil:
		_, _ = fmt.Fprintln(stderr, "preflight verify:", err)
		return 2
	}
	if len(ms) > 0 {
		logf(stdout, "verify: %d line(s) without a verdict", len(ms))
		return 1
	}
	logf(stdout, "verify: every PR of the run has its verdict (%d report line(s))", len(hs))
	return 0
}
