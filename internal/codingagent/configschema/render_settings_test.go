package configschema_test

// pi: packages/coding-agent/src/core/settings-schema.ts

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/upstreammirror"
)

// Pi's settings-schema.ts builds the settings.json schema; scripts/generate-schemas.ts renders it to schemas/settings.schema.json. This
// compares the artifact Pig renders from its own settings schema (settings.go) with Pi's committed file in the upstream mirror, byte for
// byte, so every property, default and description Pi's schema declares (selfUpdateCommand from #11629, extensionChangelogVersions
// from #8790, the session share destination) is in Pig's schema. It is the settings.json
// part of TestRenderedSchemasAreByteEqualToTheUpstreamMirror, kept on its own so it speaks for settings-schema.ts alone.
func TestRenderedSettingsSchemaIsByteEqualToTheUpstreamMirror(t *testing.T) {
	const artifact = "schemas/settings.schema.json"
	want, err := upstreammirror.ReadPicked(repoRoot(t), "packages/coding-agent/"+artifact)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := renderedSchemas(t)[artifact]
	if !ok {
		t.Fatalf("Pig renders no %s", artifact)
	}
	if got != string(want) {
		t.Errorf("%s differs from Pi's committed file in .upstream/current/packages/coding-agent", artifact)
	}
}
