package main

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// A real provider may use the id "unknown"; only the Agent's absent-selection sentinel lacks a provider. Pi does not reserve that provider id (agent.ts:57-97).
func TestPrintConcreteUnknownProviderIsNotTheDefaultSentinel(t *testing.T) {
	provider := ai.NewFauxProvider(ai.FauxConfig{})
	provider.SetResponses(fauxSteps(fauxTextResponse("provider answer")))
	host := printModeTestHost(t, provider)
	host.Session.Model.ProviderMeta.ProviderID = "unknown"
	result := runPrintModeForTest(t, host, printModeOptions{Mode: "text", InitialMessage: "hello"})
	if result.err != nil || result.stderr != "" || result.stdout != "provider answer\n" {
		t.Fatalf("err=%v stdout=%q stderr=%q", result.err, result.stdout, result.stderr)
	}
}

// AgentSession.prompt (agent-session.ts:1601-1690) dispatches commands and input before auth preflight. The unknown DEFAULT_MODEL does not prevent either extension path from handling a prompt.
func TestPrintUnknownModelPreflightFollowsExtensionInput(t *testing.T) {
	for _, mode := range []string{"text", "json"} {
		for _, prompt := range []string{"/probe", "handled", "unhandled"} {
			t.Run(mode+"/"+prompt, func(t *testing.T) {
				host := printModeTestHost(t, ai.NewFauxProvider(ai.FauxConfig{}))
				host.Session.Model = nil
				var calls []string
				host.Extensions = []extension.Extension{{
					Path: "no-model",
					Commands: map[string]extension.RegisteredCommand{
						"probe": {Name: "probe", Handler: func(context.Context, string) error { calls = append(calls, "command"); return nil }},
					},
					Handlers: map[string][]extension.HandlerFn{
						"input": {func(args ...any) (any, error) {
							text := args[0].(extension.InputEvent).Text
							calls = append(calls, text)
							if text == "handled" {
								return extension.InputEventResultHandled{}, nil
							}
							return nil, nil
						}},
					},
				}}
				result := runPrintModeForTest(t, host, printModeOptions{Mode: mode, InitialMessage: prompt})
				wantCall := prompt
				if prompt == "/probe" {
					wantCall = "command"
				}
				if !slices.Equal(calls, []string{wantCall}) {
					t.Fatalf("dispatch = %q", calls)
				}
				if prompt == "unhandled" {
					if !errors.Is(result.err, errPrintModeHandled) || result.stderr != codingagent.FormatNoAPIKeyFoundMessage("unknown")+"\n" {
						t.Fatalf("err=%v stderr=%q", result.err, result.stderr)
					}
				} else if result.err != nil || result.stderr != "" {
					t.Fatalf("handled prompt: err=%v stderr=%q", result.err, result.stderr)
				}
			})
		}
	}
}
