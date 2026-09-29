package main

import (
	"fmt"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func TestCLIEndOfOptionsUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/7269-cli-end-of-options.test.ts:14
	for _, prompt := range []string{"- summarize the following points for me", "--answer my question briefly"} {
		t.Run(fmt.Sprintf("passes %q as a prompt after --", prompt), func(t *testing.T) {
			parsed := parseFlags([]string{"-ne", "--no-session", "-p", "--", prompt})
			if !slices.Equal(parsed.Args, []string{prompt}) || len(parsed.UnknownFlags) != 0 || len(parsed.Diagnostics) != 0 {
				t.Fatalf("parsed = %+v", parsed)
			}
			provider := ai.NewFauxProvider(ai.FauxConfig{})
			var userTexts []string
			provider.SetResponses([]ai.FauxResponseStep{ai.FauxFactoryStep(func(ctx ai.TranscriptContext, _ ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.FauxResponse, error) {
				for _, message := range ctx.Messages() {
					if user, ok := message.(ai.UserMessage); ok {
						switch content := user.Content.(type) {
						case ai.UserText:
							userTexts = append(userTexts, string(content))
						case ai.UserContentBlocks:
							for _, block := range content {
								if text, ok := block.(ai.TextContent); ok {
									userTexts = append(userTexts, text.Text)
								}
							}
						}
					}
				}
				return fauxTextResponse("ok"), nil
			})})
			result := runPrintModeForTest(t, printModeTestHost(t, provider), printModeOptions{Mode: parsed.Mode, InitialMessage: parsed.Args[0]})
			if result.err != nil || result.stderr != "" || result.stdout != "ok\n" {
				t.Fatalf("print result = %+v", result)
			}
			if !slices.Equal(userTexts, []string{prompt}) {
				t.Fatalf("user texts = %q, want %q", userTexts, prompt)
			}
		})
	}
	// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/7269-cli-end-of-options.test.ts:29
	t.Run("stops parsing options while retaining @file handling", func(t *testing.T) {
		parsed := parseFlags([]string{"--unknown-flag", "value", "--", "--provider", "openai", "-c", "@prompt.md"})
		if parsed.UnknownFlags["unknown-flag"] != "value" || len(parsed.UnknownFlags) != 1 || parsed.Provider != "" || parsed.Continue || !slices.Equal(parsed.Args, []string{"--provider", "openai", "-c"}) || !slices.Equal(parsed.FileArgs, []string{"prompt.md"}) || len(parsed.Diagnostics) != 0 {
			t.Fatalf("parsed = %+v", parsed)
		}
	})
}
