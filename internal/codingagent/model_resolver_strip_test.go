package codingagent

import (
	"fmt"
	"strings"
	"testing"
)

// A --model value whose lowercase form is longer in bytes than the original ("Ⱥ" is two bytes, its lowercase "ⱥ" three) must not be sliced by the lowered length.
func TestStrippedCliModelErrorSurvivesLengthChangingCase(t *testing.T) {
	for i, model := range []string{
		"openai/" + strings.Repeat("Ⱥ", 12),
		"OpenAI/" + strings.Repeat("Ⱥ", 12),
		"openai/" + strings.Repeat("İ", 12),
		strings.Repeat("Ⱥ", 12),
	} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			if err := strippedCliModelError("openai", model); err != nil {
				t.Fatalf("strippedCliModelError(openai, %q) = %v, want nil: Stock PiG strips no API", model, err)
			}
		})
	}
}

// resolveCliModel (model-resolver.ts:506-511) strips "<provider>/" from --model with cliModel.substring(prefix.length): the prefix's UTF-16 length, not
// the byte length of a lowercase form. "Ⱥ" is two UTF-8 bytes and its lowercase "ⱥ" three, so a byte slice of the original by the lowercase prefix
// either panics or cuts the model id short.
func TestResolveCliModelStripsProviderPrefixInUTF16Units(t *testing.T) {
	runtime := &initialModelTestRuntime{models: []RuntimeModel{{Provider: "ⱥⱥⱥ", ID: "x", Name: "x"}, {Provider: "ⱥⱥⱥ", ID: "longer-id", Name: "longer-id"}}}
	for _, row := range []struct{ model, id string }{{"ȺȺȺ/x", "x"}, {"ȺȺȺ/longer-id", "longer-id"}, {"ⱥⱥⱥ/x", "x"}} {
		got := ResolveCliModel("ⱥⱥⱥ", row.model, "", false, runtime)
		if got.Error != "" || got.Model == nil || got.Model.ID != row.id {
			t.Errorf("ResolveCliModel(ⱥⱥⱥ, %q) = %+v, want model %q", row.model, got, row.id)
		}
	}
}
