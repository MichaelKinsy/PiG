// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
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
	// Alerts holds the alert marks of the bot's comments: the alerts that are on the PR, whatever State says.
	Alerts []string
}

// alertMark ends an alert comment and says which check and head it is for. The report comment lists the posted alerts
// too, but it is written after them; the mark is what a run finds when that write failed.
func alertMark(check, head string) string {
	return fmt.Sprintf("<!-- pig-preflight-alert %s %s -->", check, head)
}

var alertMarkRE = regexp.MustCompile(`<!-- pig-preflight-alert \S+ \S+ -->`)

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
	var found prComment
	for _, c := range comments {
		switch {
		case c.User.Login != bot:
		case !strings.HasPrefix(c.Body, commentMarker):
			found.Alerts = append(found.Alerts, alertMarkRE.FindAllString(c.Body, -1)...)
		case found.ID == 0:
			found.ID, found.Body, found.State = c.ID, c.Body, parseState(c.Body)
		}
	}
	return found, nil
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
