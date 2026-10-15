package ai

// pi: packages/ai/src/utils/estimate.ts

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type estimateBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
	Name string `json:"name"`
	Args string `json:"args"`
}

type estimateUsage struct {
	Input      int `json:"input"`
	Output     int `json:"output"`
	CacheRead  int `json:"cacheRead"`
	CacheWrite int `json:"cacheWrite"`
	Total      int `json:"total"`
}

type estimateMessage struct {
	Role   string          `json:"role"`
	TS     int64           `json:"ts"`
	Text   string          `json:"text"`
	Blocks []estimateBlock `json:"blocks"`
	Stop   string          `json:"stop"`
	Usage  estimateUsage   `json:"usage"`
}

type estimateAnswer struct {
	Tokens         int   `json:"tokens"`
	UsageTokens    int   `json:"usageTokens"`
	TrailingTokens int   `json:"trailingTokens"`
	LastUsageIndex *int  `json:"lastUsageIndex"`
	PerMessage     []int `json:"perMessage"`
}

// estimateTranscripts mix every message role and block type with text whose UTF-16 length differs from its rune and byte length (emoji, CJK, ZWJ
// sequences, combining marks), tool arguments whose JSON text length depends on escaping (U+2028, HTML characters, controls, numbers across the
// 1e21 threshold, -0, nested containers), and usage placement: aborted and errored responses, zero-usage responses, and timestamps that make a
// response's usage stale.
func estimateTranscripts() [][]estimateMessage {
	random := rand.New(rand.NewSource(20260938))
	words := []string{"a", "hello world", "é", "日本語", "😀", "👨\u200d👩\u200d👧", "e\u0301", "\u2028", "<b>&", "\"q\"", "\\", "\u007f", "\n", "\x00", "𝐀", ""}
	text := func(n int) string {
		var out string
		for range random.Intn(n + 1) {
			out += words[random.Intn(len(words))]
		}
		return out
	}
	argsPool := []string{`{}`, `{"a":1}`, `{"text":"<b>&amp;\u2028"}`, `{"n":1e21,"m":1e-7,"z":-0,"k":0.1}`, `{"list":[1,"é",null,true,{"x":"😀"}]}`, `{"s":"\u007f\u0000\u001f\"\\"}`, `{"nested":{"a":{"b":[[],{}]}}}`, `{"é":"日本語"}`, `{"big":123456789012345680000}`}
	block := func(allowed []string) estimateBlock {
		t := allowed[random.Intn(len(allowed))]
		b := estimateBlock{Type: t, Text: text(6)}
		if t == "toolCall" {
			b.Name = text(3)
			b.Args = argsPool[random.Intn(len(argsPool))]
		}
		return b
	}
	var out [][]estimateMessage
	for range 1200 {
		messages := []estimateMessage{}
		ts := int64(random.Intn(100))
		for range random.Intn(8) {
			if random.Intn(3) == 0 {
				ts += int64(random.Intn(10)) - 3
			} else {
				ts += int64(random.Intn(5))
			}
			m := estimateMessage{Role: []string{"system", "user", "user", "toolResult", "assistant", "assistant", "assistant"}[random.Intn(7)], TS: ts, Text: text(6)}
			switch m.Role {
			case "user":
				if random.Intn(2) == 0 {
					for range random.Intn(4) {
						m.Blocks = append(m.Blocks, block([]string{"text", "image"}))
					}
					if m.Blocks == nil {
						m.Blocks = []estimateBlock{}
					}
				}
			case "toolResult":
				m.Blocks = []estimateBlock{}
				for range random.Intn(4) {
					m.Blocks = append(m.Blocks, block([]string{"text", "image"}))
				}
			case "assistant":
				m.Blocks = []estimateBlock{}
				for range random.Intn(4) {
					m.Blocks = append(m.Blocks, block([]string{"text", "thinking", "toolCall"}))
				}
				m.Stop = []string{"stop", "stop", "toolUse", "length", "aborted", "error"}[random.Intn(6)]
				m.Usage = estimateUsage{Input: random.Intn(3) * 100, Output: random.Intn(3) * 10, CacheRead: random.Intn(3), CacheWrite: random.Intn(2), Total: []int{0, 0, 500, 1234}[random.Intn(4)]}
			}
			messages = append(messages, m)
		}
		out = append(out, messages)
	}
	return out
}

func buildEstimateMessage(t *testing.T, m estimateMessage) Message {
	t.Helper()
	assistantBlock := func(b estimateBlock) AssistantContentBlock {
		switch b.Type {
		case "text":
			return TextContent{Text: b.Text}
		case "thinking":
			return ThinkingContent{Thinking: b.Text}
		}
		var arguments JsonObject
		if err := json.Unmarshal([]byte(b.Args), &arguments); err != nil {
			t.Fatal(err)
		}
		return ToolCall{ID: "c", Name: b.Name, Arguments: arguments}
	}
	switch m.Role {
	case "system":
		return SystemMessage{Content: SystemText(m.Text), Timestamp: m.TS}
	case "user":
		if m.Blocks == nil {
			return UserMessage{Content: UserText(m.Text), Timestamp: m.TS}
		}
		blocks := UserContentBlocks{}
		for _, b := range m.Blocks {
			if b.Type == "image" {
				blocks = append(blocks, ImageContent{Data: "AAAA", MimeType: "image/png"})
			} else {
				blocks = append(blocks, TextContent{Text: b.Text})
			}
		}
		return UserMessage{Content: blocks, Timestamp: m.TS}
	case "toolResult":
		content := []ToolResultMessageContent{}
		for _, b := range m.Blocks {
			if b.Type == "image" {
				content = append(content, ImageContent{Data: "AAAA", MimeType: "image/png"})
			} else {
				content = append(content, TextContent{Text: b.Text})
			}
		}
		return ToolResultMessage{ToolCallID: "c", ToolName: "t", Content: content, Timestamp: m.TS}
	}
	content := []AssistantContentBlock{}
	for _, b := range m.Blocks {
		content = append(content, assistantBlock(b))
	}
	return AssistantMessage{Content: content, API: "x", Provider: "p", Model: "m", StopReason: StopReason(m.Stop), Timestamp: m.TS,
		Usage: Usage{Input: m.Usage.Input, Output: m.Usage.Output, CacheRead: m.Usage.CacheRead, CacheWrite: m.Usage.CacheWrite, TotalTokens: m.Usage.Total}}
}

// estimate.ts runs from the installed pi-ai in Node against the same transcripts: estimateMessageTokens for every message (UTF-16 lengths, the
// 4800-character image, tool arguments measured as JSON.stringify text) and estimateContextTokens (which response's usage still applies, the trailing
// estimate and the last usage index) must match Pi's.
func TestTokenEstimatesMatchPiOnSeededTranscripts(t *testing.T) {
	transcripts := estimateTranscripts()
	payload, err := json.Marshal(transcripts)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/estimate.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []estimateAnswer
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) != len(transcripts) {
		t.Fatalf("Pi answered %d of %d transcripts", len(expected), len(transcripts))
	}
	withUsage, differing := 0, 0
	for i, transcript := range transcripts {
		messages := make([]Message, len(transcript))
		got := estimateAnswer{PerMessage: []int{}}
		for j, m := range transcript {
			messages[j] = buildEstimateMessage(t, m)
			got.PerMessage = append(got.PerMessage, EstimateMessageTokens(messages[j]))
		}
		estimate := EstimateContextTokens(messages)
		got.Tokens, got.UsageTokens, got.TrailingTokens = estimate.Tokens, estimate.UsageTokens, estimate.TrailingTokens
		if estimate.LastUsageIndex >= 0 {
			got.LastUsageIndex = &estimate.LastUsageIndex
			withUsage++
		}
		want := expected[i]
		if want.PerMessage == nil {
			want.PerMessage = []int{}
		}
		if !reflect.DeepEqual(got, want) {
			if differing++; differing <= 5 {
				raw, _ := json.Marshal(transcript)
				t.Errorf("transcript %d %s\n  Pig %+v\n  Pi  %+v", i, strings.TrimSpace(string(raw)), got, want)
			}
		}
	}
	if differing > 5 {
		t.Errorf("%d of %d transcripts differ from Pi", differing, len(transcripts))
	}
	if withUsage < 200 {
		t.Errorf("transcripts rarely carry applicable usage: %d", withUsage)
	}
}
