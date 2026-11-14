package extensionconformance

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// pi: packages/coding-agent/src/core/extensions/loader.ts registerTool (a tool definition is kept as the extension gave it)

// A tool declaration whose description, parameter name and parameter text hold a lone UTF-16 surrogate reaches the host with each unit kept, from
// every SDK whose strings can hold one, isolated and packed: JavaScript (a string is UTF-16 units), Python (a str holds the code point and json.dumps
// writes its escape), Lisp (a character can be a surrogate code point and the SDK's writer writes its escape) and Go (the three-byte form, which the
// SDK's encoder writes as the escape). The host forwards it as JSON.stringify writes it. Rust cannot express one: a String is UTF-8 by its type's
// invariant and a serde_json::Value holds Strings, so its fixture declares U+FFFD, which is forwarded as it is.
func TestConformance_ToolDeclarationLoneSurrogatesAgreeAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	kept := []string{`"description":"D[\ud83d]"`, `"K[\ude00\ud83d]":{`, `"description":"PD[\ud83d]"`, `"enum":["E[\ude00]"]`, `"default":"DF[\ud83d]"`}
	replaced := strings.NewReplacer(`\ude00`, "\uFFFD", `\ud83d`, "\uFFFD")
	for _, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			tool, ok := findTool(h.runner, "lone_decl")
			if !ok {
				t.Fatal("lone_decl not registered")
			}
			var schema ai.ToolSchema
			if err := schema.UnmarshalJSON([]byte(`{"parameters":` + string(tool.Definition.Parameters) + `}`)); err != nil {
				t.Fatal(err)
			}
			schema.Name, schema.Description = tool.Definition.Name, tool.Definition.Description
			line, err := jsstring.MarshalJSON(schema)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range kept {
				if strings.HasPrefix(tc.name, "subprocess-rust") {
					want = replaced.Replace(want)
				}
				if !strings.Contains(string(line), want) {
					t.Errorf("the declaration the host holds, written as a session line, lacks %s: %s", want, line)
				}
			}
		})
	}
}
