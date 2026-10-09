package ai

import (
	"os"
	"regexp"
	"testing"
)

var generatedUpstreamHeaderRE = regexp.MustCompile(`(?m)^// Upstream: @earendil-works/pi-ai (\S+)$`)

// TestGeneratedCatalogsRecordPinnedUpstream fails when a generated catalog was produced from a pi-ai release other than
// pigversion.UpstreamVersion. A pin bump without `make model-catalogs` leaves the previous release's catalog in place; the
// header cmd/gen-models writes from the installed package's own manifest makes that visible without any installed package.
func TestGeneratedCatalogsRecordPinnedUpstream(t *testing.T) {
	for _, file := range []string{"models_generated.go", "image_models_generated.go", "classifier_models_generated.go", "providers_generated.go"} {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		match := generatedUpstreamHeaderRE.FindSubmatch(data)
		if match == nil {
			t.Errorf("%s has no `// Upstream: @earendil-works/pi-ai <version>` header: run `make model-catalogs`", file)
			continue
		}
		if got := string(match[1]); got != UpstreamVersionString() {
			t.Errorf("%s was generated from pi-ai %s, but the pin is %s: run `make model-catalogs`", file, got, UpstreamVersionString())
		}
	}
}
