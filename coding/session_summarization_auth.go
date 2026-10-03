// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package coding

import (
	"context"
	"errors"
	"maps"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/codingagent/compaction"
)

// Ports packages/coding-agent/src/core/agent-session.ts (_getSummarizationRequestAuth).
type summarizationRequest struct {
	model     *ai.Model
	completer compaction.SimpleCompleter
	streamFn  compaction.StreamFn
	// thinkingLevel is the thinking level of the summary request: the session's, or the router's for a virtual selection.
	thinkingLevel ai.ThinkingLevel
}

// runDefaultCompaction generates Pi's built-in compaction summary for manual and automatic compaction. It resolves the request only when Pi summarizes itself, so a summary supplied by an extension needs no auth and no routing.
//
// upstream: agent-session.ts:2633-2661 (_runDefaultCompaction)
func (s *Session) runDefaultCompaction(ctx context.Context, prep compaction.CompactionPreparation, model *ai.Model, customInstructions, reason string) (compaction.CompactionResult, error) {
	request, err := s.prepareSummarizationRequest(ctx, model)
	if err != nil {
		return compaction.CompactionResult{}, err
	}
	return compaction.Compact(ctx, prep, request.model, request.completer, request.streamFn, customInstructions, request.thinkingLevel, s.summarizationRetryOptions("compaction", reason), "")
}

type authenticatedSummaryCompleter struct {
	delegate compaction.SimpleCompleter
	options  ai.StreamOptions
}

func (c *authenticatedSummaryCompleter) CompleteSimple(ctx context.Context, model *ai.Model, system string, messages []agent.AgentMessage, options ai.StreamOptions) (string, *ai.Usage, error) {
	return c.delegate.CompleteSimple(ctx, model, system, messages, summaryAuthOptions(options, c.options))
}
func summaryAuthOptions(options, auth ai.StreamOptions) ai.StreamOptions {
	options.APIKey = auth.APIKey
	options.Headers = ai.MergeProviderHeaders(auth.Headers, options.Headers)
	env := maps.Clone(auth.Env)
	if env == nil && options.Env != nil {
		env = make(ai.ProviderEnv)
	}
	maps.Copy(env, options.Env)
	options.Env = env
	return options
}

// prepareSummarizationRequest uses the same Services-owned request preparation as ModelRuntime streaming. Explicit custom streams may operate without registry auth; caller-supplied Go providers own their own auth callbacks.
func (s *Session) prepareSummarizationRequest(ctx context.Context, model *ai.Model) (summarizationRequest, error) {
	thinking := s.ThinkingLevel()
	// Route a virtual model first: summaries size their input and output from the model they get. upstream: agent-session.ts:541-548
	if IsVirtualModel(model) {
		route, err := s.modelRuntime.ResolveModel(ctx, model, agent.ConvertToLLM(s.agent.Messages(), model), ResolveModelOptions{Reason: ModelRouteReasonDirect, ThinkingLevel: thinking})
		if err != nil {
			return summarizationRequest{model: model, thinkingLevel: thinking}, err
		}
		model, thinking = route.Model, route.ThinkingLevel
	}
	request := summarizationRequest{model: model, completer: s.resolveCompleter(), streamFn: s.streamFn, thinkingLevel: thinking}
	// Registry-built providers use the stock request path. A directly supplied Go provider is a caller-owned stream, even when it carries provider identity metadata.
	_, registryBuilt := model.Provider.(*providerAttributionProvider)
	_, nativeAuth := model.Provider.(interface{ Auth() ai.ProviderAuth })
	prepared, provider, options := model, model.Provider, ai.StreamOptions{Headers: ai.ProviderHeadersFromStrings(model.ProviderMeta.Headers)}
	var err error
	if registryBuilt || nativeAuth {
		prepared, provider, options, err = s.modelRuntime.prepare(ctx, model, ai.StreamOptions{})
	}
	if err != nil {
		if (s.streamFn != nil || s.agent.StreamFunction() != nil || !registryBuilt) && ctx.Err() == nil {
			return request, nil
		}
		if missing, ok := errors.AsType[*modelRuntimeAuthMissingError](err); ok {
			return request, errors.New(icodingagent.FormatNoAPIKeyFoundMessage(missing.provider))
		}
		return request, err
	}
	clone := *prepared
	clone.Provider = provider
	request.model = &clone
	request.completer = &authenticatedSummaryCompleter{delegate: request.completer, options: options}
	if original := request.streamFn; original != nil {
		request.streamFn = func(ctx context.Context, model *ai.Model, system string, messages []agent.AgentMessage, base ai.StreamOptions) (string, *ai.Usage, error) {
			return original(ctx, model, system, messages, summaryAuthOptions(base, options))
		}
	}
	return request, nil
}
