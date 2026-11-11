// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// The state of a PR lives in the pre-flight's own comment on it, after the report, as an HTML comment GitHub does not
// show: no cache is shared between runs or jobs.
const (
	stateOpen  = "\n<!-- pig-preflight-state\n"
	stateClose = "\n-->\n"
)

// defaultBot is the account the workflow's token comments as.
const defaultBot = "github-actions[bot]"

// prComment is the pre-flight's comment on a PR as a run finds it.
type prComment struct {
	ID    int64
	Body  string
	State *state
}

// loadComment finds the pre-flight's comment on a PR: the first comment by bot that starts with the marker. A comment
// by anyone else is not the pre-flight's, however it starts, so no one but the bot can hand a run a state. A state
// that does not parse is no state: every check is decided again.
func loadComment(ctx context.Context, a *api, repo string, pr int, bot string) (prComment, error) {
	var comments []struct {
		ID   int64  `json:"id"`
		Body string `json:"body"`
		User struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	if err := a.get(ctx, fmt.Sprintf("repos/%s/issues/%d/comments?per_page=%d", repo, pr, perPage), &comments, true); err != nil {
		return prComment{}, err
	}
	for _, c := range comments {
		if c.User.Login == bot && strings.HasPrefix(c.Body, commentMarker) {
			return prComment{ID: c.ID, Body: c.Body, State: parseState(c.Body)}, nil
		}
	}
	return prComment{}, nil
}

// parseState reads the state out of a comment body; nil when there is none or it does not parse.
func parseState(body string) *state {
	_, rest, ok := strings.Cut(body, stateOpen)
	if !ok {
		return nil
	}
	raw, _, ok := strings.Cut(rest, stateClose)
	if !ok {
		return nil
	}
	var st state
	if json.Unmarshal([]byte(raw), &st) != nil {
		return nil
	}
	return &st
}

// withState is the comment body: the report, then the state.
func withState(report string, st state) string {
	raw, err := json.Marshal(st)
	if err != nil {
		panic(err) // a state is plain data
	}
	// "--" ends an HTML comment early; JSON escapes it nowhere, so the state carries it as "-\u002d".
	return report + stateOpen + strings.ReplaceAll(string(raw), "--", `-\u002d`) + stateClose
}
