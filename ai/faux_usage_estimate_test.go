package ai

import (
	"bytes"
	"fmt"
	"math/rand"
	"reflect"
	"slices"
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
			var builder bytes.Buffer
			writeFauxMessageText(&builder, message)
			if got, want := builder.String(), fauxMessageText(message); got != want {
				t.Fatalf("%#v: got %q, want %q", message, got, want)
			}
		}
	}
	system := SystemMessage{Content: SystemText("rules"), ToolsAdded: []ToolSchema{{Name: "t", Description: "d"}}, ToolsRemoved: []ToolReference{{Name: "u"}}}
	var builder bytes.Buffer
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

// TestFauxUsageEstimateMatchesTheDirectComputationAcrossEdits drives sessions whose history is edited between requests the ways an
// agent edits it: turns appended, the tail dropped (compaction), a message replaced, a message replaced by an equal copy or by one
// whose text extends the old one across the next separator. Requests borrow the history's messages, so a kept message is the same
// value as before, as the agent's memoized conversion sends it. The texts mix invalid UTF-8, a WTF-8 lone surrogate and astral
// characters that share their high surrogate.
func TestFauxUsageEstimateMatchesTheDirectComputationAcrossEdits(t *testing.T) {
	alphabet := []string{"a", "b", " ", "\n", "\n\n", "é", "€", "\xe2\x82", "\xff", "\xed\xa0\x80", "\U0001F600", "\U0001F601", "日本", "user:"}
	random := rand.New(rand.NewSource(23))
	text := func() string {
		var builder strings.Builder
		for range random.Intn(10) {
			builder.WriteString(alphabet[random.Intn(len(alphabet))])
		}
		return builder.String()
	}
	message := func() Message {
		switch random.Intn(4) {
		case 0:
			return UserMessage{Content: UserText(text())}
		case 1:
			return UserMessage{Content: UserContentBlocks{TextContent{Text: text()}, ImageContent{MimeType: "image/png", Data: text()}}}
		case 2:
			return AssistantMessage{Content: []AssistantContentBlock{TextContent{Text: text()}, ThinkingContent{Thinking: text()}, ToolCall{ID: "c", Name: "lookup", Arguments: JsonObject{"s": text()}}}}
		default:
			return ToolResultMessage{ToolCallID: "c", ToolName: "lookup", Content: []ToolResultMessageContent{TextContent{Text: text()}}}
		}
	}
	for round := range 600 {
		provider := NewFauxProvider(FauxConfig{})
		cache := map[string]string{}
		var history []Message
		for range 1 + random.Intn(4) {
			history = append(history, message())
		}
		for step := range 8 {
			switch random.Intn(6) {
			case 0:
				for range 1 + random.Intn(3) {
					history = append(history, message())
				}
			case 1:
				history = history[:random.Intn(len(history)+1)]
			case 2:
				if len(history) > 0 {
					history[random.Intn(len(history))] = message()
				}
			case 3:
				if len(history) > 0 {
					index := random.Intn(len(history))
					history[index] = history[index].cloneMessage()
				}
			case 4:
				if len(history) > 0 {
					index := random.Intn(len(history))
					if user, ok := history[index].(UserMessage); ok {
						if content, ok := user.Content.(UserText); ok {
							history[index] = UserMessage{Content: UserText(string(content) + "\n\n" + text())}
						}
					}
				}
			}
			history = slices.Clone(history)
			request := TranscriptContext{messages: history, validated: true}
			if random.Intn(4) == 0 {
				request = NormalizeContext(Context{Messages: history})
			}
			options := StreamOptions{SessionID: []string{"", "s", "s", "s"}[random.Intn(4)]}
			if random.Intn(8) == 0 {
				options.CacheRetention = CacheRetentionNone
			}
			content := []FauxContentBlock{FauxText(text())}
			got := provider.estimateUsage(request, options, content)
			want := wholePromptEstimateUsage(cache, request, options, content)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("round %d step %d: got %+v, want %+v", round, step, got, want)
			}
		}
	}
}

// wholePromptEstimateUsage is the estimate as it was computed before a session remembered its messages: the whole prompt written out
// on every call and compared with the previous one. Its counts differ from referenceEstimateUsage only for text that is not UTF-8.
func wholePromptEstimateUsage(cache map[string]string, request TranscriptContext, options StreamOptions, content []FauxContentBlock) *Usage {
	var buffer bytes.Buffer
	if request.err == nil {
		for index, message := range request.messages {
			if index > 0 {
				buffer.WriteString("\n\n")
			}
			writeFauxPromptMessage(&buffer, message)
		}
	}
	prompt := buffer.String()
	promptUnits, promptASCII := utf16Count([]byte(prompt))
	tokens := (promptUnits + 3) / 4
	usage := &Usage{Input: tokens, Output: (utf16Length(fauxAssistantText(content)) + 3) / 4}
	if options.SessionID != "" && options.CacheRetention != CacheRetentionNone {
		previous := cache[options.SessionID]
		cache[options.SessionID] = prompt
		if previous != "" {
			_, previousASCII := utf16Count([]byte(previous))
			common := 0
			if previousASCII || promptASCII {
				common = commonPrefixBytes([]byte(previous), []byte(prompt))
			} else {
				a, b := utf16.Encode([]rune(previous)), utf16.Encode([]rune(prompt))
				for common < min(len(a), len(b)) && a[common] == b[common] {
					common++
				}
			}
			usage.CacheRead = (common + 3) / 4
			usage.CacheWrite = (promptUnits - common + 3) / 4
			usage.Input = max(0, tokens-usage.CacheRead)
		} else {
			usage.CacheWrite = tokens
		}
	}
	usage.TotalTokens = usage.Input + usage.Output + usage.CacheRead + usage.CacheWrite
	return usage
}

// The common prefix counts UTF-16 units as utf16.Encode([]rune(text)) does at every kind of first difference.
func TestCommonUTF16PrefixMatchesTheUnitComparison(t *testing.T) {
	pieces := []string{"a", "é", "€", "\xe2\x82", "\xe2", "\x82", "\xff", "\xed\xa0\x80", "\xef\xbf\xbd", "\U0001F600", "\U0001F601", "\U0001F680", "日"}
	random := rand.New(rand.NewSource(3))
	build := func() string {
		var builder strings.Builder
		for range random.Intn(8) {
			builder.WriteString(pieces[random.Intn(len(pieces))])
		}
		return builder.String()
	}
	for range 20000 {
		shared := build()
		a, b := shared+build(), shared+build()
		x, y := utf16.Encode([]rune(a)), utf16.Encode([]rune(b))
		want := 0
		for want < min(len(x), len(y)) && x[want] == y[want] {
			want++
		}
		if got := commonUTF16Prefix([]byte(a), []byte(b)); got != want {
			t.Fatalf("commonUTF16Prefix(%q, %q) = %d, want %d", a, b, got, want)
		}
	}
}
