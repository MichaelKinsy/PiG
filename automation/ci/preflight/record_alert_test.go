// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
)

type storedComment struct {
	ID   int64  `json:"id"`
	Body string `json:"body"`
	User struct {
		Login string `json:"login"`
	} `json:"user"`
}

// alertServer keeps a PR's comments like GitHub does and can answer a write with a 502 after having applied it.
type alertServer struct {
	mu         sync.Mutex
	comments   []storedComment
	failPatch  bool
	failAlerts bool
}

func (s *alertServer) add(login, body string) int64 {
	c := storedComment{ID: int64(len(s.comments) + 1), Body: body}
	c.User.Login = login
	s.comments = append(s.comments, c)
	return c.ID
}

func (s *alertServer) alerts() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, c := range s.comments {
		if c.User.Login == defaultBot && !strings.HasPrefix(c.Body, commentMarker) {
			out = append(out, c.Body)
		}
	}
	return out
}

func (s *alertServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var body map[string]string
	_ = json.NewDecoder(r.Body).Decode(&body)
	switch {
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/issues/7/comments"):
		_ = json.NewEncoder(w).Encode(s.comments)
	case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/statuses/"):
		w.WriteHeader(http.StatusCreated)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/issues/7/comments"):
		id := s.add(defaultBot, body["body"])
		if s.failAlerts && !strings.HasPrefix(body["body"], commentMarker) {
			http.Error(w, "bad gateway", http.StatusBadGateway)
			return
		}
		_, _ = fmt.Fprintf(w, `{"id":%d}`, id)
	case r.Method == http.MethodPatch:
		if s.failPatch {
			http.Error(w, "bad gateway", http.StatusBadGateway)
			return
		}
		var id int64
		_, _ = fmt.Sscanf(r.URL.Path, "/repos/o/r/issues/comments/%d", &id)
		s.comments[id-1].Body = body["body"]
		_, _ = io.WriteString(w, `{}`)
	default:
		http.NotFound(w, r)
	}
}

// One run of the recorder as the engine does it: the PR's comments are read first, then the verdict is recorded.
func recordRun(t *testing.T, url string, cfg config, d *data, ev evaluation) error {
	t.Helper()
	comment, err := loadComment(context.Background(), newAPI(url, "t"), "o/r", 7, defaultBot)
	if err != nil {
		t.Fatal(err)
	}
	d.Previous = comment.State
	return record(context.Background(), cfg, d, ev, recordOptions{apiBase: url, token: "t", comment: comment}, io.Discard)
}

func alertFixture(t *testing.T) (*alertServer, string, config, *data, evaluation) {
	t.Helper()
	cfg := config{Checks: []check{{Name: "secrets", Alert: "revoke it"}}}
	d := &data{Repo: "o/r", PR: 7, Mode: modeFull}
	d.Pull.User.Login = "dev"
	ev := evaluation{Head: "h1", Checks: map[string]verdict{"secrets": {Result: resultFail, Reason: "1 finding(s): a:1 key"}}}
	ev.finish()
	g := &alertServer{}
	server := httptest.NewServer(g)
	t.Cleanup(server.Close)
	return g, server.URL, cfg, d, ev
}

// The list of posted alerts lives in the report comment, which is written after the alerts. When that write fails, the
// alert comment itself says what it is for, so the next run does not post it a second time.
func TestAFailedCommentWriteDoesNotRepeatTheAlert(t *testing.T) {
	g, url, cfg, d, ev := alertFixture(t)
	passing := ev
	passing.Checks = map[string]verdict{"secrets": {Result: resultPass}}
	passing.finish()
	if err := recordRun(t, url, cfg, d, passing); err != nil || len(g.comments) != 1 {
		t.Fatalf("the first report: %v, comments %v", err, g.comments)
	}
	g.failPatch = true
	if err := recordRun(t, url, cfg, d, ev); err == nil || len(g.alerts()) != 1 {
		t.Fatalf("the run whose report write fails: err %v, alerts %q", err, g.alerts())
	}
	g.failPatch = false
	if err := recordRun(t, url, cfg, d, ev); err != nil {
		t.Fatal(err)
	}
	if got := g.alerts(); len(got) != 1 {
		t.Fatalf("%d alerts after the next run, want the one of the failed run:\n%s", len(got), strings.Join(got, "\n---\n"))
	}
	if st := parseState(g.comments[0].Body); st == nil || !slices.Equal(st.Alerted, []string{"secrets"}) {
		t.Fatalf("the report does not record the alert: %+v", st)
	}
}

// The alert's own POST can fail after GitHub created the comment. It is neither sent again in that run nor in the next.
func TestAnAlertTheServerCreatedBeforeFailingIsNotPostedAgain(t *testing.T) {
	g, url, cfg, d, ev := alertFixture(t)
	g.failAlerts = true
	if err := recordRun(t, url, cfg, d, ev); err == nil || len(g.alerts()) != 1 {
		t.Fatalf("the run whose alert fails: err %v, alerts %q", err, g.alerts())
	}
	g.failAlerts = false
	if err := recordRun(t, url, cfg, d, ev); err != nil {
		t.Fatal(err)
	}
	if got := g.alerts(); len(got) != 1 {
		t.Fatalf("%d alerts after the next run, want one:\n%s", len(got), strings.Join(got, "\n---\n"))
	}
}

// Only the bot's own comment counts as a posted alert, and only for the same check and head.
func TestAnAlertIsNotSuppressedByAnotherAuthorOrAnotherHead(t *testing.T) {
	g, url, cfg, d, ev := alertFixture(t)
	g.failPatch = true
	older := ev
	older.Head = "h0"
	if err := recordRun(t, url, cfg, d, older); err != nil || len(g.alerts()) != 1 {
		t.Fatalf("the alert on h0: %v, alerts %q", err, g.alerts())
	}
	bots := g.alerts()[0]
	g.mu.Lock()
	g.comments = nil
	g.add("dev", strings.ReplaceAll(bots, "h0", "h1"))
	g.add(defaultBot, bots)
	g.mu.Unlock()
	if err := recordRun(t, url, cfg, d, ev); err != nil {
		t.Fatal(err)
	}
	if got := g.alerts(); len(got) != 2 || !strings.Contains(got[1], "secrets on `h1`") {
		t.Fatalf("alerts %q, want a new one for h1", got)
	}
}
