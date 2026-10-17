package harness

// pi: packages/durable/src/harness/compaction.ts (selectCut, summarizedMessages, estimateContext, serializeConversation, summaryText, summaryFailure) and context.ts (orderToolResults)

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type compactionOp struct {
	Op            string              `json:"op"`
	Contributions [][]json.RawMessage `json:"contributions,omitempty"`
	Head          bool                `json:"head,omitempty"`
	Keep          int                 `json:"keep"`
	Cut           int                 `json:"cut"`
	Extra         []json.RawMessage   `json:"extra"`
	Messages      []json.RawMessage   `json:"messages"`
	Message       json.RawMessage     `json:"message,omitempty"`
}

type compactionGen struct{ random *rand.Rand }

func (g *compactionGen) pick(options ...string) string { return options[g.random.Intn(len(options))] }

var compactionTexts = []string{"", "a", "hello world", "  padded  ", "line one\nline two", "é😀<>&\u2028\u2029", "\u00a0x\u00a0", "日本語のテキスト", "tab\there", "\x7f\x00ctl"}

func (g *compactionGen) text() string {
	if g.random.Intn(14) == 0 {
		// Long enough to hit the 2000-unit tool-result cut, with astral characters on either side of the boundary.
		prefix := strings.Repeat("x", 1995+g.random.Intn(10))
		return prefix + g.pick("😀😀😀", "é😀é", "aaaaa", "日本語😀") + strings.Repeat("y", g.random.Intn(40))
	}
	return g.pick(compactionTexts...)
}

func (g *compactionGen) block() map[string]any {
	if g.random.Intn(8) == 0 {
		return map[string]any{"type": "image", "data": "aGk=", "mimeType": "image/png"}
	}
	return map[string]any{"type": "text", "text": g.text()}
}

// rawArguments is a JSON object spelled with the member order and number spellings the model sent.
func (g *compactionGen) rawArguments() json.RawMessage {
	keys := []string{"b", "a", "10", "2", "01", "z z", "é", "__proto__", "4294967295", "4294967294", "-1"}
	var parts []string
	seen := map[string]bool{}
	for range g.random.Intn(5) {
		key := keys[g.random.Intn(len(keys))]
		if seen[key] {
			continue
		}
		seen[key] = true
		keyJSON, _ := json.Marshal(key)
		value := g.pick(`1.0`, `1e2`, `-0`, `0.1`, `1E21`, `"a<b>&\u2028"`, `[1.50,2]`, `{"z":1,"2":2,"a":{"10":1,"b":2}}`, `null`, `true`, `1e-7`, `123456789012345678901234567890`, `"\u00e9\ud83d\ude00"`, `"\/"`)
		parts = append(parts, string(keyJSON)+":"+value)
	}
	return json.RawMessage("{" + strings.Join(parts, ",") + "}")
}

func (g *compactionGen) usage(measured bool) map[string]any {
	total := 0
	if measured {
		total = 1 + g.random.Intn(5000)
	}
	return map[string]any{"input": total / 2, "output": total / 4, "cacheRead": total / 8, "cacheWrite": total / 8, "totalTokens": total, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}}
}

var compactionCallIDs = []string{"c1", "c2", "c3", "c4"}

func (g *compactionGen) assistant() map[string]any {
	var content []any
	for range g.random.Intn(4) {
		switch g.random.Intn(5) {
		case 0:
			content = append(content, map[string]any{"type": "thinking", "thinking": g.text()})
		case 1, 2:
			content = append(content, map[string]any{"type": "text", "text": g.text()})
		default:
			arguments := any(g.rawArguments())
			content = append(content, map[string]any{"type": "toolCall", "id": g.pick(compactionCallIDs...), "name": g.pick("read", "bash", "edit tool", "é"), "arguments": arguments})
		}
	}
	if content == nil {
		content = []any{}
	}
	message := map[string]any{"role": "assistant", "content": content, "api": "openai-responses", "provider": "p", "model": "m", "usage": g.usage(g.random.Intn(3) == 0), "stopReason": g.pick("stop", "stop", "toolUse", "length", "error", "aborted"), "timestamp": g.random.Intn(1000)}
	if g.random.Intn(3) == 0 {
		// Not generated: errorMessage "" (an Error with no message). ai.AssistantMessage.ErrorMessage is a string where Pi keeps the empty string, so Pig reads it as absent.
		message["errorMessage"] = g.pick("boom", "é😀")
	}
	return message
}

func (g *compactionGen) message() map[string]any {
	switch g.random.Intn(6) {
	case 0, 1:
		return g.assistant()
	case 2, 3:
		var content any = g.text()
		if g.random.Intn(3) == 0 {
			blocks := []any{}
			for range g.random.Intn(3) {
				blocks = append(blocks, g.block())
			}
			content = blocks
		}
		return map[string]any{"role": "user", "content": content, "timestamp": g.random.Intn(1000)}
	}
	blocks := []any{}
	for range g.random.Intn(3) {
		blocks = append(blocks, g.block())
	}
	return map[string]any{"role": "toolResult", "toolCallId": g.pick(compactionCallIDs...), "toolName": g.pick("read", "bash"), "content": blocks, "isError": g.random.Intn(4) == 0, "timestamp": g.random.Intn(1000)}
}

func (g *compactionGen) contributions() [][]json.RawMessage {
	out := make([][]json.RawMessage, 2+g.random.Intn(12))
	for i := range out {
		out[i] = []json.RawMessage{}
		for range g.random.Intn(4) % 3 {
			encoded, _ := json.Marshal(g.message())
			out[i] = append(out[i], encoded)
		}
	}
	return out
}

func compactionOps() []compactionOp {
	g := &compactionGen{random: rand.New(rand.NewSource(20261022))}
	raw := func(messages ...map[string]any) []json.RawMessage {
		out := []json.RawMessage{}
		for _, m := range messages {
			encoded, _ := json.Marshal(m)
			out = append(out, encoded)
		}
		return out
	}
	var ops []compactionOp
	for range 2500 {
		contributions := g.contributions()
		head := g.random.Intn(3) == 0
		keep := g.pick("0", "1", "10", "40", "100", "200", "1000")
		var keepTokens int
		_, _ = fmt.Sscan(keep, &keepTokens)
		ops = append(ops,
			compactionOp{Op: "cut", Contributions: contributions, Head: head, Keep: keepTokens},
			compactionOp{Op: "estimate", Contributions: contributions, Head: head, Extra: raw(g.message(), g.message())[:g.random.Intn(3)]})
		messages := []json.RawMessage{}
		for _, contribution := range contributions {
			messages = append(messages, contribution...)
		}
		ops = append(ops,
			compactionOp{Op: "summarized", Contributions: contributions, Head: head, Cut: g.random.Intn(len(contributions) + 1)},
			compactionOp{Op: "order", Messages: messages},
			compactionOp{Op: "serialize", Messages: messages})
		message, _ := json.Marshal(g.assistant())
		ops = append(ops, compactionOp{Op: "summary", Message: message})
	}
	return ops
}

type compactionResult struct {
	Value json.RawMessage `json:"value"`
	Err   *string         `json:"err"`
}

func TestCompactionHelpersMatchPi(t *testing.T) {
	ops := compactionOps()
	payload, err := json.Marshal(ops)
	if err != nil {
		t.Fatal(err)
	}
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/compaction_oracle.mjs", filepath.Join(root, ".upstream", "current", "packages"))
	cmd.Env = append(cmd.Environ(), "PI_INTEROP_DIR="+filepath.Join(root, "durable", "interop")+"/")
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []compactionResult
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	decode := func(raws []json.RawMessage) []ai.Message {
		out := make([]ai.Message, len(raws))
		for i, raw := range raws {
			message, err := durable.DecodeMessage(raw)
			if err != nil {
				t.Fatalf("%s: %v", raw, err)
			}
			out[i] = message
		}
		return out
	}
	canonical := func(value any) any {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var out any
		if err := json.Unmarshal(encoded, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	differing := map[string]int{}
	total := map[string]int{}
	nonTrivial := map[string]int{}
	for i, op := range ops {
		var contributions [][]ai.Message
		for _, contribution := range op.Contributions {
			contributions = append(contributions, decode(contribution))
		}
		view := durable.ContextView{Contributions: contributions}
		var flat []ai.Message
		for index, contribution := range contributions {
			view.Entries = append(view.Entries, durable.EntryRecord{Id: durable.EntryId(index + 1)})
			flat = append(flat, contribution...)
		}
		view.Messages = flat
		if op.Head {
			view.Head = &view.Entries[0]
		}
		var got any
		switch op.Op {
		case "cut":
			if cut, ok := SelectCut(view, op.Keep); ok {
				got = cut
			}
		case "estimate":
			got = EstimateContext(view, decode(op.Extra))
		case "summarized":
			got = canonical(SummarizedMessages(view, op.Cut))
		case "order":
			got = canonical(OrderToolResults(decode(op.Messages)))
		case "serialize":
			// A cut inside a surrogate pair leaves a lone surrogate, which Pi's JSON output spells \ud83d; spell Pig's the same way.
			serialized, err := jsstring.MarshalJSON(SerializeConversation(decode(op.Messages)))
			if err != nil {
				t.Fatal(err)
			}
			var text string
			if err := json.Unmarshal(serialized, &text); err != nil {
				t.Fatal(err)
			}
			got = text
		case "summary":
			message := decode([]json.RawMessage{op.Message})[0].(ai.AssistantMessage)
			text, ok := summaryText(message)
			var textValue any
			if ok {
				textValue = text
			}
			got = map[string]any{"text": textValue, "failure": summaryFailure(message)}
		}
		want := expected[i]
		var wantValue any
		if want.Err != nil {
			wantValue = "ERR " + *want.Err
		} else if err := json.Unmarshal(want.Value, &wantValue); err != nil {
			t.Fatal(err)
		}
		total[op.Op]++
		if got != nil && got != "" {
			nonTrivial[op.Op]++
		}
		if !reflect.DeepEqual(canonical(got), canonical(wantValue)) {
			differing[op.Op]++
			if differing[op.Op] <= 2 {
				t.Errorf("%s op %d\n  Pi  %s\n  Pig %s", op.Op, i, tail(wantValue), tail(got))
			}
		}
	}
	t.Logf("ops %v; non-trivial %v; differing %v", total, nonTrivial, differing)
	for kind, count := range differing {
		t.Errorf("%s: %d of %d differ from Pi", kind, count, total[kind])
	}
	for kind, count := range total {
		if nonTrivial[kind] < count/3 {
			t.Errorf("%s probes are mostly trivial: %d of %d", kind, nonTrivial[kind], count)
		}
	}
}

func tail(value any) string {
	text := fmt.Sprint(value)
	if len(text) > 300 {
		return "..." + text[len(text)-300:]
	}
	return text
}
