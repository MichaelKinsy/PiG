package prompts

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// packages/coding-agent/test/system-prompt-updates.test.ts:84-110; system-prompt.ts:141-186 validates names before skipping empty values and preserves base/custom insertion order.
func TestCustomSystemPromptSections(t *testing.T) {
	base := BuildSystemPromptSections(Options{Cwd: "/tmp"})
	previous, err := ApplyCustomSystemPromptSections(base, ai.OrderedSections{{Name: "plan_mode", Value: new("Plan only.")}})
	if err != nil {
		t.Fatal(err)
	}
	current, err := ApplyCustomSystemPromptSections(base, ai.OrderedSections{{Name: "plan_mode", Value: new("Implementation allowed.")}})
	if err != nil {
		t.Fatal(err)
	}
	want := ai.OrderedSections{{Name: "plan_mode", Value: new("<plan_mode>\nImplementation allowed.\n</plan_mode>")}}
	if got := DiffSystemPromptSections(previous, current); !reflect.DeepEqual(got, want) {
		t.Fatalf("patch=%+v want=%+v", got, want)
	}
	if got := DiffSystemPromptSections(previous, previous); len(got) != 0 {
		t.Fatalf("unchanged patch=%+v", got)
	}
	if got := DiffSystemPromptSections(previous, base); !reflect.DeepEqual(got, ai.OrderedSections{{Name: "plan_mode"}}) {
		t.Fatalf("removed patch=%+v", got)
	}
	for _, name := range []string{"", "preamble", "UPPER", "space name", "1section", "tag>", "é", "a\n"} {
		if _, err := ApplyCustomSystemPromptSections(base, ai.OrderedSections{{Name: name, Value: new("")}}); err == nil || err.Error() != "Invalid system prompt section name: "+name {
			t.Errorf("name %q error=%v", name, err)
		}
	}
	custom := ai.OrderedSections{{Name: "z-last", Value: new("Z")}, {Name: "cwd", Value: new("overridden")}, {Name: "a_first", Value: new("A")}, {Name: "rules", Value: new("")}}
	got, err := ApplyCustomSystemPromptSections(base, custom)
	if err != nil {
		t.Fatal(err)
	}
	want = append(append(ai.OrderedSections(nil), base...), ai.PromptSection{Name: "z-last", Value: new("<z-last>\nZ\n</z-last>")}, ai.PromptSection{Name: "a_first", Value: new("<a_first>\nA\n</a_first>")})
	for i := range want {
		if want[i].Name == "cwd" {
			want[i].Value = new("<cwd>\noverridden\n</cwd>")
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ordered override=%+v want=%+v", got, want)
	}
	if !reflect.DeepEqual(base, BuildSystemPromptSections(Options{Cwd: "/tmp"})) {
		t.Fatal("custom sections mutated the baseline")
	}
}
