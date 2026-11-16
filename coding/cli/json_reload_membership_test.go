package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Pi's reload() discovers the extensions again (resource-loader.ts DefaultResourceLoader.reload) in JSON mode as in the others: a command that changes the extensions folder and awaits ctx.reload() leaves the extensions that are on disk loaded.

// runJSONReload runs `pig --mode json -p /act-and-reload` over a membership fixture and waits for the process to end.
func runJSONReload(t *testing.T, f membershipFixture) {
	t.Helper()
	cmd := exec.CommandContext(testbudget.Context(t), buildPigBinaryForSignalTest(t), "--mode", "json", "--no-session", "--model", "test-faux/faux-1", "-p", "/act-and-reload")
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), f.env()...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pig --mode json: %v\n%s", err, out)
	}
}

func requireMarker(t *testing.T, path, why string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("%s: %v", why, err)
	}
}

// A removed sibling stays configured when a mode reloads the startup set, and in a shared Node cell its missing file fails the load of the caller beside it.
func TestJSONReloadDropsAnExtensionRemovedFromDisk(t *testing.T) {
	f := nodeMembershipFixture(t, "remove")
	runJSONReload(t, f)
	requireMarker(t, f.marker, "the command that awaited ctx.reload() did not resolve after its sibling's file was removed")
}

// An extension file that appears before the reload is loaded by it: its factory writes a file.
func TestJSONReloadLoadsAnExtensionAddedToDisk(t *testing.T) {
	f := membershipFixture{home: t.TempDir()}
	loaded := filepath.Join(f.home, "added-loaded")
	extensions := filepath.Join(f.home, "agent", "extensions")
	sibling := filepath.Join(extensions, "sibling.js")
	source := fmt.Sprintf("import {writeFileSync} from \"node:fs\";\nwriteFileSync(%q, \"1\");\nexport default function (pi) {}\n", loaded)
	writeFixtureFile(t, filepath.Join(extensions, "caller.js"), fmt.Sprintf(`import {writeFileSync} from "node:fs";
export default function (pi) {
  pi.registerCommand("act-and-reload", {description: "add the sibling and reload", handler: async (_args, ctx) => {
    writeFileSync(%q, %q);
    await ctx.reload();
  }});
}
`, sibling, source))
	runJSONReload(t, f)
	requireMarker(t, loaded, "the extension added before ctx.reload() was not loaded")
}
