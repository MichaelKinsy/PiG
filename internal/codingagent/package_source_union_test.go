package codingagent

import (
	"encoding/json"
	"testing"
)

// Pi settings-manager.ts PackageSource = string | { source, extensions?, skills?, prompts?, themes?, autoload? }: both members of the union decode,
// the object form is remembered as filtered even with no filter fields, and each member re-encodes to its own form.
func TestPackageSourceUnionVariantsDecodeAndReencode(t *testing.T) {
	var bare PackageSource
	if err := json.Unmarshal([]byte(`"npm:pkg"`), &bare); err != nil || bare.Source != "npm:pkg" || bare.WasObject {
		t.Fatalf("string member decoded as %+v, err %v", bare, err)
	}
	var object PackageSource
	if err := json.Unmarshal([]byte(`{"source":"git:repo","extensions":["a.ts"],"autoload":false}`), &object); err != nil {
		t.Fatal(err)
	}
	if object.Source != "git:repo" || len(object.Extensions) != 1 || object.Autoload == nil || *object.Autoload || !object.WasObject {
		t.Fatalf("object member decoded as %+v", object)
	}
	var unfiltered PackageSource
	if err := json.Unmarshal([]byte(`{"source":"git:repo"}`), &unfiltered); err != nil || !unfiltered.WasObject {
		t.Fatalf("an object with no filter fields must stay an object: %+v, err %v", unfiltered, err)
	}
	for in, want := range map[string]PackageSource{`"npm:pkg"`: bare, `{"source":"git:repo"}`: unfiltered} {
		out, err := json.Marshal(want)
		if err != nil || string(out) != in {
			t.Fatalf("re-encoded %+v as %s (err %v), want %s", want, out, err, in)
		}
	}
}
