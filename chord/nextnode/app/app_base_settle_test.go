package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins/pluginstest"
)

// Not a Pi case (Pi's app.test.ts sleeps 200 ms after writeFile; Go's barrier pluginstest.Settle must wait for both
// the watcher's event handling and any base-file hash comparisons those events started). An in-place rewrite with the
// same bytes (truncate, then write) can be hashed empty on the first event, marking base, and then withdrawn once the
// second event's comparison sees the original bytes again; Settle must not return while that comparison is still in
// flight.
func TestAppBaseMarkIsWithdrawnAfterTruncateAndSameBytesRewriteOnceSettled(t *testing.T) {
	dir := t.TempDir()
	for file, text := range map[string]string{"api/v1.ts": "export {};", "start.ts": "export {};"} {
		path := filepath.Join(dir, file)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := startTestApp(t, map[string]map[string]string{"echo": {"server.ts": echoServer}}, func(o *AppOptions) {
		o.Base = []string{filepath.Join(dir, "api"), filepath.Join(dir, "start.ts")}
	})
	if s.app.Plugins.Component.Marks().Base {
		t.Fatal("base is marked at start")
	}
	changed := filepath.Join(dir, "api/v1.ts")
	same, err := os.ReadFile(changed)
	if err != nil {
		t.Fatal(err)
	}
	// Step 1 of an in-place os.WriteFile: truncate to 0 bytes, and wait until the watcher's comparison marks base.
	if err := os.Truncate(changed, 0); err != nil {
		t.Fatal(err)
	}
	untilMarks(t, s.app, "base marked by truncate", func(m plugins.PluginsState) bool { return m.Base })
	// Step 2: write the original bytes back (without truncating again), settle the watcher, and assert base cleared.
	file, err := os.OpenFile(changed, os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(same); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	pluginstest.Settle(t, s.app.Plugins.Component)
	if s.app.Plugins.Component.Marks().Base {
		t.Fatal("base is still marked after same bytes were written back and Settle returned")
	}
}
