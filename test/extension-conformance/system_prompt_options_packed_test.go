package extensionconformance

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi system-prompt.ts:54-69 retains absent versus present-empty customPrompt. Registry and skill metadata accompany it in every realization, not just the nonempty conformance sentinel.
func TestSystemPromptOptionContentAcrossSDKs(t *testing.T) {
	cases := allHarnessCases()
	for _, language := range []string{"go", "rust", "python"} {
		cases = append(cases, harnessCase{name: "packed-" + language, make: func(t *testing.T) *harness { return makePackedUIHarness(t, language) }})
	}
	for _, present := range []bool{false, true} {
		name := "absent-custom"
		if present {
			name = "empty-custom"
		}
		t.Run(name, func(t *testing.T) {
			options := conformanceSystemPromptOptions()
			options.CustomPrompt = ""
			options.CustomPromptSet = present
			var want []string
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					h := tc.make(t)
					t.Cleanup(func() {
						if h.cleanup != nil {
							h.cleanup()
						}
						if h.host != nil {
							h.host.Shutdown("prompt option conformance complete")
						}
					})
					ctx := t.Context()
					if h.host == nil {
						h.runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{
							GetSystemPromptOptions: func() *extension.BuildSystemPromptOptions { return &options },
							IsProjectTrusted:       func() bool { return conformanceProjectTrusted },
						}, nil)
						cc := h.runner.CreateCommandContext()
						ctx = extension.WithCommandContext(extension.WithContext(ctx, cc.Context), cc)
					} else {
						h.bridge.SetHostAction("getSystemPromptOptions", func() extension.BuildSystemPromptOptions { return options })
					}
					command, ok := findCommand(h.runner, "context-probe")
					if !ok {
						t.Fatal("context-probe missing")
					}
					if err := command.Handler(ctx, ""); err != nil {
						t.Fatal(err)
					}
					// ctx.mode is delivered at ready and differs between harness families; TestConformance_TransportsMatch owns it. Compare the option content after it.
					got := make([]string, 0, len(*h.notify))
					for _, line := range *h.notify {
						_, rest, _ := strings.Cut(line, " trusted=")
						got = append(got, rest)
					}
					if h.host == nil {
						want = got
						// Bind the reference to Pi's values so a shared reference/SDK fallback cannot pass by agreement.
						expected := fmt.Sprintf("%t spo_prompt= spo_cwd=/probe-cwd spo_tools=read,bash spo_shape=selectedTools:array:2,toolSnippets:object:0,toolGuidelines:object:1,promptGuidelines:array:0,appendSystemPrompt:string:0,sections:object:0,contextFiles:array:0,skills:array:1 spo_guidelines=Read carefully. spo_skill_scope=project spo_force_empty=true spo_custom_present=%t:info", conformanceProjectTrusted, present)
						if len(want) != 1 || want[0] != expected {
							t.Fatalf("reference options = %v, want [%s]", *h.notify, expected)
						}
					} else if !reflect.DeepEqual(got, want) {
						t.Fatalf("%s options = %v, want %v", tc.name, got, want)
					}
				})
			}
		})
	}
}
