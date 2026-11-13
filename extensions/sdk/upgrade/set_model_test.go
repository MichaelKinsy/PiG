// SPDX-License-Identifier: MIT

package upgrade_test

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/upgrade"
)

// SDK 0.8.0's Context.SetModel takes Pi's model object and Context.SetModelNamed takes the `provider/id` name SetModel took before. A call whose
// argument is provably a string becomes SetModelNamed, whatever form the string has: a literal, a variable, an expression, a call's result, a
// parenthesized callee.
func TestSetModelWithAStringBecomesSetModelNamed(t *testing.T) {
	result, _ := planSource(t, `func pick() string { return "openai/gpt-5" }

func handler(ctx sdk.Context, args string) error {
	if _, err := ctx.SetModel("anthropic/claude-haiku-4-5"); err != nil {
		return err
	}
	name := "openai/" + args
	ok, err := ctx.SetModel(name)
	if err != nil || !ok {
		return err
	}
	_, _ = ctx.SetModel("openai/" + fmt.Sprint(args))
	_, _ = ctx.SetModel(pick())
	_, _ = (ctx.SetModel)(fmt.Sprintf("%s/%s", "acme", args))
	return nil
}
`)
	want := `func pick() string { return "openai/gpt-5" }

func handler(ctx sdk.Context, args string) error {
	if _, err := ctx.SetModelNamed("anthropic/claude-haiku-4-5"); err != nil {
		return err
	}
	name := "openai/" + args
	ok, err := ctx.SetModelNamed(name)
	if err != nil || !ok {
		return err
	}
	_, _ = ctx.SetModelNamed("openai/" + fmt.Sprint(args))
	_, _ = ctx.SetModelNamed(pick())
	_, _ = (ctx.SetModelNamed)(fmt.Sprintf("%s/%s", "acme", args))
	return nil
}
`
	if got := rewritten(t, result); got != want {
		t.Fatalf("rewritten source:\n%s\nwant:\n%s", got, want)
	}
	if len(result.Skipped) != 0 || len(result.Remaining) != 0 || len(result.Errors) != 0 {
		t.Fatalf("skipped %+v, remaining %+v, errors %v", result.Skipped, result.Remaining, result.Errors)
	}
	for _, change := range result.Files {
		for _, rewrite := range change.Rewrites {
			if rewrite.Rule != upgrade.RuleSetModelObject || rewrite.Symbol != "Context.SetModel" {
				t.Errorf("rewrite = %+v, want rule %s of Context.SetModel", rewrite, upgrade.RuleSetModelObject)
			}
		}
	}
}

// An argument that is not provably a string (an any, bytes, a defined string type SetModelNamed does not take) is not rewritten: the plan names its
// line and says what to write. The string call beside it is rewritten.
func TestSetModelWithAValueOfAnotherTypeIsListedWithItsLine(t *testing.T) {
	result, _ := planSource(t, `type modelName string

func handler(ctx sdk.Context, chosen any, raw []byte, named modelName) error {
	_, _ = ctx.SetModel(chosen)
	_, _ = ctx.SetModel(raw)
	_, _ = ctx.SetModel(named)
	_, _ = ctx.SetModel("acme/chat-1")
	return nil
}
`)
	if got := rewritten(t, result); !strings.Contains(got, "ctx.SetModel(chosen)") || !strings.Contains(got, "ctx.SetModel(raw)") || !strings.Contains(got, "ctx.SetModel(named)") || !strings.Contains(got, `ctx.SetModelNamed("acme/chat-1")`) {
		t.Fatalf("rewritten source:\n%s", got)
	}
	first := strings.Count(header, "\n") + 4
	if len(result.Skipped) != 3 {
		t.Fatalf("skipped = %+v, want the three calls whose argument is not a string", result.Skipped)
	}
	for i, skipped := range result.Skipped {
		if skipped.Rule != upgrade.RuleSetModelObject || skipped.Symbol != "Context.SetModel" || skipped.Line != first+i || !strings.Contains(skipped.Reason, "SetModelNamed") {
			t.Errorf("skipped[%d] = %+v, want rule %s of Context.SetModel at line %d", i, skipped, upgrade.RuleSetModelObject, first+i)
		}
	}
}

// A call that passes a model object already has the current shape: the plan changes nothing, whether the map is a literal, a variable or the registry's.
func TestSetModelWithAModelObjectIsLeftAlone(t *testing.T) {
	result, _ := planSource(t, `func handler(ctx sdk.Context) error {
	model, err := ctx.ModelRegistry().Find("acme", "chat-1")
	if err != nil {
		return err
	}
	model["contextWindow"] = 4321
	if _, err := ctx.SetModel(model); err != nil {
		return err
	}
	_, _ = ctx.SetModel(map[string]any{"provider": "acme", "id": "chat-2", "api": "openai-completions"})
	_, err = ctx.SetModelNamed("acme/chat-1")
	return err
}
`)
	if result.Changed() || len(result.Skipped) != 0 || len(result.Remaining) != 0 {
		t.Fatalf("the plan touches a current extension: files %d, skipped %+v, remaining %+v", len(result.Files), result.Skipped, result.Remaining)
	}
}

// A SetModel that is not the SDK Context's is another method: the plan leaves its call alone even though the compiler's message has the same shape.
func TestSetModelOfAnotherTypeIsNotRewritten(t *testing.T) {
	result, _ := planSource(t, `type picker struct{}

func (picker) SetModel(model map[string]any) bool { return model != nil }

func handler(ctx sdk.Context) bool {
	return picker{}.SetModel("acme/chat-1")
}
`)
	if result.Changed() || len(result.Skipped) != 0 {
		t.Fatalf("the plan touches a SetModel that is not the SDK's: files %d, skipped %+v", len(result.Files), result.Skipped)
	}
}
