package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() (resultErr error) {
	ctx := context.Background()
	dir, err := os.MkdirTemp("", "session-model-")
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, os.RemoveAll(dir)) }()
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: dir, AgentDir: dir})
	if err != nil {
		return err
	}
	raw := []*ai.Model{{ID: "faux-1", DisplayName: "One", Input: []string{"text"}, Capabilities: ai.ModelCapabilities{ContextWindow: 128000, MaxThinking: ai.ThinkingLevelHigh}, ProviderMeta: ai.ProviderMetadata{ProviderID: "faux", API: ai.APIOpenAICompletions, BaseURL: "https://faux.invalid", Reasoning: true}}, {ID: "faux-2", DisplayName: "Two", Input: []string{"text"}, Capabilities: ai.ModelCapabilities{ContextWindow: 128000}, ProviderMeta: ai.ProviderMetadata{ProviderID: "faux", API: ai.APIOpenAICompletions, BaseURL: "https://faux.invalid"}}}
	configured := true
	providerUser := ""
	stream := func(_ context.Context, model *ai.Model, request ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		for _, message := range request.Messages() {
			if user, ok := message.(ai.UserMessage); ok {
				switch content := user.Content.(type) {
				case ai.UserText:
					providerUser = string(content)
				case ai.UserContentBlocks:
					for _, block := range content {
						if text, ok := block.(ai.TextContent); ok {
							providerUser = text.Text
						}
					}
				}
			}
		}
		message := &ai.AssistantMessage{API: model.ProviderMeta.API, Provider: "faux", Model: model.ID, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "done"}}, StopReason: ai.StopReasonStop}
		events := ai.NewAssistantMessageEventStream()
		_ = events.Push(ai.StartEvent{Partial: message})
		_ = events.Push(ai.DoneEvent{Reason: ai.StopReasonStop, Message: message})
		return events, nil
	}
	provider := &ai.ModelsProvider{ID: "faux", Name: "Faux", GetModels: func() ([]*ai.Model, error) { return raw, nil }, Stream: stream, StreamSimple: stream, Auth: ai.ProviderAuth{APIKey: &ai.APIKeyAuth{Name: "Faux key", Resolve: func(context.Context, ai.APIKeyAuthInput) (*ai.AuthResult, error) {
		if !configured {
			return nil, nil
		}
		return &ai.AuthResult{Auth: ai.ModelAuth{APIKey: "faux-key"}}, nil
	}}}}
	if err := services.ModelRuntime().RegisterNativeProvider(provider); err != nil {
		return err
	}
	services.ModelRuntime().Refresh(ctx, ai.ModelsRefreshOptions{AllowNetwork: new(false)})
	var options []*extension.BuildSystemPromptOptions
	ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"input": {func(args ...any) (any, error) {
		event := args[0].(extension.InputEvent)
		if event.Text == "ping" {
			return extension.InputEventResultHandled{}, nil
		}
		if event.Text == "literal-command" {
			return extension.InputEventResultTransform{Text: "/inspect-options"}, nil
		}
		return extension.InputEventResultTransform{Text: "transformed:" + event.Text}, nil
	}}}, Commands: map[string]extension.RegisteredCommand{"inspect-options": {Name: "inspect-options", Handler: func(ctx context.Context, _ string) error {
		opts, err := extension.CommandContextFromContext(ctx).GetSystemPromptOptions()
		if err != nil {
			return err
		}
		options = append(options, opts)
		opts.SelectedTools = append(opts.SelectedTools, "mutated_tool")
		return nil
	}}}}
	runner := inproc.NewRunner([]extension.Extension{ext}, dir)
	firstModel := services.ModelRuntime().GetModel("faux", "faux-1")
	secondModel := services.ModelRuntime().GetModel("faux", "faux-2")
	session, err := coding.NewSession(services, coding.SessionOptions{Model: firstModel, NoSession: true, Runner: runner})
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, session.Close()) }()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range session.Events() {
		}
	}()
	defer func() { _ = session.Close(); <-done }()
	session.SetScopedModels([]coding.ScopedModel{{Model: firstModel, ThinkingLevel: ai.ThinkingHigh}, {Model: secondModel}})
	if err := session.SetThinkingLevel(ai.ThinkingHigh); err != nil {
		return err
	}
	first, err := session.CycleModel("forward")
	if err != nil {
		return err
	}
	if session.ThinkingLevel() != ai.ThinkingOff {
		return errors.New("non-reasoning model not clamped")
	}
	second, err := session.CycleModel("forward")
	if err != nil {
		return err
	}
	if session.ThinkingLevel() != ai.ThinkingHigh {
		return errors.New("scoped preference lost")
	}
	firstModel.ThinkingLevelMap = ai.ThinkingLevelMap{ai.ThinkingXHigh: new("xhigh"), ai.ThinkingMax: new("max")}
	var levels []ai.ModelThinkingLevel
	for range 3 {
		level, err := session.CycleThinkingLevel()
		if err != nil {
			return err
		}
		levels = append(levels, level)
	}
	configured = false
	authErr := session.SetModel(secondModel)
	if authErr == nil {
		return errors.New("unauthenticated model accepted")
	}
	configured = true
	if err := session.Prompt(ctx, "hello"); err != nil {
		return err
	}
	if err := session.Prompt(ctx, "ping"); err != nil {
		return err
	}
	for range 2 {
		if err := session.Prompt(ctx, "/inspect-options"); err != nil {
			return err
		}
	}
	users := 0
	for _, message := range session.Messages() {
		if message.User != nil {
			users++
		}
	}
	hasRead, hasMutation := false, false
	for _, tool := range options[1].SelectedTools {
		hasRead = hasRead || tool == "read"
		hasMutation = hasMutation || tool == "mutated_tool"
	}
	priorOptions := slices.Clone(options[0].SelectedTools)
	session.SetActiveToolsByName([]string{"read"})
	if err := session.Prompt(ctx, "/inspect-options"); err != nil {
		return err
	}
	output := map[string]any{"optionsRebuilt": options[2] != options[0], "rebuiltSelection": options[2].SelectedTools, "priorOptionsRetained": slices.Equal(options[0].SelectedTools, priorOptions), "first": first.Model.ID, "second": second.Model.ID, "levels": levels, "rejectedUnauthenticated": true, "input": providerUser, "users": users, "optionsShared": options[0] == options[1], "optionsRead": hasRead, "optionsMutation": hasMutation}
	if err := session.Prompt(ctx, "literal-command"); err != nil {
		return err
	}
	output["literalCommandCalls"] = len(options) - 3
	output["literalProviderText"] = providerUser
	users = 0
	for _, message := range session.Messages() {
		if message.User != nil {
			users++
		}
	}
	output["literalUserCount"] = users
	return json.NewEncoder(os.Stdout).Encode(output)
}
