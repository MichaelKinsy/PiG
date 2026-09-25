package ai

// Mirrors upstream .upstream/current/packages/ai/src/providers/faux.ts.
// Provides deterministic responses and tool call simulation without
// hitting a real LLM API.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"
)

const (
	fauxDefaultMinTokenSize = 3
	fauxDefaultMaxTokenSize = 5
)

// FauxContentBlock is a union of content blocks for faux responses.
type FauxContentBlock struct {
	Type      string         `json:"type"`                // "text" | "thinking" | "toolCall"
	Text      string         `json:"text,omitempty"`      // for type "text"
	Thinking  string         `json:"thinking,omitempty"`  // for type "thinking"
	ID        string         `json:"id,omitempty"`        // for type "toolCall"
	Name      string         `json:"name,omitempty"`      // for type "toolCall"
	Arguments map[string]any `json:"arguments,omitempty"` // for type "toolCall"
}

// FauxText creates a text content block.
func FauxText(text string) FauxContentBlock {
	return FauxContentBlock{Type: "text", Text: text}
}

// FauxThinking creates a thinking content block.
func FauxThinking(thinking string) FauxContentBlock {
	return FauxContentBlock{Type: "thinking", Thinking: thinking}
}

// FauxToolCall creates a tool call content block.
func FauxToolCall(name string, args map[string]any, id string) FauxContentBlock {
	if id == "" {
		id = fmt.Sprintf("tool:%d:%d", time.Now().UnixMilli(), rand.IntN(1000000))
	}
	return FauxContentBlock{Type: "toolCall", ID: id, Name: name, Arguments: args}
}

// FauxResponse defines a canned response for the faux provider.
type FauxResponse struct {
	Content      []FauxContentBlock
	StopReason   string // "stop", "error", "length", "toolUse"
	ErrorMessage string
}

// FauxResponseFactory creates a response dynamically based on context.
type FauxResponseFactory func(context TranscriptContext, opts StreamOptions, callCount int) FauxResponse

// FauxResponseStep is either a static FauxResponse or a FauxResponseFactory.
type FauxResponseStep struct {
	Static  *FauxResponse
	Factory FauxResponseFactory
}

// FauxStaticStep creates a step from a static response.
func FauxStaticStep(resp FauxResponse) FauxResponseStep {
	return FauxResponseStep{Static: &resp}
}

// FauxFactoryStep creates a step from a factory function.
func FauxFactoryStep(fn FauxResponseFactory) FauxResponseStep {
	return FauxResponseStep{Factory: fn}
}

// FauxConfig configures the faux provider.
type FauxConfig struct {
	ProviderID      string
	Model           string
	TokensPerSecond int // 0 = instant
	MinTokenSize    int
	MaxTokenSize    int
}

type fauxProvider struct {
	cfg       FauxConfig
	mu        sync.Mutex
	responses []FauxResponseStep
	callCount int
}

// NewFauxProvider creates a mock Provider for testing.
func NewFauxProvider(cfg FauxConfig) *fauxProvider {
	if cfg.ProviderID == "" {
		cfg.ProviderID = "faux"
	}
	if cfg.Model == "" {
		cfg.Model = "faux-1"
	}
	if cfg.MinTokenSize <= 0 {
		cfg.MinTokenSize = fauxDefaultMinTokenSize
	}
	if cfg.MaxTokenSize <= 0 {
		cfg.MaxTokenSize = fauxDefaultMaxTokenSize
	}
	if cfg.MaxTokenSize < cfg.MinTokenSize {
		cfg.MaxTokenSize = cfg.MinTokenSize
	}
	return &fauxProvider{cfg: cfg}
}

func (p *fauxProvider) ID() string   { return p.cfg.ProviderID }
func (p *fauxProvider) Close() error { return nil }

// SetResponses replaces the pending response queue.
func (p *fauxProvider) SetResponses(responses []FauxResponseStep) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.responses = append([]FauxResponseStep{}, responses...)
}

// AppendResponses adds responses to the queue.
func (p *fauxProvider) AppendResponses(responses []FauxResponseStep) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.responses = append(p.responses, responses...)
}

// PendingResponseCount returns how many responses remain queued.
func (p *fauxProvider) PendingResponseCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.responses)
}

// CallCount returns the total number of Stream() calls made.
func (p *fauxProvider) CallCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.callCount
}

func (p *fauxProvider) Stream(ctx context.Context, transcript TranscriptContext, opts StreamOptions) (*AssistantMessageEventStream, error) {
	if err := validateProviderRequest(ctx, transcript); err != nil {
		return nil, fmt.Errorf("faux: invalid transcript: %w", err)
	}
	p.mu.Lock()
	p.callCount++
	callCount := p.callCount
	var step *FauxResponseStep
	if len(p.responses) > 0 {
		s := p.responses[0]
		step = &s
		p.responses = p.responses[1:]
	}
	p.mu.Unlock()

	builder := newAssistantStreamBuilder(ctx, "faux", p.cfg.ProviderID, p.cfg.Model)
	go func() {
		if step == nil {
			builder.fail(StopReasonError, errors.New("no more faux responses queued"))
			return
		}

		var response FauxResponse
		switch {
		case step.Static != nil:
			response = *step.Static
		case step.Factory != nil:
			response = step.Factory(transcript, opts, callCount)
		default:
			builder.fail(StopReasonError, errors.New("faux response step has neither static nor factory"))
			return
		}

		for blockIndex, block := range response.Content {
			if err := ctx.Err(); err != nil {
				builder.fail(StopReasonAborted, err)
				return
			}
			switch block.Type {
			case "text":
				for _, chunk := range splitByTokenSize(block.Text, p.cfg.MinTokenSize, p.cfg.MaxTokenSize) {
					if err := ctx.Err(); err != nil {
						builder.fail(StopReasonAborted, err)
						return
					}
					builder.textDelta(chunk)
					p.delay(chunk)
				}
			case "thinking":
				for _, chunk := range splitByTokenSize(block.Thinking, p.cfg.MinTokenSize, p.cfg.MaxTokenSize) {
					if err := ctx.Err(); err != nil {
						builder.fail(StopReasonAborted, err)
						return
					}
					builder.thinkingDelta(chunk, false)
					p.delay(chunk)
				}
			case "toolCall":
				arguments, _ := json.Marshal(block.Arguments)
				builder.toolCallDelta(streamToolCallDelta{
					index: blockIndex, id: block.ID, name: block.Name, argumentsDelta: string(arguments),
				})
			}
		}

		reason := StopReason(response.StopReason)
		// upstream: ai/src/providers/faux.ts:stopReason
		if reason == "" {
			reason = StopReasonStop
		}
		if reason == StopReasonError {
			builder.fail(StopReasonError, errors.New(response.ErrorMessage))
			return
		}
		builder.done(reason, nil, response.ErrorMessage)
	}()
	return builder.stream, nil
}

func (p *fauxProvider) delay(chunk string) {
	if p.cfg.TokensPerSecond <= 0 {
		return
	}
	tokens := max(1, len(chunk)/4)
	delayMs := float64(tokens) / float64(p.cfg.TokensPerSecond) * 1000
	time.Sleep(time.Duration(delayMs) * time.Millisecond)
}

func splitByTokenSize(text string, minSize, maxSize int) []string {
	if text == "" {
		return []string{""}
	}
	var chunks []string
	idx := 0
	for idx < len(text) {
		tokenSize := minSize + rand.IntN(maxSize-minSize+1)
		charSize := max(1, tokenSize*4)
		end := min(idx+charSize, len(text))
		chunks = append(chunks, text[idx:end])
		idx = end
	}
	return chunks
}
