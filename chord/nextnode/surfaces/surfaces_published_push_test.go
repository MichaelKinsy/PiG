package surfaces

import (
	"context"
	"errors"
	"slices"
	"testing"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
)

// Ports Pi v1.7.0 published.ts & surface-build.ts tests:
// - unchanged script & stylesheet skips publishing a new set
// - failed push is retried on the next step and reports against the last pushed set
func TestPublishedScriptsSkipsUnchangedSetAndRetriesFailedPush(t *testing.T) {
	f := newPublishedFixture(t)
	initial := []plugins.PluginScript{built("notes", "d1", "function(){}")}
	if _, err := f.published.publish(initial); err != nil {
		t.Fatal(err)
	}
	if err := f.published.Push(chordctx.Background()); err != nil {
		t.Fatal(err)
	}
	firstSet := f.published.Set()

	// Same digest and stylesheet: keeps the current set.
	step := f.published.Step("web")
	res, err := step.Reload(nil, map[string][]plugins.PluginScript{"web": initial}, chordctx.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hosts[0].Report.Plugins.Changed) != 0 || len(res.Hosts[0].Report.Plugins.Added) != 0 || len(res.Hosts[0].Report.Plugins.Removed) != 0 {
		t.Fatalf("unchanged reload reported changes: %+v", res.Hosts[0].Report.Plugins)
	}
	if f.published.Set().Combined != firstSet.Combined {
		t.Fatalf("combined changed on identical script: %q vs %q", f.published.Set().Combined, firstSet.Combined)
	}

	// Cancel during push of a changed script: fails step, server still shows old set.
	updated := []plugins.PluginScript{built("notes", "d2", "function(){return 2}")}
	cancelled, cancel := context.WithCancelCause(chordctx.Background())
	cancel(errors.New("caller disconnected"))
	if _, err := step.Reload(nil, map[string][]plugins.PluginScript{"web": updated}, cancelled); err == nil {
		t.Fatal("expected error on cancelled push")
	}
	if shown := f.shown(); shown.Combined != firstSet.Combined {
		t.Fatalf("server set changed despite cancelled push: %q vs %q", shown.Combined, firstSet.Combined)
	}

	// Next apply with the same updated script pushes it and reports "notes" as changed against the last pushed set.
	retry, err := step.Reload(nil, map[string][]plugins.PluginScript{"web": updated}, chordctx.Background())
	if err != nil {
		t.Fatalf("retry reload: %v", err)
	}
	if !slices.Equal(retry.Hosts[0].Report.Plugins.Changed, []string{"notes"}) {
		t.Fatalf("retry changed = %v, want [notes]", retry.Hosts[0].Report.Plugins.Changed)
	}
	if shown := f.shown(); shown.Combined == firstSet.Combined {
		t.Fatalf("server set did not update on retry push")
	}
}
