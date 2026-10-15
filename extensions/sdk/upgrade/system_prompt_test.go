// SPDX-License-Identifier: MIT

package upgrade_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/upgrade"
)

// systemPromptSource reads before_agent_start's data["systemPrompt"] as a string in each form: a handler the package
// declares, a function literal, the ok form, a read inside an expression, and an if statement's init. The input
// handler's map is not before_agent_start's data, so its read stays.
const systemPromptSource = `func onStart(ctx sdk.Context, data map[string]any) (any, error) {
	prompt := data["systemPrompt"].(string)
	return map[string]any{"systemPrompt": prompt + "!"}, nil
}

func Extension() *sdk.Extension {
	ext := sdk.New("prompt")
	ext.OnEvent(sdk.EventBeforeAgentStart, onStart)
	ext.OnEvent("before_agent_start", func(ctx sdk.Context, data map[string]any) (any, error) {
		base, _ := data["systemPrompt"].(string)
		current, ok := data["systemPrompt"].(string)
		if !ok {
			return nil, nil
		}
		fmt.Println(len(data["systemPrompt"].(string)), base, current)
		if text, ok := data["systemPrompt"].(string); ok {
			fmt.Println(text)
		}
		return nil, nil
	})
	ext.OnEvent(sdk.EventInput, func(ctx sdk.Context, data map[string]any) (any, error) {
		text, _ := data["systemPrompt"].(string)
		return text, nil
	})
	return ext
}
`

// SDK 0.5.0 gives a before_agent_start handler data["systemPrompt"] as an *sdk.SystemPrompt, Pi's live getter. A read
// as a string still compiles but panics, or in its ok form reads an empty prompt, so the upgrade rewrites it to Get.
func TestBeforeAgentStartSystemPromptStringReadsBecomeGet(t *testing.T) {
	result, _ := planSourceRequiring(t, "v0.4.1", systemPromptSource)
	want := `func onStart(ctx sdk.Context, data map[string]any) (any, error) {
	prompt, err := data["systemPrompt"].(*sdk.SystemPrompt).Get()
	if err != nil {
		return nil, err
	}
	return map[string]any{"systemPrompt": prompt + "!"}, nil
}

func Extension() *sdk.Extension {
	ext := sdk.New("prompt")
	ext.OnEvent(sdk.EventBeforeAgentStart, onStart)
	ext.OnEvent("before_agent_start", func(ctx sdk.Context, data map[string]any) (any, error) {
		base, err := data["systemPrompt"].(*sdk.SystemPrompt).Get()
		if err != nil {
			return nil, err
		}
		systemPrompt, err := data["systemPrompt"].(*sdk.SystemPrompt).Get()
		if err != nil {
			return nil, err
		}
		current, ok := systemPrompt, true
		if !ok {
			return nil, nil
		}
		systemPrompt2, err := data["systemPrompt"].(*sdk.SystemPrompt).Get()
		if err != nil {
			return nil, err
		}
		fmt.Println(len(systemPrompt2), base, current)
		systemPrompt3, err := data["systemPrompt"].(*sdk.SystemPrompt).Get()
		if err != nil {
			return nil, err
		}
		if text, ok := systemPrompt3, true; ok {
			fmt.Println(text)
		}
		return nil, nil
	})
	ext.OnEvent(sdk.EventInput, func(ctx sdk.Context, data map[string]any) (any, error) {
		text, _ := data["systemPrompt"].(string)
		return text, nil
	})
	return ext
}
`
	if got := rewritten(t, result); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	for _, change := range result.Files {
		for _, rewrite := range change.Rewrites {
			if rewrite.Rule != upgrade.RuleSystemPromptGetter {
				t.Errorf("rewrite = %+v", rewrite)
			}
		}
	}
	if len(result.Skipped) != 0 || len(result.Remaining) != 0 || len(result.Errors) != 0 {
		t.Fatalf("skipped %+v, remaining %+v, errors %v", result.Skipped, result.Remaining, result.Errors)
	}
}

// An extension that requires SDK 0.5.0 was written for the *sdk.SystemPrompt value: its reads are its own.
func TestCurrentSystemPromptReadsNeedNoUpgrade(t *testing.T) {
	result, _ := planSourceRequiring(t, "v0.5.0", systemPromptSource)
	if result.Changed() || len(result.Skipped) != 0 || len(result.Remaining) != 0 || len(result.Errors) != 0 {
		t.Fatalf("files %d, skipped %+v, remaining %+v, errors %v", len(result.Files), result.Skipped, result.Remaining, result.Errors)
	}
}
