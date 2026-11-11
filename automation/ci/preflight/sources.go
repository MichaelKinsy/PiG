// SPDX-License-Identifier: MIT

package main

import (
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
		client:      &http.Client{Timeout: 30 * time.Second},
		retryDelays: defaultRetryDelays,
	}
}

// defaultRetryDelays are the waits before the first, second and third retry of a request.
var defaultRetryDelays = []time.Duration{2 * time.Second, 4 * time.Second, 6 * time.Second}

// transient reports whether a failed request may succeed when repeated: a network error, a timeout, HTTP 429, or a
// 5xx. A 4xx other than 429 is the answer, not a hiccup.
func transient(status int, err error) bool {
	if err != nil {
		return true
	}
	return status == http.StatusTooManyRequests || status >= 500
}

// do sends one GET and returns the body, retrying a transient failure after each of retryDelays.
func (a *api) do(ctx context.Context, path, target string) ([]byte, http.Header, error) {
	for attempt := 0; ; attempt++ {
		body, header, status, err := a.once(ctx, target)
		if err == nil && status == http.StatusOK {
			return body, header, nil
		}
		if err == nil {
			err = &httpError{path: path, status: status, body: strings.TrimSpace(string(body))}
		}
		if ctx.Err() != nil || attempt >= len(a.retryDelays) || !transient(status, errOrNil(status, err)) {
			return nil, nil, err
		}
		wait := a.retryDelays[attempt]
		if a.log != nil {
			logf(a.log, "retry %d/%d of GET %s in %s: %v", attempt+1, len(a.retryDelays), path, wait, err)
		}
		if err := sleep(ctx, wait); err != nil {
			return nil, nil, err
		}
	}
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

// errOrNil keeps a transport error and drops the httpError made from a status, so transient judges a status by its code.
func errOrNil(status int, err error) error {
	if status != 0 {
		return nil
	}
	return err
}

func (a *api) once(ctx context.Context, target string) ([]byte, http.Header, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil) //nolint:gosec // G704: target is the operator's --api base plus a fixed path, or a next-page link get checked to be under that base.
	if err != nil {
		return nil, nil, 0, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if a.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.token)
	}
	a.mu.Lock()
	a.calls++
	a.mu.Unlock()
	resp, err := a.client.Do(req) //nolint:gosec // G704: the request built above, whose URL get checked to be under the --api base.
	if err != nil {
		return nil, nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, nil, 0, err
	}
	return body, resp.Header, resp.StatusCode, nil
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
		body, header, err := a.do(ctx, path, next)
		if err != nil {
			return fmt.Errorf("GET %s: %w", path, err)
		}
		if !all {
			return json.Unmarshal(body, out)
		}
		pages = append(pages, body)
		next = ""
		if m := nextLinkRE.FindStringSubmatch(header.Get("Link")); m != nil {
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
	path   string
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
	Head         struct {
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

func pathEscape(s string) string { return url.PathEscape(s) }
