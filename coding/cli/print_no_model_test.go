package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
	configureFauxAuth(t, host.Services, "unknown")
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

// AgentSession.prompt (agent-session.ts:2037-2050) checks the selected provider's auth before the request: print and JSON mode
// fail with Pi's "No API key found" guidance and never start a turn, where PiG used to run the turn and let the provider fail
// with "provider is not configured".
func TestPrintModeRejectsAProviderWithoutAuthBeforeTheTurn(t *testing.T) {
	for _, mode := range []string{"text", "json"} {
		t.Run(mode, func(t *testing.T) {
			provider := ai.NewFauxProvider(ai.FauxConfig{})
			provider.SetResponses(fauxSteps(fauxTextResponse("must not run")))
			host := printModeTestHost(t, provider)
			host.Session.Model.ProviderMeta.ProviderID = "keyless"
			result := runPrintModeForTest(t, host, printModeOptions{Mode: mode, InitialMessage: "hello"})
			if !errors.Is(result.err, errPrintModeHandled) || result.stderr != codingagent.FormatNoAPIKeyFoundMessage("keyless")+"\n" {
				t.Fatalf("err=%v stderr=%q", result.err, result.stderr)
			}
			if strings.Contains(result.stdout, "agent_start") || strings.Contains(result.stdout, "must not run") {
				t.Fatalf("the turn ran: %s", result.stdout)
			}
		})
	}
}

// AgentSession.prompt (agent-session.ts:2095-2100, then 2118-2136) expands skill commands and prompt templates before it validates the selected model's auth, so a /skill: whose file cannot be read reports its skill_expansion error before the missing-auth error.
func TestPrintModeExpandsSkillCommandsBeforeTheAuthCheck(t *testing.T) {
	for _, mode := range []string{"text", "json"} {
		t.Run(mode, func(t *testing.T) {
			host := printModeTestHost(t, ai.NewFauxProvider(ai.FauxConfig{}))
			host.Session.Model.ProviderMeta.ProviderID = "keyless"
			skillFile := filepath.Join(host.Services.AgentDir(), "skills", "boom", "SKILL.md")
			writeResourceLoaderFixture(t, skillFile, "---\nname: boom\ndescription: Unreadable once the prompt starts.\n---\nbody\n")
			host.Session.ResourceLoader.(*cliResourceLoader).set(nil, []*codingagent.SkillDef{{Name: "boom", Description: "Unreadable once the prompt starts.", FilePath: skillFile, BaseDir: filepath.Dir(skillFile)}}, nil)
			host.Extensions = []extension.Extension{{Path: "remove-skill", Handlers: map[string][]extension.HandlerFn{
				"input": {func(...any) (any, error) { return nil, os.Remove(skillFile) }},
			}}}
			result := runPrintModeForTest(t, host, printModeOptions{Mode: mode, InitialMessage: "/skill:boom now"})
			want := "Extension error (" + skillFile + "): ENOENT: no such file or directory, open '" + skillFile + "'\n" + codingagent.FormatNoAPIKeyFoundMessage("keyless") + "\n"
			if !errors.Is(result.err, errPrintModeHandled) || result.stderr != want {
				t.Fatalf("err=%v stderr=%q, want %q", result.err, result.stderr, want)
			}
		})
	}
}
