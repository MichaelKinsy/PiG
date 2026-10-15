package ai_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math/rand"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/internal/requestvalidity"
)

// payloadGen draws random transcripts for the payload oracles: text and image user content, assistant thinking and tool calls of the target model
// and of another one, error and aborted turns, and tool results with images, as the JSON of agent messages.
type payloadGen struct {
	rng                  *rand.Rand
	api, provider, model string
	callCounter          int
	thinkingSignatures   []string
}

func (g *payloadGen) quote(s string) string       { b, _ := json.Marshal(s); return string(b) }
func (g *payloadGen) pick(items ...string) string { return items[g.rng.Intn(len(items))] }
func (g *payloadGen) text() string {
	return g.pick("", "alpha", "beta \u2028 <&>", "line1\nline2", "😀", "x", " padded ")
}

// textLit is a JSON string literal: the texts above, and the ones a Go string cannot hold, such as a lone UTF-16 surrogate that JSON.parse keeps and the
// providers' sanitizeSurrogates drops.
func (g *payloadGen) textLit() string {
	if g.rng.Intn(4) != 0 {
		return g.quote(g.text())
	}
	return g.pick(`"\ud83d"`, `"a\ude00b"`, `"\ud83d\ude00"`, `"x\ud83d"`, `"   "`, `"\u0000"`, `"tab\there"`, `"\r\n"`, `"\u00a0"`, `"\u200b"`, `"é\u0301"`)
}
func (g *payloadGen) boolean() string { return g.pick("true", "false") }

// callID is a tool call id of the shapes providers see: Pi's own, the pipe-joined Responses form, and ones a converter has to rewrite (spaces, dots, non-ASCII, over the 64-character limit).
func (g *payloadGen) callID(n int) string {
	switch g.rng.Intn(8) {
	case 0:
		return fmt.Sprintf("call_%d|%s", n, strings.Repeat("x", 50))
	case 1:
		return fmt.Sprintf("call.%d-a", n)
	case 2:
		return fmt.Sprintf("id with space %d", n)
	case 3:
		return fmt.Sprintf("é_%d", n)
	case 4:
		return fmt.Sprintf("%s%d", strings.Repeat("a", 70), n)
	case 5:
		return fmt.Sprintf("fc_%d|call_%d", n, n)
	default:
		return fmt.Sprintf("call_%d", n)
	}
}

func (g *payloadGen) toolName() string {
	if g.rng.Intn(4) != 0 {
		return g.pick("read", "bash", "write")
	}
	return g.pick("my-tool.v2", "ünï", strings.Repeat("n", 70), "a b", "Read")
}

// arguments is a tool call's arguments object, nested values included.
func (g *payloadGen) arguments() string {
	switch g.rng.Intn(6) {
	case 0:
		return `{}`
	case 1:
		return fmt.Sprintf(`{"path":%s,"nested":{"a":[1,2,{"b":null}],"c":%s},"flag":%s}`, g.textLit(), g.textLit(), g.boolean())
	case 2:
		return `{"list":[],"empty":"","zero":0,"neg":-1.5,"big":1e21,"small":1e-7}`
	case 3:
		return fmt.Sprintf(`{"é":%s,"key with space":1}`, g.textLit())
	default:
		return fmt.Sprintf(`{"path":%s,"n":%d}`, g.textLit(), g.rng.Intn(10))
	}
}

const payloadOraclePNG = "iVBORw0KGgo="

func (g *payloadGen) userContent() string {
	switch g.rng.Intn(5) {
	case 0:
		return g.textLit()
	case 1:
		return fmt.Sprintf(`[{"type":"text","text":%s}]`, g.textLit())
	case 2:
		return `[]`
	case 3:
		return fmt.Sprintf(`[{"type":"image","data":"%s","mimeType":"image/png"}]`, payloadOraclePNG)
	default:
		return fmt.Sprintf(`[{"type":"text","text":%s},{"type":"image","data":"%s","mimeType":"image/png"}]`, g.textLit(), payloadOraclePNG)
	}
}

// message draws one message; pending holds the tool calls still waiting for a result.
func (g *payloadGen) message(pending *[]string) string {
	stamp := g.rng.Intn(5000)
	switch g.rng.Intn(4) {
	case 0:
		return fmt.Sprintf(`{"role":"user","content":%s,"timestamp":%d}`, g.userContent(), stamp)
	case 1, 2:
		var blocks []string
		for range g.rng.Intn(4) {
			switch g.rng.Intn(3) {
			case 0:
				blocks = append(blocks, fmt.Sprintf(`{"type":"text","text":%s}`, g.textLit()))
			case 1:
				signature := ""
				if len(g.thinkingSignatures) > 0 {
					signature = g.pick(g.thinkingSignatures...)
				}
				blocks = append(blocks, fmt.Sprintf(`{"type":"thinking","thinking":%s%s}`, g.textLit(), signature))
			default:
				g.callCounter++
				id := g.callID(g.callCounter)
				name := g.toolName()
				*pending = append(*pending, id+"\x00"+name)
				blocks = append(blocks, fmt.Sprintf(`{"type":"toolCall","id":%s,"name":%s,"arguments":%s}`, g.quote(id), g.quote(name), g.arguments()))
			}
		}
		provider, api, model := g.provider, g.api, g.model
		if g.rng.Intn(3) == 0 {
			provider, api, model = g.pick("other", "anthropic"), g.pick("anthropic-messages", "openai-responses", "openai-completions"), "other-model"
		}
		return fmt.Sprintf(`{"role":"assistant","content":[%s],"api":"%s","provider":"%s","model":"%s","usage":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":3,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"%s","timestamp":%d}`, strings.Join(blocks, ","), api, provider, model, g.pick("stop", "toolUse", "stop", "error", "aborted", "length"), stamp)
	default:
		if len(*pending) == 0 {
			return fmt.Sprintf(`{"role":"user","content":%s,"timestamp":%d}`, g.userContent(), stamp)
		}
		id, name, _ := strings.Cut((*pending)[0], "\x00")
		*pending = (*pending)[1:]
		content := fmt.Sprintf(`[{"type":"text","text":%s}]`, g.textLit())
		switch g.rng.Intn(6) {
		case 0, 1:
			content = fmt.Sprintf(`[{"type":"text","text":%s},{"type":"image","data":"%s","mimeType":"image/png"}]`, g.textLit(), payloadOraclePNG)
		case 2:
			content = fmt.Sprintf(`[{"type":"image","data":"%s","mimeType":"image/png"}]`, payloadOraclePNG)
		case 3:
			content = `[]`
		}
		return fmt.Sprintf(`{"role":"toolResult","toolCallId":%s,"toolName":"%s","content":%s,"isError":%s,"timestamp":%d}`, g.quote(id), name, content, g.boolean(), stamp)
	}
}

func (g *payloadGen) tools() string {
	if g.rng.Intn(2) == 0 {
		return ""
	}
	var items []string
	for i := range 1 + g.rng.Intn(3) {
		items = append(items, fmt.Sprintf(`{"name":"tool%d","description":%s,"parameters":{"type":"object","properties":{"path":{"type":"string"},"n":{"type":"number"}},"required":["path"]}}`, i, g.textLit()))
	}
	return `"tools":[` + strings.Join(items, ",") + `]`
}

// context draws a Context as JSON: an optional system prompt, tools and one to six messages.
func (g *payloadGen) context() json.RawMessage {
	var pending, raws, members []string
	if g.rng.Intn(2) == 0 {
		members = append(members, `"systemPrompt":`+g.textLit())
	}
	for range 1 + g.rng.Intn(6) {
		raws = append(raws, g.message(&pending))
	}
	if t := g.tools(); t != "" {
		members = append(members, t)
	}
	members = append(members, `"messages":[`+strings.Join(raws, ",")+`]`)
	return json.RawMessage("{" + strings.Join(members, ",") + "}")
}

// payloadContext is the context of a payload probe. The probes are random transcripts compared with Pi's payload (a result with no call, a repeated result, ids Pi does not rewrite), not requests a provider has to accept, so the request-validity guard does not apply to them.
func payloadContext(t *testing.T) context.Context {
	t.Helper()
	return requestvalidity.Allow(t.Context())
}

// payloadTranscript decodes the JSON of a drawn Context into what a provider receives, through agent.ConvertToLLM as a session does.
func payloadTranscript(t *testing.T, raw json.RawMessage) ai.TranscriptContext {
	t.Helper()
	var context struct {
		SystemPrompt string            `json:"systemPrompt"`
		Tools        []ai.ToolSchema   `json:"tools"`
		Messages     []json.RawMessage `json:"messages"`
	}
	// The SDK-compatible decode keeps a lone surrogate as the one UTF-16 unit JSON.parse reads, as a loaded session does.
	if err := sdkjson.Unmarshal(raw, &context); err != nil {
		t.Fatal(err)
	}
	var agentMessages []agent.AgentMessage
	for _, item := range context.Messages {
		var message agent.AgentMessage
		if err := sdkjson.Unmarshal(item, &message); err != nil {
			t.Fatalf("%s: %v", item, err)
		}
		agentMessages = append(agentMessages, message)
	}
	return ai.NormalizeContext(ai.Context{SystemPrompt: context.SystemPrompt, Messages: agent.ConvertToLLM(agentMessages), Tools: context.Tools})
}

// piPayloads runs a node script over the probes and returns, per probe, the payload JSON Pi handed to onPayload or "" when it produced none.
func piPayloads(t *testing.T, script string, probes any) []string {
	t.Helper()
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", script, pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(output, &raw); err != nil {
		t.Fatal(err)
	}
	payloads := make([]string, len(raw))
	for i, item := range raw {
		if json.Unmarshal(item, &payloads[i]) != nil {
			t.Logf("probe %d: Pi produced no payload: %s", i, item)
		}
	}
	produced := 0
	for _, payload := range payloads {
		if payload != "" {
			produced++
		}
	}
	// A script that never reaches onPayload would pass every comparison vacuously.
	if produced*10 < len(payloads)*6 {
		t.Fatalf("Pi produced a payload for only %d of %d probes", produced, len(payloads))
	}
	return payloads
}

// payloadDiff lists the top-level members that differ, each with its value on both sides, and the first differing message.
func payloadDiff(got, want string) string {
	var x, y map[string]json.RawMessage
	if json.Unmarshal([]byte(got), &x) != nil || json.Unmarshal([]byte(want), &y) != nil {
		return "\n  not JSON objects"
	}
	norm := func(raw json.RawMessage) string {
		var v any
		if json.Unmarshal(raw, &v) != nil {
			return string(raw)
		}
		b, _ := json.Marshal(v)
		return string(b)
	}
	var out strings.Builder
	keys := map[string]bool{}
	for k := range x {
		keys[k] = true
	}
	for k := range y {
		keys[k] = true
	}
	for _, k := range slices.Sorted(maps.Keys(keys)) {
		a, aok := x[k]
		b, bok := y[k]
		if aok && bok && norm(a) == norm(b) {
			continue
		}
		if (k == "messages" || k == "input" || k == "contents") && aok && bok {
			var am, bm []json.RawMessage
			_ = json.Unmarshal(a, &am)
			_ = json.Unmarshal(b, &bm)
			for i := range max(len(am), len(bm)) {
				var l, r string
				if i < len(am) {
					l = norm(am[i])
				}
				if i < len(bm) {
					r = norm(bm[i])
				}
				if l != r {
					fmt.Fprintf(&out, "\n  %s[%d]:\n   PiG %.500s\n   Pi  %.500s", k, i, l, r)
					break
				}
			}
			continue
		}
		fmt.Fprintf(&out, "\n  %s:\n   PiG %.300s (present=%v)\n   Pi  %.300s (present=%v)", k, norm(a), aok, norm(b), bok)
	}
	return out.String()
}

// comparePayload records one probe's payload difference; failures counts them and classes tallies the differing members.
func comparePayload(t *testing.T, label, got, want string, failures *int, classes map[string]int) {
	t.Helper()
	diff := payloadDiff(got, want)
	if diff == "" {
		return
	}
	for line := range strings.SplitSeq(diff, "\n") {
		if strings.HasPrefix(line, "  ") && strings.HasSuffix(line, ":") {
			classes[strings.TrimSpace(line)]++
		}
	}
	if *failures++; *failures <= 8 {
		t.Errorf("%s:%s", label, diff)
	}
}
