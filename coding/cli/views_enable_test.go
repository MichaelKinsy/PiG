package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/coding/extension/pigsdk"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// viewsEntryEnv names the entry of the pig command the test binary runs as a fresh process (see TestPigEntriesEnableExtensionViews).
const viewsEntryEnv = "PIG_TEST_VIEWS_ENTRY"

// buildExtensionViews builds a view that holds an editor, a grid and an input, as the host does for a view an extension
// mounts. It fails with the sentence that names the call in a process that did not enable them.
func buildExtensionViews() error {
	surface := subprocess.NewMountSurface(subprocess.MountSurfaceOptions{Repaint: func() {}, Activate: func(subprocess.MountActivation) {}, Leave: func() {}})
	return surface.Accept(json.RawMessage(`{"root":{"kind":"vstack","children":[{"kind":"grid","id":"g","columns":1,"cells":[{"value":"a","label":"1"}]},{"kind":"input","id":"i"},{"kind":"editor","id":"e"}]}}`), "")
}

// The enables are the process's, so each entry runs in a process of its own: this test binary, started again with the
// entry's name. There the view builds only because the entry enabled it: the test binary enables nothing itself, which
// the child checks first. The entries are Main, which enables the views before it runs any command (a print run with no
// extensions here, which creates no host of its own), and the two places below Main that create an extension host of the
// command: the session's host and `pig extension preview-login`, which loads the extension it previews.
func TestPigEntriesEnableExtensionViews(t *testing.T) {
	if entry := os.Getenv(viewsEntryEnv); entry != "" {
		runViewsEntry(t, entry)
		return
	}
	for _, entry := range []string{"main", "host", "preview-login"} {
		t.Run(entry, func(t *testing.T) {
			child := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestPigEntriesEnableExtensionViews$", "-test.count=1", "-test.v")
			child.Env = append(os.Environ(), viewsEntryEnv+"="+entry)
			if out, err := child.CombinedOutput(); err != nil || !strings.Contains(string(out), "views-entry-ok "+entry) {
				t.Fatalf("entry %s: %v\n%s", entry, err, out)
			}
		})
	}
}

func runViewsEntry(t *testing.T, entry string) {
	if err := buildExtensionViews(); err == nil || !strings.Contains(err.Error(), "views are not enabled in this program") {
		t.Fatalf("before the entry ran: err = %v, want the view refused because nothing enabled it yet", err)
	}
	t.Setenv("PIG_HOME", t.TempDir())
	switch entry {
	case "main":
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("PIG_CODING_AGENT_DIR", filepath.Join(home, "agent"))
		t.Setenv("PIG_TEST_FAUX", "1")
		t.Setenv("PIG_OFFLINE", "1")
		Main([]string{"--model", "test-faux/faux-1", "--no-extensions", "--no-session", "--offline", "-p", "reply with exactly: ok"}, nil)
	case "host":
		host, _ := newSubprocessExtensionHost(t.TempDir(), extension.ModeTUI, nil, nil)
		t.Cleanup(func() { host.Shutdown("test done") })
	case "preview-login":
		useLoginPreviewTestBudget(t)
		t.Setenv("PIG_LOGIN_PREVIEW_FIXTURE", "valid")
		if err := pigsdk.EnsureSynced(codingagent.ConfigRoot()); err != nil {
			t.Fatal(err)
		}
		_, stderr, code := captureStdoutStderr(t, func() int {
			return runExtensionCommand([]string{"extension", "preview-login", "testdata/login-preview"})
		})
		if code != 0 {
			t.Fatalf("preview-login: code=%d stderr=%s", code, stderr)
		}
	default:
		t.Fatalf("unknown entry %q", entry)
	}
	if err := buildExtensionViews(); err != nil {
		t.Fatalf("after %s: a view with a grid, an input and an editor does not build: %v", entry, err)
	}
	t.Log("views-entry-ok " + entry)
}
