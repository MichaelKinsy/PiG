package sdk

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

func TestPromptSectionsPreserveAuthoredOrder(t *testing.T) {
	var data map[string]any
	raw := json.RawMessage(`{"systemPromptOptions":{"sections":{"z_base":"retained","plan_mode":"Plan only."}}}`)
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	options, err := preparePromptOptions(raw, data)
	if err != nil {
		t.Fatal(err)
	}
	sections := options["sections"].(*SystemPromptSections)
	sections.Set("a_first", "A")
	sections.Set("plan_mode", "Implementation allowed.")
	encoded, err := json.Marshal(sections)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"z_base":"retained","plan_mode":"Implementation allowed.","a_first":"A"}`
	if string(encoded) != want {
		t.Fatalf("sections=%s want=%s", encoded, want)
	}
	sections.Delete("plan_mode")
	sections.Set("plan_mode", "last")
	var restored SystemPromptSections
	encoded, err = json.Marshal(sections)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(restored, SystemPromptSections{{"z_base", "retained"}, {"a_first", "A"}, {"plan_mode", "last"}}) {
		t.Fatalf("sections=%+v", restored)
	}
}

func TestPromptSectionsRejectNonObjects(t *testing.T) {
	for _, input := range []string{`[]`, `null`, `"text"`, `{"name":1}`} {
		var sections SystemPromptSections
		if err := json.Unmarshal([]byte(input), &sections); err == nil {
			t.Errorf("accepted %s", input)
		}
	}
}
