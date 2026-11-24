package configschema

import (
	"strings"
	"testing"
)

// Pi b82477b7b (#12134): OpenAICompletionsCompatSchema supportsDeveloperRole description
// specifies that system is the default and developer is true only for api.openai.com.
func TestOpenAICompletionsCompatSupportsDeveloperRoleDescription(t *testing.T) {
	const want = "Whether to send system messages of reasoning models (the system prompt and mid-conversation system messages) with the developer role instead of system. Default: true only for api.openai.com."
	for _, p := range propsOf(OpenAICompletionsCompatSchema()) {
		if p.Name != "supportsDeveloperRole" {
			continue
		}
		got, _ := p.Schema.get("description")
		if got != want {
			t.Fatalf("supportsDeveloperRole description =\n%q\nwant:\n%q", got, want)
		}
		if _, hasDefault := p.Schema.get("default"); hasDefault {
			t.Fatalf("completions supportsDeveloperRole should not declare a schema default")
		}
		return
	}
	t.Fatal("supportsDeveloperRole property not found in OpenAICompletionsCompatSchema")
}

func TestOpenAIResponsesCompatSupportsDeveloperRoleUnchanged(t *testing.T) {
	for _, p := range propsOf(OpenAIResponsesCompatSchema()) {
		if p.Name != "supportsDeveloperRole" {
			continue
		}
		got, _ := p.Schema.get("description")
		if s, ok := got.(string); !ok || !strings.Contains(s, "developer role instead of system") {
			t.Fatalf("unexpected responses description: %v", got)
		}
		def, ok := p.Schema.get("default")
		if !ok || def != true {
			t.Fatalf("responses supportsDeveloperRole default = %v (%v), want true", def, ok)
		}
		return
	}
	t.Fatal("supportsDeveloperRole property not found in OpenAIResponsesCompatSchema")
}
