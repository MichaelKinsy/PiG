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

// pulls/<n>/commits lists at most 250 commits and pulls/<n>/files at most 3000 files.
const (
	maxCommits = 250
	maxFiles   = 3000
	perPage    = 100
)

const apiTimeout = 30 * time.Second

type api struct {
	base        string
	token       string
	client      *http.Client
	retryDelays []time.Duration
	wait        func(context.Context, time.Duration) error
	log         io.Writer

	mu    sync.Mutex
	calls int
}

func newAPI(base, token string) *api {
	return &api{
		base:        strings.TrimSuffix(base, "/"),
		token:       token,
		client:      &http.Client{Timeout: apiTimeout},
		retryDelays: defaultRetryDelays,
		wait:        sleep,
	}
}

var defaultRetryDelays = []time.Duration{2 * time.Second, 4 * time.Second, 6 * time.Second}

type response struct {
	status int
	header http.Header
	body   []byte
}

func retrying(ctx context.Context, delays []time.Duration, log io.Writer, label string, send func() (response, error)) (response, error) {
	return retryingWhen(ctx, delays, sleep, log, label, true, send)
}

// retryingWhen repeats a request the server did not take (HTTP 429) and, when the request is repeatable, one that
// failed with a network error or a 5xx: after those the server may have done what was asked.
func retryingWhen(ctx context.Context, delays []time.Duration, wait func(context.Context, time.Duration) error, log io.Writer, label string, repeatable bool, send func() (response, error)) (response, error) {
	for attempt := 0; ; attempt++ {
		r, err := send()
		if err == nil && succeeded(r.status) {
			return r, nil
		}
		transient := err == nil && r.status == http.StatusTooManyRequests || repeatable && (err != nil || r.status >= 500)
		if err == nil {
			err = &httpError{status: r.status, body: strings.TrimSpace(string(r.body))}
		}
		if ctx.Err() != nil || attempt >= len(delays) || !transient {
			return r, fmt.Errorf("%s: %w", label, err)
		}
		if log != nil {
			logf(log, "retry %d/%d of %s in %s: %v", attempt+1, len(delays), label, delays[attempt], err)
		}
		if err := wait(ctx, delays[attempt]); err != nil {
			return r, err
		}
	}
}

func succeeded(status int) bool { return status >= 200 && status < 300 }

func exchange(ctx context.Context, client *http.Client, method, target, token string, body []byte, limit int64) (response, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	//nolint:gosec // G704: the host is --api (the GitHub API) or a download URL pinned in config.yaml; the run's data only fills path segments.
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
	resp, err := client.Do(req) //nolint:gosec // G704: as above.
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

// The caller says whether the request is repeatable: sending it again after the server may have applied it does no harm.
func (a *api) call(ctx context.Context, method, target, label string, repeatable bool, body []byte, limit int64) (response, error) {
	return retryingWhen(ctx, a.retryDelays, a.wait, a.log, label, repeatable, func() (response, error) {
		a.mu.Lock()
		a.calls++
		a.mu.Unlock()
		return exchange(ctx, a.client, method, target, a.token, body, limit)
	})
}

func sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

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

func (a *api) get(ctx context.Context, path string, out any, all bool) error {
	next := a.base + "/" + strings.TrimPrefix(path, "/")
	var pages []json.RawMessage
	for next != "" {
		if !strings.HasPrefix(next, a.base+"/") {
			return fmt.Errorf("GET %s: next page %q is not under %s", path, next, a.base)
		}
		r, err := a.call(ctx, http.MethodGet, next, "GET "+path, true, nil, 64<<20)
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

func hasStatus(err error, status int) bool {
	var h *httpError
	return errors.As(err, &h) && h.status == status
}

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

type prFile struct {
	Filename         string `json:"filename"`
	Status           string `json:"status"`
	SHA              string `json:"sha"`
	Patch            string `json:"patch,omitempty"`
	PreviousFilename string `json:"previous_filename,omitempty"`
	Changes          int    `json:"changes"`
}

func (f prFile) whole() bool {
	return f.Patch == "" && f.Status != "removed" && (f.Status != "renamed" || f.Changes > 0)
}

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

type comparison struct {
	Status   string   `json:"status"`
	AheadBy  int      `json:"ahead_by"`
	BehindBy int      `json:"behind_by"`
	Files    []prFile `json:"files"`
}

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

func refPath(ref string) string {
	parts := strings.Split(ref, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}
