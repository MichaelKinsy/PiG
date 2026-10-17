//go:build parity

package journeys

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/MichaelKinsy/PiG/test/journeys/harness"
)

const agentEnd = `"type":"agent_end"`

// rpcCall sends one command and waits for its response.
func rpcCall(c *harness.Ctx, id, body string) {
	c.Send(fmt.Sprintf(`{"id":%q,%s}`, id, body))
	c.WaitFor(fmt.Sprintf(`"id":%q,"type":"response"`, id))
}

// rpcTurn sends a prompt and waits for the run to end and the provider to be idle.
func rpcTurn(c *harness.Ctx, id, text string, requests int) {
	ends := strings.Count(c.Screen(), agentEnd)
	base := len(c.P.Requests())
	rpcCall(c, id, fmt.Sprintf(`"type":"prompt","message":%q`, text))
	c.WaitCount(agentEnd, ends+1)
	c.Idle(base + requests)
	c.Quiet(c.Settle())
}

// rpcCapture returns the first submatch of re in everything the process wrote after the response with id.
func rpcCapture(c *harness.Ctx, id string, re *regexp.Regexp) string {
	s := c.Screen()
	_, after, ok := strings.Cut(s, fmt.Sprintf(`"id":%q,"type":"response"`, id))
	if !ok {
		c.Fail("no response %s on the wire", id)
		return ""
	}
	m := re.FindStringSubmatch(after)
	if m == nil {
		c.Fail("response %s has no match for %v", id, re)
		return ""
	}
	return m[1]
}

var (
	reSessionFile = regexp.MustCompile(`"sessionFile":"([^"]+)"`)
	reEntryID     = regexp.MustCompile(`"entryId":"([^"]+)"`)
)

// Wall-clock and random values on the RPC wire; the structure around them is compared byte for byte.
var rpcVolatile = []harness.Normalizer{
	{Pattern: `"timestamp":"\d{4}-[^"]+"`, With: `"timestamp":"T"`, Reason: "session entry timestamps are ISO wall-clock times"},
	{Pattern: `"baseUrl":"http://127.0.0.1:\d+/v1"`, With: `"baseUrl":"U"`, Reason: "each product talks to its own scripted provider on a random port"},
	{Pattern: `"type":"message_update","usage":\{[^}]*\{[^}]*\}[^}]*\}`, With: `"type":"message_update","usage":U`, Reason: "message_update carries the live partial's usage, which the producer mutates while events wait in the queue (agent/src/utils/event-stream.ts:44-91 queues the same reference), so its value depends on chunk timing in both products"},
	{Pattern: `"(preamble|docs|cwd)":"(?:[^"\\]|\\.)*"`, With: `"$1":"S"`, Reason: "the system message's preamble and docs sections name the product (D2, product name and docs location) and its cwd section is the sandbox path; the provider request comparison masks the system prompt for the same reason"},
	{Pattern: `"contextUsage":\{"tokens":\d+,"contextWindow":(\d+),"percent":[\d.e+-]+\}`, With: `"contextUsage":{"tokens":N,"contextWindow":$1,"percent":N}`, Reason: "the context estimate counts the system prompt's text (compaction.ts estimateContextTokens); the products' prompts differ in length (D2)"},
	{Pattern: `"wall_time_seconds":[\d.e+-]+`, With: `"wall_time_seconds":0`, Reason: "the bash tool details report the command's wall-clock time"},
	{Pattern: `"durationMs":\d+`, With: `"durationMs":0`, Reason: "tool execution durations are wall-clock"},
	{Pattern: `"timestamp":\d+`, With: `"timestamp":0`, Reason: "message timestamps are wall-clock (Date.now() in both products)"},
	{Pattern: `"sessionId":"[0-9a-f-]{36}"`, With: `"sessionId":"S"`, Reason: "session ids are random UUIDs (session-manager.ts createSessionId)"},
	{Pattern: `"(sessionFile|path|outputPath)":"[^"]*"`, With: `"$1":"P"`, Reason: "paths lead into each product's private sandbox and carry the session's creation time and id"},
	{Pattern: `"(id|entryId|parentId|leafId)":"[0-9a-f]{8}"`, With: `"$1":"E"`, Reason: "session entry ids are random 8-hex strings (session-manager.ts generateId)"},
}

func init() {
	register(&harness.Journey{
		Name:    "rpc-session",
		Doc:     "A long `--mode rpc` session over standard input and output: prompt, tool turn, steer, follow-up, abort, bash (kept and excluded), compaction, every query, every setter, new session, fork, clone, switch, export, and malformed or unknown commands. The wire bytes, provider requests and session files are compared with Pi.",
		Touches: []string{"cmd/pig/", "coding/", "agent/", "internal/codingagent/", "internal/sessionentry/"},
		Env: func(harness.Kind) harness.Env {
			return harness.Env{Pipe: true, Args: []string{"--mode", "rpc"}}
		},
		Script: script(
			onSummary(fixed(harness.Say("## Goal\nrpc journey summary"))),
			onPrompt("TOOL", fixed(harness.Use(harness.Call{Name: "bash", Args: map[string]any{"command": "echo rpc-tool-ran"}}))),
			afterToolsFixed("TOOL", harness.Say("Tool finished over RPC.")),
			onPrompt("STEER", fixed(harness.Say("Slow start. ").Then(harness.Hold("steer")).Then(harness.Say("Done slowly.")))),
			onPrompt("FOLLOW", fixed(harness.Say("Other start. ").Then(harness.Hold("follow")).Then(harness.Say("Done again.")))),
			onPrompt("ABORT", fixed(harness.Say("Doomed start. ").Then(harness.Hold("abort")).Then(harness.Say("Never seen.")))),
		),
		Normalizers: rpcVolatile,
		Backbone: []harness.Step{
			{Name: "launch", Run: func(c *harness.Ctx) { c.Launch(); c.Snap("ready") }},
		},
		Pool: []harness.Action{
			{Step: harness.Step{Name: "prompt-turn", Run: func(c *harness.Ctx) {
				rpcTurn(c, "p-hello", "Hello over RPC", 1)
				c.Snap("prompt")
			}}},
			{Step: harness.Step{Name: "tool-turn", Run: func(c *harness.Ctx) {
				rpcTurn(c, "p-tool", "TOOL run it", 2)
				c.Snap("tool")
			}}},
			{Step: harness.Step{Name: "steer", Run: func(c *harness.Ctx) {
				ends := strings.Count(c.Screen(), agentEnd)
				base := len(c.P.Requests())
				rpcCall(c, "p-steer", `"type":"prompt","message":"STEER start"`)
				c.WaitGate("steer")
				c.WaitFor(`"delta":"Slow start. "`)
				rpcCall(c, "steer-1", `"type":"steer","message":"NUDGE toward the left"`)
				c.P.Release("steer")
				c.WaitCount(agentEnd, ends+1)
				c.Idle(base + 2)
				c.Quiet(c.Settle())
				c.Snap("steered")
			}}},
			{Step: harness.Step{Name: "follow-up", Run: func(c *harness.Ctx) {
				ends := strings.Count(c.Screen(), agentEnd)
				base := len(c.P.Requests())
				rpcCall(c, "p-follow", `"type":"prompt","message":"FOLLOW start"`)
				c.WaitGate("follow")
				c.WaitFor(`"delta":"Other start. "`)
				rpcCall(c, "follow-1", `"type":"follow_up","message":"AFTER that, one more thing"`)
				rpcCall(c, "queue-1", `"type":"get_state"`)
				c.P.Release("follow")
				c.WaitCount(agentEnd, ends+1)
				c.Idle(base + 2)
				c.Quiet(c.Settle())
				c.Snap("followed-up")
			}}},
			{Step: harness.Step{Name: "abort", Run: func(c *harness.Ctx) {
				ends := strings.Count(c.Screen(), agentEnd)
				base := len(c.P.Requests())
				rpcCall(c, "p-abort", `"type":"prompt","message":"ABORT me"`)
				c.WaitGate("abort")
				c.WaitFor(`"delta":"Doomed start. "`)
				rpcCall(c, "abort-1", `"type":"abort"`)
				c.Await("client disconnect", func(string) bool { return c.P.Count(harness.EvDisconnect) >= 1 })
				c.WaitCount(agentEnd, ends+1)
				c.Idle(base + 1)
				c.Quiet(c.Settle())
				c.Snap("aborted")
			}}},
			{Step: harness.Step{Name: "bash", Run: func(c *harness.Ctx) {
				rpcCall(c, "bash-1", `"type":"bash","command":"echo rpc-bash; exit 2"`)
				rpcCall(c, "bash-2", `"type":"bash","command":"echo kept-out","excludeFromContext":true`)
				rpcCall(c, "bash-3", `"type":"bash","command":"echo explicit","excludeFromContext":false`)
				rpcCall(c, "bash-msgs", `"type":"get_messages"`)
				c.Quiet(c.Settle())
				c.Snap("bash")
			}}},
			{Step: harness.Step{Name: "queries", Run: func(c *harness.Ctx) {
				for _, q := range []string{"get_state", "get_messages", "get_session_stats", "get_available_models", "get_available_thinking_levels", "get_commands", "get_last_assistant_text", "get_tree", "get_entries", "get_fork_messages"} {
					rpcCall(c, "q-"+q, fmt.Sprintf(`"type":%q`, q))
				}
				c.Quiet(c.Settle())
				c.Snap("queries")
			}}},
			{Step: harness.Step{Name: "setters", Run: func(c *harness.Ctx) {
				rpcCall(c, "s-1", `"type":"set_thinking_level","level":"high"`)
				rpcCall(c, "s-2", `"type":"cycle_thinking_level"`)
				rpcCall(c, "s-3", `"type":"set_steering_mode","mode":"all"`)
				rpcCall(c, "s-4", `"type":"set_follow_up_mode","mode":"all"`)
				rpcCall(c, "s-5", `"type":"set_auto_compaction","enabled":false`)
				rpcCall(c, "s-6", `"type":"set_auto_retry","enabled":false`)
				rpcCall(c, "s-7", `"type":"set_session_name","name":"rpc journey"`)
				rpcCall(c, "s-8", `"type":"cycle_model"`)
				rpcCall(c, "s-9", `"type":"set_steering_mode","mode":"one-at-a-time"`)
				rpcCall(c, "s-10", `"type":"get_state"`)
				c.Quiet(c.Settle())
				c.Snap("setters")
			}}},
			{Needs: []string{"prompt-turn", "tool-turn"}, Step: harness.Step{Name: "compact", Run: func(c *harness.Ctx) {
				// The response follows the summary request, and a session with nothing worth summarizing answers without one.
				rpcCall(c, "compact-1", `"type":"compact","customInstructions":"keep the tool output"`)
				c.Idle(len(c.P.Requests()))
				c.Quiet(c.Settle())
				c.Snap("compacted")
			}}},
			{Needs: []string{"prompt-turn"}, Step: harness.Step{Name: "fork-clone-export", Run: func(c *harness.Ctx) {
				rpcCall(c, "f-msgs", `"type":"get_fork_messages"`)
				entry := rpcCapture(c, "f-msgs", reEntryID)
				rpcCall(c, "f-export", `"type":"export_html","outputPath":"journey-export.html"`)
				rpcCall(c, "f-fork", fmt.Sprintf(`"type":"fork","entryId":%q`, entry))
				rpcCall(c, "f-state", `"type":"get_state"`)
				rpcCall(c, "f-clone", `"type":"clone"`)
				rpcCall(c, "f-state2", `"type":"get_state"`)
				c.Quiet(c.Settle())
				c.Snap("forked")
			}}},
			{Step: harness.Step{Name: "new-and-switch", Run: func(c *harness.Ctx) {
				rpcCall(c, "n-state", `"type":"get_state"`)
				first := rpcCapture(c, "n-state", reSessionFile)
				rpcCall(c, "n-new", `"type":"new_session"`)
				rpcTurn(c, "n-hello", "Hello in the new session", 1)
				rpcCall(c, "n-switch", fmt.Sprintf(`"type":"switch_session","sessionPath":%q`, first))
				rpcCall(c, "n-state2", `"type":"get_state"`)
				c.Quiet(c.Settle())
				c.Snap("switched")
			}}},
			{Step: harness.Step{Name: "bad-input", Run: func(c *harness.Ctx) {
				c.Send(`{not json`)
				c.WaitFor(`"type":"response","command":"parse"`)
				rpcCall(c, "bad-unknown", `"type":"no_such_command"`)
				rpcCall(c, "bad-prompt", `"type":"prompt"`)
				rpcCall(c, "bad-model", `"type":"set_model","provider":"nope","modelId":"missing"`)
				rpcCall(c, "bad-fork", `"type":"fork","entryId":"deadbeef"`)
				rpcCall(c, "bad-level", `"type":"set_thinking_level","level":"ludicrous"`)
				c.Quiet(c.Settle())
				c.Snap("errors")
			}}},
		},
		Tail: []harness.Step{{Name: "quit", Run: func(c *harness.Ctx) { c.Snap("before-quit"); c.Quit() }}},
	})
}
