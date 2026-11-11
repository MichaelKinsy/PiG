// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// The GitHub REST limits a PR's data is read under: pulls/<n>/commits returns at most 250 commits and pulls/<n>/files at
// most 3000 files. A larger PR fails the pre-flight with that reason instead of being judged on a truncated list.
const (
	maxCommits = 250
	maxFiles   = 3000
	perPage    = 100
)

// apiTimeout bounds one API request.
const apiTimeout = 30 * time.Second

// api reads the GitHub REST API with a token.
type api struct {
	base   string
	token  string
	client *http.Client
	// retryDelays are the waits before each retry of a request that failed transiently.
	retryDelays []time.Duration
	// log receives one line per retry; nil discards them.
	log io.Writer

	mu sync.Mutex
	// calls counts requests, retries included, logged so a run shows what it cost.
	calls int
}

func newAPI(base, token string) *api {
	return &api{
		base:        strings.TrimSuffix(base, "/"),
		token:       token,
		client:      &http.Client{Timeout: apiTimeout},
		retryDelays: defaultRetryDelays,
	}
}

// defaultRetryDelays are the waits before the first, second and third retry of a request.
var defaultRetryDelays = []time.Duration{2 * time.Second, 4 * time.Second, 6 * time.Second}

// response is one HTTP answer.
type response struct {
	status int
	header http.Header
	body   []byte
}

// retrying calls send until it answers 2xx, answers a status that is not transient, or the retries after each of
// delays run out. A transient failure is a network error, a timeout, HTTP 429 or a 5xx; any other status is the answer,
// not a hiccup. label names the request in errors and in the one log line per retry.
func retrying(ctx context.Context, delays []time.Duration, log io.Writer, label string, send func() (response, error)) (response, error) {
	for attempt := 0; ; attempt++ {
		r, err := send()
		if err == nil && succeeded(r.status) {
			return r, nil
		}
		transient := err != nil || r.status == http.StatusTooManyRequests || r.status >= 500
		if err == nil {
			err = &httpError{status: r.status, body: strings.TrimSpace(string(r.body))}
		}
		if ctx.Err() != nil || attempt >= len(delays) || !transient {
			return r, fmt.Errorf("%s: %w", label, err)
		}
		if log != nil {
			logf(log, "retry %d/%d of %s in %s: %v", attempt+1, len(delays), label, delays[attempt], err)
		}
		if err := sleep(ctx, delays[attempt]); err != nil {
			return r, err
		}
	}
}

// succeeded reports a 2xx status.
func succeeded(status int) bool { return status >= 200 && status < 300 }

// exchange sends one request and reads at most limit bytes of its answer. With a token it speaks the GitHub REST API.
func exchange(ctx context.Context, client *http.Client, method, target, token string, body []byte, limit int64) (response, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return response{}, err
	}
	if token != "" {
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return response{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return response{}, err
	}
	return response{status: resp.StatusCode, header: resp.Header, body: raw}, nil
}

// call sends one API request with the retries of the api and counts it.
func (a *api) call(ctx context.Context, method, target, label string, body []byte, limit int64) (response, error) {
	return retrying(ctx, a.retryDelays, a.log, label, func() (response, error) {
		a.mu.Lock()
		a.calls++
		a.mu.Unlock()
		return exchange(ctx, a.client, method, target, a.token, body, limit)
	})
}

// sleep waits d or until ctx ends.
func sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// token is the GitHub token of the environment: GITHUB_TOKEN in a workflow, GH_TOKEN for a local run.
func token() string {
	if t := os.Getenv("GITHUB_TOKEN"); t != "" {
		return t
	}
	return os.Getenv("GH_TOKEN")
}

func (a *api) callCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls
}

var nextLinkRE = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

// get decodes one response into out; with all it follows the Link header and appends every page's array.
func (a *api) get(ctx context.Context, path string, out any, all bool) error {
	next := a.base + "/" + strings.TrimPrefix(path, "/")
	var pages []json.RawMessage
	for next != "" {
		if !strings.HasPrefix(next, a.base+"/") {
			return fmt.Errorf("GET %s: next page %q is not under %s", path, next, a.base)
		}
		r, err := a.call(ctx, http.MethodGet, next, "GET "+path, nil, 64<<20)
		if err != nil {
			return err
		}
		if !all {
			return json.Unmarshal(r.body, out)
		}
		pages = append(pages, r.body)
		next = ""
		if m := nextLinkRE.FindStringSubmatch(r.header.Get("Link")); m != nil {
			next = m[1]
		}
	}
	var merged []json.RawMessage
	for _, page := range pages {
		var items []json.RawMessage
		if err := json.Unmarshal(page, &items); err != nil {
			return fmt.Errorf("GET %s: %w", path, err)
		}
		merged = append(merged, items...)
	}
	joined, err := json.Marshal(merged)
	if err != nil {
		return err
	}
	return json.Unmarshal(joined, out)
}

type httpError struct {
	status int
	body   string
}

func (e *httpError) Error() string {
	return fmt.Sprintf("HTTP %d: %.200s", e.status, e.body)
}

func isNotFound(err error) bool {
	var h *httpError
	return errors.As(err, &h) && (h.status == http.StatusNotFound || h.status == http.StatusUnprocessableEntity)
}

// pull is the part of pulls/<n> the checks read.
type pull struct {
	Number       int    `json:"number"`
	State        string `json:"state"`
	Body         string `json:"body"`
	Merged       bool   `json:"merged"`
	Mergeable    *bool  `json:"mergeable"`
	Commits      int    `json:"commits"`
	ChangedFiles int    `json:"changed_files"`
	User         struct {
		Login string `json:"login"`
	} `json:"user"`
	Head struct {
		SHA string `json:"sha"`
	} `json:"head"`
	Base struct {
		SHA string `json:"sha"`
		Ref string `json:"ref"`
	} `json:"base"`
}

// prFile is one entry of pulls/<n>/files or of a compare's files.
type prFile struct {
	Filename         string `json:"filename"`
	Status           string `json:"status"`
	SHA              string `json:"sha"`
	Patch            string `json:"patch,omitempty"`
	PreviousFilename string `json:"previous_filename,omitempty"`
	Changes          int    `json:"changes"`
}

// whole reports a file whose new content GitHub shows no patch for (a binary file, or a diff too large to show): a
// scan of the added lines reads it whole from the head. A rename without a change adds nothing.
func (f prFile) whole() bool {
	return f.Patch == "" && f.Status != "removed" && (f.Status != "renamed" || f.Changes > 0)
}

// prCommit is one entry of pulls/<n>/commits.
type prCommit struct {
	SHA    string `json:"sha"`
	Commit struct {
		Message      string `json:"message"`
		Verification struct {
			Verified bool   `json:"verified"`
			Reason   string `json:"reason"`
		} `json:"verification"`
	} `json:"commit"`
}

// comparison is the part of compare/<a>...<b> the checks read.
type comparison struct {
	Status   string   `json:"status"`
	AheadBy  int      `json:"ahead_by"`
	BehindBy int      `json:"behind_by"`
	Files    []prFile `json:"files"`
}

// dependencyChange is one entry of dependency-graph/compare.
type dependencyChange struct {
	ChangeType      string `json:"change_type"`
	Manifest        string `json:"manifest"`
	Ecosystem       string `json:"ecosystem"`
	Name            string `json:"name"`
	Version         string `json:"version"`
	License         string `json:"license"`
	Vulnerabilities []struct {
		Severity    string `json:"severity"`
		AdvisoryID  string `json:"advisory_ghsa_id"`
		AdvisoryURL string `json:"advisory_url"`
	} `json:"vulnerabilities"`
}

// refPath escapes a branch name for a URL path segment by segment: the API reads team/x/y with its slashes, not as
// team%2Fx%2Fy.
func refPath(ref string) string {
	parts := strings.Split(ref, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}
