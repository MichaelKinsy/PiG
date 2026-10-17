package pipelinediff

import (
	"fmt"
	"math/rand/v2"
	"strings"
)

// target is one model whose request both implementations build.
type target struct {
	Name     string
	Provider string
	Model    string
	API      string
}

var targets = []target{
	{"anthropic", "anthropic", "claude-sonnet-4-5", "anthropic-messages"},
	{"openai-responses", "openai", "gpt-5", "openai-responses"},
	{"azure-responses", "azure", "gpt-4.1", "azure-openai-responses"},
	{"completions-groq", "groq", "llama-3.1-8b-instant", "openai-completions"},
	{"completions-kimi", "moonshotai", "kimi-k2.6", "openai-completions"},
	{"completions-deepseek", "deepseek", "deepseek-flash", "openai-completions"},
	{"google", "google", "gemini-2.5-flash", "google-generative-ai"},
	{"mistral", "mistral", "devstral-medium-latest", "mistral-conversations"},
}

// message is one Pi AgentMessage as JSON.
type message = map[string]any

// testCase is the input of one differential run.
type testCase struct {
	Mode         Mode
	Seed         uint64
	Target       target
	SystemPrompt string
	Messages     []message
}

// foreignModels are assistant identities other than the target, so replay crosses a provider or model boundary.
var foreignModels = [][3]string{
	{"openai-responses", "openai", "gpt-5"},
	{"google-generative-ai", "google", "gemini-2.5-flash"},
	{"anthropic-messages", "anthropic", "claude-opus-4-5"},
	{"openai-completions", "groq", "llama-3.1-8b-instant"},
	{"mistral-conversations", "mistral", "devstral-medium-latest"},
}

// Mode selects what history shapes the generator may build.
type Mode int

const (
	// Realistic builds what Pi's agent and session can produce: results follow their assistant message in order, a turn that ended in error or abort has no results, and extra messages land only after a run of results.
	Realistic Mode = iota
	// Chaos also builds shapes only a damaged or hand-built session holds: results for dropped turns, out-of-order and interleaved results.
	Chaos
)

type generator struct {
	mode   Mode
	rng    *rand.Rand
	target target
	clock  int64
	nextID int
	msgs   []message
}

func newGenerator(seed uint64, t target, mode Mode) *generator {
	return &generator{mode: mode, rng: rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)), target: t, clock: 1_700_000_000_000}
}

// generate builds one case from a seed. The same seed yields the same case.
func generate(seed uint64, t target, mode Mode) testCase {
	g := newGenerator(seed, t, mode)
	c := testCase{Seed: seed, Target: t, Mode: mode}
	if g.chance(0.6) {
		c.SystemPrompt = "You are a careful assistant."
	}
	episodes := 1 + g.rng.IntN(7)
	// A session begins with the user's first prompt.
	g.add(g.user())
	for range episodes {
		g.episode()
	}
	// A request ends with a user turn or a tool result: the agent never sends one that ends with an assistant message.
	if last := g.msgs; len(last) == 0 || last[len(last)-1]["role"] == "assistant" {
		g.add(g.user())
	}
	c.Messages = g.msgs
	return c
}

func (g *generator) chance(p float64) bool { return g.rng.Float64() < p }
func (g *generator) pick(n int) int        { return g.rng.IntN(n) }

func (g *generator) tick() int64 {
	g.clock += 1000
	return g.clock
}

func (g *generator) text() string {
	words := []string{"alpha", "beta", "gamma", "delta", "read the file", "ok", "résumé", "日本語", "😀", "line\nbreak", "  padded  "}
	n := 1 + g.pick(4)
	parts := make([]string, n)
	for i := range parts {
		parts[i] = words[g.pick(len(words))]
	}
	return strings.Join(parts, " ")
}

// callID returns a tool call id. An assistant message from the target model carries an id that model's API would have issued, as replay of a real session does; any other assistant carries any provider's id, including ones the target must normalize.
func (g *generator) callID(native bool) string {
	g.nextID++
	if native {
		switch g.target.API {
		case "anthropic-messages":
			return fmt.Sprintf("toolu_%024d", g.nextID)
		case "mistral-conversations":
			return fmt.Sprintf("a%08d", g.nextID)
		}
		return fmt.Sprintf("call_%d", g.nextID)
	}
	switch g.pick(12) {
	case 0:
		return fmt.Sprintf("call_%d|fc_%s", g.nextID, strings.Repeat("x", 40+g.pick(500)))
	case 1:
		return fmt.Sprintf("tool use %d!", g.nextID)
	case 2:
		return fmt.Sprintf("toolu_%024d", g.nextID)
	default:
		return fmt.Sprintf("call_%d", g.nextID)
	}
}

func (g *generator) add(m message) {
	if g.mode == Realistic && m["role"] == "assistant" && len(g.msgs) > 0 && g.msgs[len(g.msgs)-1]["role"] == "assistant" {
		g.msgs = append(g.msgs, g.user())
	}
	g.msgs = append(g.msgs, m)
}

func (g *generator) user() message {
	m := message{"role": "user", "timestamp": g.tick()}
	switch g.pick(4) {
	case 0:
		m["content"] = g.text()
	case 1:
		m["content"] = []any{message{"type": "text", "text": g.text()}}
	case 2:
		m["content"] = []any{message{"type": "text", "text": g.text()}, g.image()}
	default:
		m["content"] = []any{g.image(), g.image(), message{"type": "text", "text": g.text()}}
	}
	return m
}

func (g *generator) image() message {
	return message{"type": "image", "data": "iVBORw0KGgo=", "mimeType": "image/png"}
}

func (g *generator) identity() (api, provider, model string) {
	if g.chance(0.6) {
		return g.target.API, g.target.Provider, g.target.Model
	}
	f := foreignModels[g.pick(len(foreignModels))]
	return f[0], f[1], f[2]
}

func (g *generator) assistant(content []any, stop string) message {
	api, provider, model := g.identity()
	return g.assistantAs(content, stop, api, provider, model)
}

func (g *generator) assistantAs(content []any, stop, api, provider, model string) message {
	m := message{
		"role": "assistant", "content": content, "api": api, "provider": provider, "model": model,
		"usage": message{"input": 1, "output": 1, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 2,
			"cost": message{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}},
		"stopReason": stop, "timestamp": g.tick(),
	}
	if stop == "error" || stop == "aborted" {
		m["errorMessage"] = "failed"
	}
	return m
}

func (g *generator) blocks(withCalls []message) []any {
	out := []any{}
	if g.chance(0.4) {
		kind := g.pick(4)
		switch kind {
		case 0:
			out = append(out, message{"type": "thinking", "thinking": g.text(), "thinkingSignature": `{"type":"reasoning","id":"rs_1","summary":[]}`})
		case 1:
			out = append(out, message{"type": "thinking", "thinking": "", "thinkingSignature": `{"type":"reasoning","id":"rs_2","summary":[],"encrypted_content":"x"}`, "redacted": true})
		case 2:
			out = append(out, message{"type": "thinking", "thinking": "   "})
		default:
			out = append(out, message{"type": "thinking", "thinking": g.text()})
		}
	}
	if g.chance(0.6) {
		block := message{"type": "text", "text": g.text()}
		if g.chance(0.2) {
			block["textSignature"] = "tsig"
		}
		out = append(out, block)
	}
	for _, call := range withCalls {
		out = append(out, call)
	}
	// A model may stream text after its calls; Pi's converters keep block order (openai-responses-shared.ts emits a message item after the function_call).
	if len(withCalls) > 0 && g.chance(0.15) {
		out = append(out, message{"type": "text", "text": g.text()})
	}
	return out
}

func (g *generator) call(native bool) message {
	id := g.callID(native)
	call := message{"type": "toolCall", "id": id, "name": []string{"read", "bash", "edit"}[g.pick(3)], "arguments": message{"path": id}}
	if g.chance(0.15) {
		call["thoughtSignature"] = "thought"
	}
	return call
}

func (g *generator) result(callID, name string) message {
	content := []any{message{"type": "text", "text": g.text()}}
	switch g.pick(6) {
	case 0:
		content = []any{g.image()}
	case 1:
		content = []any{message{"type": "text", "text": g.text()}, g.image()}
	case 2:
		content = []any{}
	}
	return message{"role": "toolResult", "toolCallId": callID, "toolName": name, "content": content, "isError": g.chance(0.2), "timestamp": g.tick()}
}

// interrupt is a message that can land between a tool call and its results.
func (g *generator) interrupt() message {
	switch g.pick(6) {
	case 0:
		return g.user()
	case 1:
		return message{"role": "branchSummary", "summary": g.text(), "fromId": "entry-1", "timestamp": g.tick()}
	case 2:
		return message{"role": "compactionSummary", "summary": g.text(), "tokensBefore": 1234, "timestamp": g.tick()}
	case 3:
		return message{"role": "bashExecution", "command": "ls", "output": g.text(), "exitCode": g.pick(3), "cancelled": g.chance(0.2), "truncated": false, "excludeFromContext": g.chance(0.3), "timestamp": g.tick()}
	case 4:
		return message{"role": "custom", "customType": "note", "content": g.text(), "display": true, "timestamp": g.tick()}
	default:
		return g.system()
	}
}

func (g *generator) system() message {
	return message{"role": "system", "content": "System update: " + g.text(), "timestamp": g.tick()}
}

func (g *generator) episode() {
	switch g.pick(10) {
	case 0, 1:
		g.add(g.user())
	case 2:
		g.add(g.assistant(g.blocks(nil), []string{"stop", "length"}[g.pick(2)]))
	case 3, 4, 5:
		g.toolEpisode("toolUse")
	case 6:
		g.toolEpisode([]string{"aborted", "error"}[g.pick(2)])
	case 7:
		g.add(g.interrupt())
	default:
		g.add(g.user())
		g.toolEpisode("toolUse")
	}
}

// toolEpisode appends an assistant turn with parallel calls and the results that survive: a random subset, in a random order, with an interrupting message sometimes inside the run.
func (g *generator) toolEpisode(stop string) {
	count := 1 + g.pick(3)
	api, provider, model := g.identity()
	native := api == g.target.API && provider == g.target.Provider && model == g.target.Model
	calls := make([]message, count)
	for i := range calls {
		calls[i] = g.call(native)
	}
	g.add(g.assistantAs(g.blocks(calls), stop, api, provider, model))
	if g.mode == Realistic && stop != "toolUse" {
		return
	}
	order := g.rng.Perm(count)
	keep := count
	if g.chance(0.35) {
		keep = g.pick(count + 1)
	}
	if g.mode == Realistic {
		order = order[:0]
		for i := range count {
			order = append(order, i)
		}
	}
	for position, index := range order[:keep] {
		if g.mode == Chaos && position > 0 && g.chance(0.2) {
			g.add(g.interrupt())
		}
		call := calls[index]
		g.add(g.result(call["id"].(string), call["name"].(string)))
	}
	if g.mode == Realistic && g.chance(0.5) {
		g.add(g.interrupt())
	} else if keep > 0 && g.chance(0.15) {
		g.add(g.interrupt())
	}
	if g.chance(0.4) {
		g.add(g.assistant(g.blocks(nil), "stop"))
	}
}

// realistic reports whether a history is one Pi's agent and session can produce: it ends with a message that is not an assistant message, every tool result directly follows the assistant message that called it, and no assistant message that ended in error or abort has results. Shrinking keeps a Realistic case realistic.
func realistic(c testCase) bool {
	if !sendable(c) {
		return false
	}
	var calls map[string]bool
	previous := ""
	for _, m := range c.Messages {
		role, _ := m["role"].(string)
		if role == "assistant" && previous == "assistant" {
			return false
		}
		previous = role
		switch m["role"] {
		case "assistant":
			calls = nil
			if m["stopReason"] != "toolUse" {
				continue
			}
			calls = map[string]bool{}
			blocks, _ := m["content"].([]any)
			for _, b := range blocks {
				if block, ok := b.(map[string]any); ok && block["type"] == "toolCall" {
					calls[block["id"].(string)] = true
				}
			}
		case "toolResult":
			id, _ := m["toolCallId"].(string)
			if !calls[id] {
				return false
			}
		default:
			calls = nil
		}
	}
	return true
}

// sendable reports whether an agent would send the history: it ends with a message that is not an assistant message, its first message that is not a system message is not an assistant message or tool result, and no user message is empty. Shrinking keeps every case sendable.
func sendable(c testCase) bool {
	if len(c.Messages) == 0 || c.Messages[len(c.Messages)-1]["role"] == "assistant" {
		return false
	}
	for _, m := range c.Messages {
		if m["role"] == "user" {
			switch content := m["content"].(type) {
			case string:
				if content == "" {
					return false
				}
			case []any:
				if len(content) == 0 {
					return false
				}
			}
		}
	}
	for _, m := range c.Messages {
		if m["role"] != "system" {
			return m["role"] != "assistant" && m["role"] != "toolResult"
		}
	}
	return false
}
