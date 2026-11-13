package prompts

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// D645, the system prompt row. Pi's buildSystemPrompt reads `toolSnippets[name]` and `toolGuidelines[name] ?? []` on plain objects
// (packages/coding-agent/src/core/system-prompt.ts:157-159, :119), so a selected tool named like a member of Object.prototype finds the inherited
// function or object: the rules loop iterates it and throws a TypeError, and when the tool has guidelines of its own, so that the loop passes, the
// tools list carries a line with the function's source text. PiG's snippets and guidelines are Go maps: the tool has no snippet and no guideline and
// the prompt is built without a line for it. Each case holds what Pi's answer must start with in this run at the pin (the tool and rule lines, or the
// error) and PiG's tool and rule lines; the tool `plain` is the ordinary name and must be equal.
func TestASelectedToolNamedLikeAnObjectPrototypeMemberHasNoSnippetAndNoGuideline(t *testing.T) {
	type options struct {
		Cwd            string              `json:"cwd"`
		SelectedTools  []string            `json:"selectedTools"`
		ToolSnippets   map[string]string   `json:"toolSnippets"`
		ToolGuidelines map[string][]string `json:"toolGuidelines,omitempty"`
	}
	plain := `["- read: Read files","- Be concise in your responses","- Show file paths clearly when working with files"]`
	ruled := `["- read: Read files","- Its own rule","- Be concise in your responses"]`
	cases := []struct {
		tool       string
		guidelines map[string][]string
		pi, pig    string
	}{
		{"constructor", nil, "THROWS TypeError: function is not iterable", plain},
		{"toString", nil, "THROWS TypeError: function is not iterable", plain},
		{"__proto__", nil, "THROWS TypeError: object is not iterable", plain},
		{"constructor", map[string][]string{"constructor": {"Its own rule"}}, `["- read: Read files","- constructor: function Object() { [native code] }","- Its own rule"]`, ruled},
		{"plain", nil, plain, plain},
		{"plain", map[string][]string{"plain": {"Its own rule"}}, ruled, ruled},
	}
	probes := make([]options, len(cases))
	for i, c := range cases {
		probes[i] = options{Cwd: "/w", SelectedTools: []string{"read", c.tool}, ToolSnippets: map[string]string{"read": "Read files"}, ToolGuidelines: c.guidelines}
	}
	var pi []string
	pioracle.Run(t, `
const { buildSystemPrompt } = await load("pi-coding-agent/core/system-prompt.js");
emit(input.map((options) => {
	try { return JSON.stringify(buildSystemPrompt(options).split("\n").filter((line) => line.startsWith("- ")).slice(0, 3)); } catch (error) { return "THROWS " + String(error); }
}));`, probes, &pi)
	if len(pi) != len(cases) {
		t.Fatalf("Pi answered %d of %d option sets", len(pi), len(cases))
	}
	for i, c := range cases {
		var lines []string
		for _, section := range BuildSystemPromptSections(Options{Cwd: "/w", Tools: probes[i].SelectedTools, ToolHints: probes[i].ToolSnippets, ToolGuidelines: c.guidelines}) {
			if section.Name != "tools" && section.Name != "rules" {
				continue
			}
			for line := range strings.SplitSeq(*section.Value, "\n") {
				if strings.HasPrefix(line, "- ") && len(lines) < 3 {
					lines = append(lines, line)
				}
			}
		}
		encoded, err := json.Marshal(lines)
		if err != nil {
			t.Fatal(err)
		}
		pig := string(encoded)
		t.Logf("selected tools [read %s], guidelines %v: Pi %s, PiG %s", c.tool, c.guidelines, pi[i], pig)
		if !strings.HasPrefix(pi[i], c.pi) {
			t.Errorf("selected tools [read %s], guidelines %v: Pi answers %s, the record says it starts with %s", c.tool, c.guidelines, pi[i], c.pi)
		}
		if pig != c.pig {
			t.Errorf("selected tools [read %s], guidelines %v: PiG's tool and rule lines are %s, want %s", c.tool, c.guidelines, pig, c.pig)
		}
	}
}
