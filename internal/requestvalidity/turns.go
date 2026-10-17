package requestvalidity

import (
	"fmt"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

// Turn is one entry of a request's message list, reduced to what a provider's tool contract constrains.
type Turn struct {
	// Role is "system", "user", "assistant" or "tool".
	Role string
	// Calls are the tool call ids this turn announces.
	Calls []string
	// Results are the tool call ids this turn answers.
	Results []string
	// Empty is a turn with no content, which some providers reject.
	Empty bool
	// ContentBeforeResult is a turn that carries a tool result after other content.
	ContentBeforeResult bool
}

// Contract is the part of a provider's message contract that PiG checks.
type Contract struct {
	// API is the PiG API name.
	API string
	// Cite names the documentation the rules come from.
	Cite string
	// Roles are the roles the request may contain.
	Roles []string
	// ResultsInUserTurn means the results of one assistant turn are blocks of the single user turn that follows it (Anthropic, Bedrock, Google). Otherwise each result is its own "tool" turn.
	ResultsInUserTurn bool
	// UniqueCallIDs rejects a call id that appears in two assistant turns.
	UniqueCallIDs bool
	// IDPattern is the format a call id must match.
	IDPattern *lazyregexp.Regexp
	// Alternate requires user and assistant turns to alternate.
	Alternate bool
	// FirstUser requires the first non-system turn to be a user turn.
	FirstUser bool
	// LastRoles are the roles the last turn may have. Empty allows any.
	LastRoles []string
	// CallsFollowUser requires an assistant turn with tool calls to follow a user turn.
	CallsFollowUser bool
	// NonEmpty rejects a user or assistant turn with no content.
	NonEmpty bool
}

// Check returns every rule of the contract that turns breaks.
func (c Contract) Check(turns []Turn) []Violation {
	var out []Violation
	add := func(rule, format string, args ...any) {
		out = append(out, Violation{Rule: rule, Detail: fmt.Sprintf(format, args...)})
	}
	var open map[string]bool
	var openOrder []string
	answered := map[string]bool{}
	seenCalls := map[string]int{}
	unanswered := func() []string {
		var ids []string
		for _, id := range openOrder {
			if !answered[id] {
				ids = append(ids, id)
			}
		}
		return ids
	}
	closeOpen := func() { open, openOrder, answered = nil, nil, map[string]bool{} }
	prev := ""
	nonSystem := -1
	for i, turn := range turns {
		if !slices.Contains(c.Roles, turn.Role) {
			add("role-allowed", "message %d has role %q, which %s does not accept", i, turn.Role, c.API)
		}
		if turn.Role != "system" {
			nonSystem++
			if nonSystem == 0 && c.FirstUser && turn.Role != "user" {
				add("first-role", "the first message is %q, not user", turn.Role)
			}
			if c.NonEmpty && turn.Empty && turn.Role != "tool" && len(turn.Calls) == 0 && len(turn.Results) == 0 {
				add("non-empty-content", "message %d (%s) has no content", i, turn.Role)
			}
			if c.Alternate && prev != "" && prev == turn.Role && turn.Role != "tool" {
				add("role-alternation", "message %d repeats role %q", i, turn.Role)
			}
		}
		switch {
		case len(turn.Results) > 0:
			if open == nil {
				add("result-has-call", "message %d carries tool result %s with no tool call in the immediately previous assistant message", i, quoteAll(turn.Results))
			}
			if c.ResultsInUserTurn && turn.Role != "user" {
				add("result-role", "message %d carries tool results in role %q", i, turn.Role)
			}
			if c.ResultsInUserTurn && turn.ContentBeforeResult {
				add("result-first", "message %d puts content before its tool results", i)
			}
			if open != nil {
				for _, id := range turn.Results {
					switch {
					case !open[id]:
						add("result-has-call", "message %d answers %q, which the previous assistant message did not call", i, id)
					case answered[id]:
						add("duplicate-result", "message %d answers %q a second time", i, id)
					default:
						answered[id] = true
					}
				}
			}
			if c.ResultsInUserTurn {
				if open != nil {
					if missing := unanswered(); len(missing) > 0 {
						add("call-has-result", "the message before %d called %s, and message %d does not answer them", i, quoteAll(missing), i)
					}
				}
				closeOpen()
			}
		case len(turn.Calls) > 0:
			if missing := unanswered(); len(missing) > 0 {
				add("call-has-result", "tool calls %s have no result before assistant message %d", quoteAll(missing), i)
			}
			closeOpen()
			if c.CallsFollowUser && prev != "user" {
				add("call-turn-order", "assistant message %d with tool calls follows %q, not a user turn", i, prev)
			}
			open = map[string]bool{}
			for _, id := range turn.Calls {
				if open[id] {
					add("duplicate-call-id", "assistant message %d calls %q twice", i, id)
				} else if c.UniqueCallIDs && seenCalls[id] > 0 {
					add("duplicate-call-id", "assistant message %d reuses tool call id %q from message %d", i, id, seenCalls[id]-1)
				}
				if !open[id] {
					openOrder = append(openOrder, id)
				}
				open[id] = true
				if seenCalls[id] == 0 {
					seenCalls[id] = i + 1
				}
				if c.IDPattern != nil && !c.IDPattern.MatchString(id) {
					add("call-id-format", "tool call id %q does not match %s", id, c.IDPattern)
				}
			}
		default:
			if missing := unanswered(); len(missing) > 0 {
				add("between-call-and-result", "message %d (%s) sits between tool calls %s and their results", i, turn.Role, quoteAll(missing))
			}
			closeOpen()
		}
		if turn.Role != "system" {
			prev = turn.Role
		}
	}
	if missing := unanswered(); len(missing) > 0 {
		add("call-has-result", "tool calls %s have no result at the end of the request", quoteAll(missing))
	}
	if len(c.LastRoles) > 0 && nonSystem >= 0 && !slices.Contains(c.LastRoles, prev) {
		add("last-role", "the last message has role %q; %s requires one of %s", prev, c.API, strings.Join(c.LastRoles, ", "))
	}
	return out
}

func quoteAll(ids []string) string {
	quoted := make([]string, len(ids))
	for i, id := range ids {
		quoted[i] = fmt.Sprintf("%q", id)
	}
	return "[" + strings.Join(quoted, " ") + "]"
}
