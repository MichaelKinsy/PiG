package evalsuites

// Ports packages/evals/evals/openai-provider.docs.eval.ts and custom-provider.docs.eval.ts.

import (
	"context"
	"fmt"
	"os"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/evals"
)

const streamAPIKey = "resolved-stream-key"

func probeOutput(ctx context.Context, run evals.AgentRun, scenario evals.ProviderScenario) (any, error) {
	runtime, err := evals.LoadConfiguredModelRuntime(ctx, run.AgentDir)
	if err != nil {
		return nil, err
	}
	defer runtime.Close()
	return evals.InspectProvider(ctx, runtime, scenario), nil
}

func probeContext(prompt string) func() ai.Context {
	return func() ai.Context {
		return ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText(prompt)}}}
	}
}

// OpenAIProvider asks the agent to configure an OpenAI-compatible provider that reaches the Acme fixture.
func OpenAIProvider() evals.Suite {
	server := evals.CreateAcmeServer(evals.AcmeServerModeOpenAI)
	expected := evals.ProviderRuntimeOutput{Result: evals.ProviderRuntimeResult{ProviderProbe: &evals.ProviderProbe{
		ValidRequestReceived: true,
		Model:                evals.ModelFields{ID: evals.OpenAIModelID, Name: "Acme Chat", Provider: evals.OpenAIProviderID, Input: []string{"text"}, ContextWindow: 32768, MaxTokens: 4096},
		Response:             evals.ProviderProbeResponse{Text: evals.OpenAIProbeResponse, StopReason: "stop", InputTokens: 3, OutputTokens: 2},
	}}}
	return evals.Suite{
		Name: "Add OpenAI-compatible provider",
		File: "evals/openai-provider.docs.eval.go",
		Harness: documentationHarness(evals.PiCodingAgentHarnessOptions{Output: func(ctx context.Context, run evals.AgentRun) (any, error) {
			return probeOutput(ctx, run, evals.ProviderScenario{
				ProviderID: evals.OpenAIProviderID, ModelID: evals.OpenAIModelID, CreateContext: probeContext(evals.OpenAIProbePrompt),
				Options:              ai.StreamOptions{Env: ai.ProviderEnv{"ACME_API_KEY": "resolved-acme-key"}, MaxTokens: 32},
				ValidRequestReceived: server.ValidRequestReceived,
			})
		}}),
		Judges:           []evals.Judge{mustJudge(expected)},
		NoJudgeThreshold: true,
		Setup: func(context.Context) (func(context.Context) error, error) {
			return server.Stop, server.Start()
		},
		BeforeEach: server.Reset,
		Cases: []evals.Case{{Name: "configures a provider that works through Pi", Run: func(ctx context.Context, run evals.RunFunc) error {
			_, err := run(ctx, promptThenReload(fmt.Sprintf(`Configure this running PiG installation with Acme as a provider. Do not create project-local configuration. Its provider ID is %s, its API is at %s, and it uses OpenAI Chat Completions. Read its API key from the ACME_API_KEY environment variable.

The provider offers one model, %s, shown as “Acme Chat”. It accepts text, does not support reasoning, has a 32,768-token context window and a 4,096-token maximum output, and has no usage cost.`,
				evals.OpenAIProviderID, server.BaseURL(), evals.OpenAIModelID)))
			return err
		}}},
	}
}

// CustomProvider asks the agent to implement a streaming provider from an API fixture document.
func CustomProvider() evals.Suite {
	const documentationPath = "fixtures/acme-stream-api.json"
	server := evals.CreateAcmeServer(evals.AcmeServerModeStream)
	expected := evals.ProviderRuntimeOutput{Result: evals.ProviderRuntimeResult{ProviderProbe: &evals.ProviderProbe{
		ValidRequestReceived: true,
		Model:                evals.ModelFields{ID: evals.StreamModelID, Name: "Acme Stream Chat", Provider: evals.StreamProviderID, Input: []string{"text"}, ContextWindow: 16384, MaxTokens: 2048},
		Response:             evals.ProviderProbeResponse{Text: evals.StreamProbeResponse, StopReason: "stop", InputTokens: 4, OutputTokens: 3},
	}}}
	return evals.Suite{
		Name: "Add custom streaming provider",
		File: "evals/custom-provider.docs.eval.go",
		Harness: documentationHarness(evals.PiCodingAgentHarnessOptions{
			WorkspaceFiles: map[string]string{documentationPath: evals.StreamAPIDocumentation},
			Output: func(ctx context.Context, run evals.AgentRun) (any, error) {
				return probeOutput(ctx, run, evals.ProviderScenario{
					ProviderID: evals.StreamProviderID, ModelID: evals.StreamModelID, CreateContext: probeContext(evals.StreamProbePrompt),
					Options:              ai.StreamOptions{Env: ai.ProviderEnv{"ACME_STREAM_API_KEY": streamAPIKey}, MaxTokens: 32},
					ValidRequestReceived: server.ValidRequestReceived,
				})
			},
		}),
		Judges:           []evals.Judge{mustJudge(expected)},
		NoJudgeThreshold: true,
		Setup: func(context.Context) (func(context.Context) error, error) {
			if err := os.Setenv("ACME_STREAM_API_KEY", streamAPIKey); err != nil {
				return nil, err
			}
			cleanup := func(ctx context.Context) error {
				_ = os.Unsetenv("ACME_STREAM_API_KEY")
				return server.Stop(ctx)
			}
			return cleanup, server.Start()
		},
		BeforeEach: server.Reset,
		Cases: []evals.Case{{Name: "implements a provider from an API fixture", Run: func(ctx context.Context, run evals.RunFunc) error {
			_, err := run(ctx, promptThenReload(fmt.Sprintf(`Configure this running PiG installation with Acme Stream as a provider. Do not create a project package or modify project source. Its provider ID is %s, its API is at %s, and its API documentation is in ./%s. Read its credential from the ACME_STREAM_API_KEY environment variable.

It offers one model, %s, shown as “Acme Stream Chat”. The model accepts text, does not support reasoning, has a 16,384-token context window and a 2,048-token maximum output, and has no usage cost.`,
				evals.StreamProviderID, server.Origin(), documentationPath, evals.StreamModelID)))
			return err
		}}},
	}
}
