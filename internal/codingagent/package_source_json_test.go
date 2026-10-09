package codingagent

import (
	"encoding/json"
	"reflect"
	"testing"
)

// Upstream settings-manager.ts PackageSource is `string | { source, autoload?, extensions?, skills?, prompts?, themes? }`.
// The settings file keeps whichever form the user wrote, and an object with no filter fields is still filtered.
func TestPackageSourceJSONKeepsTheStringOrObjectForm(t *testing.T) {
	yes := true
	for _, tc := range []struct {
		name, in, out string
		want          PackageSource
		filtered      bool
	}{
		{"string", `"npm:a"`, `"npm:a"`, PackageSource{Source: "npm:a"}, false},
		{"bare object", `{"source":"npm:a"}`, `{"source":"npm:a"}`, PackageSource{Source: "npm:a", WasObject: true}, true},
		{"every field", `{"source":"git:b","autoload":true,"extensions":["e"],"skills":[],"prompts":["p"],"themes":["t"]}`,
			`{"autoload":true,"extensions":["e"],"prompts":["p"],"skills":[],"source":"git:b","themes":["t"]}`,
			PackageSource{Source: "git:b", Autoload: &yes, Extensions: []string{"e"}, Skills: []string{}, Prompts: []string{"p"}, Themes: []string{"t"}, WasObject: true}, true},
	} {
		var got PackageSource
		if err := json.Unmarshal([]byte(tc.in), &got); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if !reflect.DeepEqual(got, tc.want) || got.Filtered() != tc.filtered {
			t.Errorf("%s: decoded %+v (filtered %v), want %+v (filtered %v)", tc.name, got, got.Filtered(), tc.want, tc.filtered)
		}
		out, err := json.Marshal(got)
		if err != nil || string(out) != tc.out {
			t.Errorf("%s: encoded %s %v, want %s", tc.name, out, err, tc.out)
		}
	}
	if err := json.Unmarshal([]byte(`3`), new(PackageSource)); err == nil {
		t.Error("a number is neither a string nor an object")
	}
}
