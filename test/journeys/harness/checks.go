package harness

import (
	"fmt"
	"regexp"
	"strings"
)

var dataURLPattern = regexp.MustCompile(`^data:image/[a-z+.-]+;base64,[A-Za-z0-9+/=]+$`)

// CheckChatRequest validates one OpenAI chat-completions request against the provider's documented message contract. Pi 1.1.0 transform-messages.ts closes unanswered calls, so a request built from a transformed transcript satisfies every rule here; a violation is a bug in whichever product built the request, whatever the real provider would answer.
//
//   - a tool message answers a tool call of the assistant message before it ("messages with role 'tool' must be a response to a preceding message with 'tool_calls'");
//   - an assistant message with tool_calls is followed by one tool message per call before any other role ("An assistant message with 'tool_calls' must be followed by tool messages responding to each 'tool_call_id'");
//   - tool call ids are unique across the transcript;
//   - the system message is first and only;
//   - an assistant message carries text or tool calls;
//   - the final message is a user or tool message.
func CheckChatRequest(r *Request) []string {
	var v []string
	add := func(format string, args ...any) { v = append(v, fmt.Sprintf(format, args...)) }
	if len(r.Messages) == 0 {
		return []string{"request has no messages"}
	}
	seen := map[string]int{}
	var pending []string // call ids of the last assistant message not yet answered
	var pendingAt int
	for i, m := range r.Messages {
		switch m.Role {
		case "system", "developer":
			if i != 0 {
				add("message %d: %s message after the first message", i, m.Role)
			}
		case "user":
			if len(pending) > 0 {
				add("message %d: user message while tool calls %v of message %d are unanswered", i, pending, pendingAt)
				pending = nil
			}
			if strings.TrimSpace(m.Text) == "" && len(m.ImageURLs) == 0 {
				add("message %d: user message has no text and no image", i)
			}
			for _, u := range m.ImageURLs {
				if !dataURLPattern.MatchString(u) && !strings.HasPrefix(u, "http") {
					add("message %d: malformed image url %.40q", i, u)
				}
			}
		case "assistant":
			if len(pending) > 0 {
				add("message %d: assistant message while tool calls %v of message %d are unanswered", i, pending, pendingAt)
				pending = nil
			}
			if strings.TrimSpace(m.Text) == "" && len(m.ToolCalls) == 0 {
				add("message %d: assistant message has neither text nor tool calls", i)
			}
			for _, c := range m.ToolCalls {
				if c.ID == "" {
					add("message %d: tool call %q has no id", i, c.Name)
				}
				if prev, dup := seen[c.ID]; dup {
					add("message %d: duplicate tool call id %q (first at message %d)", i, c.ID, prev)
				}
				seen[c.ID] = i
				pending = append(pending, c.ID)
			}
			pendingAt = i
		case "tool":
			found := -1
			for k, id := range pending {
				if id == m.ToolCallID {
					found = k
				}
			}
			if found < 0 {
				add("message %d: tool message answers %q, which is not an unanswered call of the previous assistant message", i, m.ToolCallID)
			} else {
				pending = append(pending[:found], pending[found+1:]...)
			}
		default:
			add("message %d: unknown role %q", i, m.Role)
		}
	}
	if len(pending) > 0 {
		add("tool calls %v of message %d never receive a tool message", pending, pendingAt)
	}
	if last := r.Messages[len(r.Messages)-1].Role; last != "user" && last != "tool" {
		add("final message has role %q; a request must end with a user or tool message", last)
	}
	return v
}
