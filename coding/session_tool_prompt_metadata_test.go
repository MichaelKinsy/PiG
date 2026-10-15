package coding

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi: packages/coding-agent/src/main.ts:849 (SessionOptions.customTools).
func TestSessionToolPromptUsesJavaScriptWhitespace(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/src/core/agent-session.ts:1347-1368 uses JS \s and trim, which include BOM but exclude NEL.
	definition := registryTool("custom", "Custom", "Custom operation", "\ufeffRun\n \u00a0custom  action\ufeff", "\ufeffkeep\ufeff", "keep", " \u0085 ")
	session := newRegistryPortSession(t, nil, SessionOptions{NoTools: "builtin", CustomTools: []extension.ToolDefinition{definition}}, nil, nil)
	assertRegistryPrompt(t, session, []string{"- custom: Run custom action", "- keep", "- \u0085"}, []string{"\ufeff"})
	if strings.Count(session.systemPrompt(), "\n- keep\n") != 1 {
		t.Fatal("normalized guideline was not deduplicated")
	}
	var lines []string
	for line := range strings.SplitSeq(session.systemPrompt(), "\n") {
		if strings.HasPrefix(line, "- custom:") || line == "- keep" || line == "- \u0085" {
			lines = append(lines, line)
		}
	}
	data, err := json.Marshal(lines)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("REGISTRY whitespace %s\n", data)
	for _, info := range session.GetAllTools() {
		if info.Name == "custom" && !reflect.DeepEqual(info.PromptGuidelines, definition.PromptGuidelines) {
			t.Fatal("metadata query changed the original guidelines")
		}
	}
}
