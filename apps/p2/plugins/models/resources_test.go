package models

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/models/contract"
)

func openFake(t *testing.T, dir string) *Resources {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	resources, err := OpenResources(ctx, ResourceOptions{Directory: dir, Fake: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resources.Close(context.Background()) })
	return resources
}

func TestResourcesLoadSaveAndNotify(t *testing.T) {
	dir := t.TempDir()
	resources := openFake(t, dir)
	snapshot := resources.Snapshot()
	if len(snapshot.Errors) != 0 || len(snapshot.Providers) == 0 || len(snapshot.Models) == 0 {
		t.Fatalf("first snapshot: %+v", snapshot)
	}
	var changes atomic.Int32
	stop := resources.Subscribe(func() { changes.Add(1) })
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	settings := resources.Settings()
	settings.Set("thinkingLevel", "low")
	if err := resources.SaveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	// The save resolves after its reload: the new settings are there, and a listener was told.
	if got := resources.Settings().Value("thinkingLevel"); got != "low" {
		t.Fatalf("thinkingLevel = %v", got)
	}
	if changes.Load() == 0 {
		t.Fatal("no change was published")
	}
	if resources.HarnessSettings() == nil {
		t.Fatal("no harness settings")
	}
	// An invalid save is refused before anything is written.
	invalid := resources.Settings()
	invalid.Set("thinkingLevel", "sideways")
	if err := resources.SaveSettings(ctx, invalid); err == nil {
		t.Fatal("an invalid settings.json was saved")
	}
	// An edit made outside is picked up by the watch; an invalid one is reported and the last valid state kept.
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"detail":`), 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for len(resources.ResourceErrors()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("an invalid settings.json was never reported")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if got := resources.ResourceErrors()[0]; got != (contract.ResourceError{Resource: "settings", Message: "Could not load settings resources; keeping last valid state"}) {
		t.Fatalf("error = %+v", got)
	}
	if resources.Settings().Value("thinkingLevel") != "low" {
		t.Fatal("the last valid settings were not kept")
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"detail":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for len(resources.ResourceErrors()) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("the error stayed after the file was fixed")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if resources.Settings().Value("detail") != float64(2) {
		t.Fatalf("settings = %+v", resources.Settings())
	}
}

func TestResourcesModelsJSONFailureKeepsLastModels(t *testing.T) {
	dir := t.TempDir()
	resources := openFake(t, dir)
	before := resources.Models()
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(`{"providers":{"p":{"name":"x"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := resources.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	// The first failure is read once more before it is reported; Reload returns once the loads caught up.
	deadline := time.Now().Add(30 * time.Second)
	for len(resources.ResourceErrors()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no error reported")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if resources.ResourceErrors()[0].Resource != "models" || resources.Models() != before {
		t.Fatalf("errors %+v; models replaced: %v", resources.ResourceErrors(), resources.Models() != before)
	}
}
