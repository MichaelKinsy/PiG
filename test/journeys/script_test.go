//go:build parity

package journeys

import (
	"fmt"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/test/journeys/harness"
)

// rule answers a request or declines it.
type rule func(r *harness.Request) (harness.Reply, bool)

// script returns a harness.Script that uses the first rule that answers, then falls back to echoing the newest turn. Rules must be pure functions of the request so PiG and Pi receive the same answers.
func script(rules ...rule) harness.Script {
	return func(r *harness.Request) harness.Reply {
		for _, rl := range rules {
			if reply, ok := rl(r); ok {
				return withUsage(r, reply)
			}
		}
		return withUsage(r, echo(r))
	}
}

// withUsage reports token usage that depends only on the message count, so footers agree across products whose system prompts differ in length.
func withUsage(r *harness.Request, reply harness.Reply) harness.Reply {
	if reply.Usage == nil {
		reply.Usage = &harness.Usage{Prompt: 100 + 40*len(r.Messages), Completion: 20}
	}
	return reply
}

func echo(r *harness.Request) harness.Reply {
	last := r.Last()
	if last.Role == "tool" {
		return harness.Say(fmt.Sprintf("Tool result received (%d chars).", len(last.Text)))
	}
	return harness.Say("Echo: " + short(last.Text))
}

func short(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 60 {
		s = s[:60] + "…"
	}
	return s
}

// onPrompt answers a request whose newest user turn starts with prefix (and that has no unanswered tool results after it).
func onPrompt(prefix string, reply func(r *harness.Request) harness.Reply) rule {
	return func(r *harness.Request) (harness.Reply, bool) {
		if r.Last().Role == "user" && strings.HasPrefix(strings.TrimSpace(r.Last().Text), prefix) {
			return reply(r), true
		}
		return harness.Reply{}, false
	}
}

// afterTools answers the request that carries tool results for a turn begun by a prompt starting with prefix.
func afterTools(prefix string, reply func(r *harness.Request, results []harness.Message) harness.Reply) rule {
	return func(r *harness.Request) (harness.Reply, bool) {
		if r.Last().Role != "tool" {
			return harness.Reply{}, false
		}
		for _, v := range slices.Backward(r.Messages) {
			if v.Role == "user" {
				if strings.HasPrefix(strings.TrimSpace(v.Text), prefix) {
					return reply(r, r.ToolResults()), true
				}
				break
			}
		}
		return harness.Reply{}, false
	}
}

func fixed(reply harness.Reply) func(*harness.Request) harness.Reply {
	return func(*harness.Request) harness.Reply { return reply }
}

// afterToolsFixed is afterTools with a constant reply.
func afterToolsFixed(prefix string, reply harness.Reply) rule {
	return afterTools(prefix, func(*harness.Request, []harness.Message) harness.Reply { return reply })
}
