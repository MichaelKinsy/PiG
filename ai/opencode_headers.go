package ai

// Ports packages/ai/src/providers/opencode-headers.ts.

import (
	"context"
	"maps"
	"strings"
)

const openCodeSessionHeader = "x-opencode-session"

func hasProviderHeader(headers ProviderHeaders, name string) bool {
	for key := range headers {
		if strings.EqualFold(key, name) {
			return true
		}
	}
	return false
}

// withOpenCodeSessionHeaderOptions adds the session header from options.SessionID unless a header of that name (any case) is present; it never mutates the caller's header map.
func withOpenCodeSessionHeaderOptions(options StreamOptions) StreamOptions {
	if options.SessionID == "" || hasProviderHeader(options.Headers, openCodeSessionHeader) {
		return options
	}
	headers := make(ProviderHeaders, len(options.Headers)+1)
	maps.Copy(headers, options.Headers)
	headers[openCodeSessionHeader] = new(options.SessionID)
	options.Headers = headers
	return options
}

// WithOpenCodeSessionHeader adds OpenCode's required per-conversation routing header before API dispatch.
func WithOpenCodeSessionHeader(streams *ProviderStreams) *ProviderStreams {
	wrapped := *streams
	if streams.Stream != nil {
		stream := streams.Stream
		wrapped.Stream = func(ctx context.Context, model *Model, transcript TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error) {
			return stream(ctx, model, transcript, withOpenCodeSessionHeaderOptions(options))
		}
	}
	if streams.StreamSimple != nil {
		streamSimple := streams.StreamSimple
		wrapped.StreamSimple = func(ctx context.Context, model *Model, transcript TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error) {
			return streamSimple(ctx, model, transcript, withOpenCodeSessionHeaderOptions(options))
		}
	}
	return &wrapped
}
