package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// In print mode and JSON mode no view client can attach, so ctx.ui.mountView is refused with code `unsupported` ("no client is attached"), and a reload in between changes
// nothing: there is never a mount to end as owner_gone (D614 reload rule, where a client can attach: interactive and RPC). The real binary runs three commands of the
// Node fixture ui-view-reload-modes.mjs in one run: a mount, a reload, a mount again; each appends what its view call returned to a file.
func TestPrintAndJSONModeRefuseAViewMountBeforeAndAfterAReload(t *testing.T) {
	if testing.Short() {
		t.Skip("starts pig and a Node extension")
	}
	fixture, err := filepath.Abs(filepath.Join("testdata", "ui-view-reload-modes.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	binary := buildPigBinaryForSignalTest(t)
	for _, mode := range []string{"text", "json"} {
		t.Run(mode, func(t *testing.T) {
			dir := listenTempDir(t)
			logPath := filepath.Join(dir, "views.log")
			cmd := exec.Command(binary, "--mode", mode, "-p", "--model", "test-faux/echo", "--no-session", "-e", fixture, "/view-first", "/view-reload", "/view-again")
			cmd.Env = append(append(os.Environ(), listenEnv(t, dir)...), "PIG_TEST_VIEW_LOG="+logPath)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("pig: %v\n%s", err, out)
			}
			got, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatal(err)
			}
			if want := "first:unsupported\nreload\nagain:unsupported\n"; string(got) != want {
				t.Fatalf("the commands logged %q, want %q: a mount is refused unsupported before and after the reload", got, want)
			}
		})
	}
}
