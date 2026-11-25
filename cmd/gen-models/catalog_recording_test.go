package main

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// oracleProviderData is providers/data of the pi-ai the installed oracle runs: what Pi's own generator (packages/ai/catalog/generate.ts of the
// pinned build plus the overlay of automation/gen/catalog-source/pi-overlay) wrote over the committed recording (automation/pin/oracle.py).
const oracleProviderData = "../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/dist/providers/data"

// The port of the generator and Pi's generator, run over the one committed recording, write the same bytes: every data file and the manifest
// (its generatedAt is the recording's fetch time). Pi's output is the denominator, so a model this port drops, adds or changes fails here
// whatever our own catalogs say. `make model-catalogs` refuses to write the catalogs when this does not hold; this test keeps it true between runs.
func TestGeneratorMatchesPisGeneratorOnTheCommittedRecording(t *testing.T) {
	piData, err := os.ReadDir(oracleProviderData)
	if err != nil {
		pioracle.Missing(t, "the oracle Pi's provider data "+oracleProviderData, err)
	}
	source := filepath.Join("..", "..", "automation", "gen", "catalog-source")
	if _, err := os.Stat(filepath.Join(source, sourceMetaFile)); os.IsNotExist(err) {
		t.Skip("no committed catalog-source recording; bundled catalog uses the pinned Pi release frozen snapshot")
	}
	previous, previousAt := http.DefaultClient.Transport, generatedAtOverride
	t.Cleanup(func() { http.DefaultClient.Transport, generatedAtOverride = previous, previousAt })
	cleanup, err := replaySource(source)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	out := filepath.Join(t.TempDir(), "data")
	if err := generateModels(generatorOptions{strict: true, dataOutputDir: out}); err != nil {
		t.Fatal(err)
	}
	goData, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	names := func(entries []os.DirEntry) []string {
		var all []string
		for _, entry := range entries {
			all = append(all, entry.Name())
		}
		return all
	}
	if got, want := names(goData), names(piData); !slices.Equal(got, want) {
		t.Fatalf("this generator wrote %d files, Pi's %d:\n got %v\nwant %v", len(got), len(want), got, want)
	}
	for _, name := range names(piData) {
		want, err := os.ReadFile(filepath.Join(oracleProviderData, name))
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(out, name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s: %d bytes from this generator, %d from Pi's", name, len(got), len(want))
		}
	}
}
