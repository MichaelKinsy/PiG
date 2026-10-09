package ai

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"
)

// referenceEstimateUsage is faux.ts withUsageEstimate written the direct way: the whole prompt as UTF-16 units.
func referenceEstimateUsage(p *FauxProviderHandle, cache map[string]string, request TranscriptContext, options StreamOptions, content []FauxContentBlock) *Usage {
	parts := []string{}
	for _, message := range request.Messages() {
		role := ""
		switch message.(type) {
		case SystemMessage:
			role = "system"
		case UserMessage:
			role = "user"
		case AssistantMessage:
			role = "assistant"
		case ToolResultMessage:
			role = "toolResult"
		}
		parts = append(parts, role+":"+fauxMessageText(message))
	}
	prompt := strings.Join(parts, "\n\n")
	tokens := (utf16Length(prompt) + 3) / 4
	output := (utf16Length(fauxAssistantText(content)) + 3) / 4
	usage := &Usage{Input: tokens, Output: output}
	if options.SessionID != "" && options.CacheRetention != CacheRetentionNone {
		previous := cache[options.SessionID]
		cache[options.SessionID] = prompt
		if previous != "" {
			a, b := utf16.Encode([]rune(previous)), utf16.Encode([]rune(prompt))
			common := 0
			for common < min(len(a), len(b)) && a[common] == b[common] {
				common++
			}
			usage.CacheRead = (common + 3) / 4
			usage.CacheWrite = (len(b) - common + 3) / 4
			usage.Input = max(0, tokens-usage.CacheRead)
		} else {
			usage.CacheWrite = tokens
		}
	}
	usage.TotalTokens = usage.Input + usage.Output + usage.CacheRead + usage.CacheWrite
	return usage
}

func randomText(random *rand.Rand, unicode bool) string {
	alphabet := []string{"a", "b", "c", " ", "0", "\n", "x"}
	if unicode {
		alphabet = append(alphabet, "é", "ключ", "\U0001F600", "\U0001F601", "日本")
	}
	var text strings.Builder
	for range random.Intn(12) {
		text.WriteString(alphabet[random.Intn(len(alphabet))])
	}
	return text.String()
}

func randomMessages(random *rand.Rand, unicode bool) []Message {
	var messages []Message
	for range 1 + random.Intn(6) {
		switch random.Intn(4) {
		case 0:
			messages = append(messages, UserMessage{Content: UserText(randomText(random, unicode))})
		case 1:
			messages = append(messages, UserMessage{Content: UserContentBlocks{TextContent{Text: randomText(random, unicode)}, ImageContent{MimeType: "image/png", Data: randomText(random, unicode)}}})
		case 2:
			messages = append(messages, AssistantMessage{Content: []AssistantContentBlock{
				TextContent{Text: randomText(random, unicode)},
				ThinkingContent{Thinking: randomText(random, unicode)},
				ToolCall{ID: "c", Name: "lookup", Arguments: JsonObject{"n": float64(random.Intn(9)), "s": randomText(random, unicode)}},
			}})
		default:
			messages = append(messages, ToolResultMessage{ToolCallID: "c", ToolName: "lookup", Content: []ToolResultMessageContent{TextContent{Text: randomText(random, unicode)}, ImageContent{MimeType: "image/png", Data: "zz"}}})
		}
	}
	return messages
}

// TestFauxUsageEstimateMatchesTheDirectComputation drives a session's requests through both implementations: ASCII
// prompts take the byte-prefix path, others the UTF-16 path, and a prompt that changes alphabet mid-session takes both.
func TestFauxUsageEstimateMatchesTheDirectComputation(t *testing.T) {
	random := rand.New(rand.NewSource(11))
	for round := range 400 {
		provider := NewFauxProvider(FauxConfig{})
		cache := map[string]string{}
		base := randomMessages(random, round%2 == 0)
		for step := range 5 {
			messages := append([]Message(nil), base...)
			if step > 0 && random.Intn(2) == 0 {
				messages = append(messages, randomMessages(random, random.Intn(3) == 0)...)
			}
			if random.Intn(4) == 0 && len(messages) > 1 {
				messages[random.Intn(len(messages))] = UserMessage{Content: UserText(randomText(random, random.Intn(2) == 0))}
			}
			request := NormalizeContext(Context{Messages: messages})
			options := StreamOptions{SessionID: []string{"", "s"}[random.Intn(2)]}
			if random.Intn(5) == 0 {
				options.CacheRetention = CacheRetentionNone
			}
			content := []FauxContentBlock{FauxText(randomText(random, true))}
			got := provider.estimateUsage(request, options, content)
			want := referenceEstimateUsage(provider, cache, request, options, content)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("round %d step %d: got %+v, want %+v", round, step, got, want)
			}
		}
	}
}

func TestFauxMessageTextWriterMatchesFauxMessageText(t *testing.T) {
	random := rand.New(rand.NewSource(5))
	for range 500 {
		for _, message := range randomMessages(random, true) {
			var builder strings.Builder
			writeFauxMessageText(&builder, message)
			if got, want := builder.String(), fauxMessageText(message); got != want {
				t.Fatalf("%#v: got %q, want %q", message, got, want)
			}
		}
	}
	system := SystemMessage{Content: SystemText("rules"), ToolsAdded: []ToolSchema{{Name: "t", Description: "d"}}, ToolsRemoved: []ToolReference{{Name: "u"}}}
	var builder strings.Builder
	writeFauxMessageText(&builder, system)
	if got, want := builder.String(), fauxMessageText(system); got != want {
		t.Fatalf("system: got %q, want %q", got, want)
	}
	_ = fmt.Sprint
}

// fauxMessageText is faux.ts messageToText built from its parts, the reference writeFauxMessageText is checked against.
func fauxMessageText(message Message) string {
	switch message := message.(type) {
	case SystemMessage:
		parts := []string{}
		if text := GetCurrentSystemPrompt([]Message{message}); text != "" {
			parts = append(parts, text)
		}
		for _, tool := range message.ToolsRemoved {
			parts = append(parts, "tool-:"+SafeJsonStringify(tool))
		}
		for _, tool := range message.ToolsAdded {
			parts = append(parts, "tool+:"+SafeJsonStringify(tool))
		}
		return strings.Join(parts, "\n")
	case UserMessage:
		if text, ok := message.Content.(UserText); ok {
			return string(text)
		}
		blocks, _ := message.Content.(UserContentBlocks)
		parts := []string{}
		for _, block := range blocks {
			switch block := block.(type) {
			case TextContent:
				parts = append(parts, block.Text)
			case ImageContent:
				parts = append(parts, fmt.Sprintf("[image:%s:%d]", block.MimeType, utf16Length(block.Data)))
			}
		}
		return strings.Join(parts, "\n")
	case AssistantMessage:
		parts := []string{}
		for _, block := range message.Content {
			switch block := block.(type) {
			case TextContent:
				parts = append(parts, block.Text)
			case ThinkingContent:
				parts = append(parts, block.Thinking)
			case ToolCall:
				parts = append(parts, block.Name+":"+SafeJsonStringify(block.Arguments))
			}
		}
		return strings.Join(parts, "\n")
	case ToolResultMessage:
		parts := []string{message.ToolName}
		for _, block := range message.Content {
			switch block := block.(type) {
			case TextContent:
				parts = append(parts, block.Text)
			case ImageContent:
				parts = append(parts, fmt.Sprintf("[image:%s:%d]", block.MimeType, utf16Length(block.Data)))
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}
