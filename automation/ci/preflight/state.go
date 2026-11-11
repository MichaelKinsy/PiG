// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	stateOpen  = "\n<!-- pig-preflight-state\n"
	stateClose = "\n-->\n"
)

const defaultBot = "github-actions[bot]"

type prComment struct {
	ID    int64
	Body  string
	State *state
}

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

func withState(report string, st state) string {
	raw, err := json.Marshal(st)
	if err != nil {
		panic(err)
	}
	// "--" would end the HTML comment early.
	return report + stateOpen + strings.ReplaceAll(string(raw), "--", `-\u002d`) + stateClose
}
