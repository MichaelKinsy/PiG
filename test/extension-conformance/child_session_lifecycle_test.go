package extensionconformance

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/test/extension-conformance/testfixture/childsession"
)

// The lifecycle rows run the Go fixture's scenarios against the golden Pi produced (TestNodeCreateAgentSessionLifecycleMatchesPi and TestNodeCreateAgentSessionModelErrorsMatchPi run the same scenarios through Pi's own createAgentSession and assert the same members).

func runChildTool(t *testing.T, ext extension.Extension, tool, args string) map[string]any {
	t.Helper()
	result, err := ext.Tools[tool].Definition.Execute(t.Context(), "call", json.RawMessage(args), nil)
	if err != nil {
		t.Fatal(err)
	}
	var observed map[string]any
	if err := json.Unmarshal([]byte(result.Text()), &observed); err != nil {
		t.Fatalf("%q: %v", result.Text(), err)
	}
	return observed
}

func loadChildFused(t *testing.T, host *subprocess.Host) extension.Extension {
	t.Helper()
	ext, err := host.LoadInProcess(t.Context(), subprocess.ExtConfig{Name: childsession.Name, Path: "/ext/child-session.go", Enabled: true}, childsession.Extension().RunWithConn)
	if err != nil {
		t.Fatal(err)
	}
	return *ext
}

func loadChildGo(t *testing.T, host *subprocess.Host, isolation string) extension.Extension {
	t.Helper()
	root := findModuleRoot(t)
	cfg := subprocess.ExtConfig{
		Name: childsession.Name, Source: filepath.Join(root, "test/extension-conformance/testdata/child-session-go"), Enabled: true, Isolation: isolation,
		RuntimeKind: "subprocess", RuntimeLanguage: "go", EntrypointKind: "factory", Factory: "Extension",
		SDKName: "github.com/MichaelKinsy/PiG/extensions/sdk", ModulePath: "example.com/child-session-go", Package: "example.com/child-session-go",
	}
	loaded, failures := host.LoadAll(t.Context(), []subprocess.ExtConfig{cfg})
	if len(failures) != 0 || len(loaded) != 1 {
		t.Fatalf("load: %v", failures)
	}
	return loaded[0]
}

func TestChildAgentSessionLifecycleMatchesPi(t *testing.T) {
	for name, load := range map[string]func(*testing.T, *subprocess.Host) extension.Extension{
		"fused":  loadChildFused,
		"strict": func(t *testing.T, host *subprocess.Host) extension.Extension { return loadChildGo(t, host, "strict") },
	} {
		t.Run(name, func(t *testing.T) {
			host, _ := newChildSessionHost(t)
			got := runChildTool(t, load(t, host), "child_lifecycle", `{}`)
			if !reflect.DeepEqual(got, childSessionWant()["lifecycle"]) {
				t.Fatalf("lifecycle = %v, want Pi's %v", got, childSessionWant()["lifecycle"])
			}
		})
	}
}

func TestChildAgentSessionModelErrorsMatchPi(t *testing.T) {
	for name, load := range map[string]func(*testing.T, *subprocess.Host) extension.Extension{
		"fused":  loadChildFused,
		"strict": func(t *testing.T, host *subprocess.Host) extension.Extension { return loadChildGo(t, host, "strict") },
	} {
		t.Run(name, func(t *testing.T) {
			host, _ := newChildSessionHostWith(t, false, nil)
			got := runChildTool(t, load(t, host), "child_errors", `{}`)
			if !reflect.DeepEqual(got, childSessionWant()["model-errors"]) {
				t.Fatalf("model errors = %v, want Pi's %v", got, childSessionWant()["model-errors"])
			}
		})
	}
}

// recordedChild records what the host does to a session: the prompt's return, the abort and the dispose.
type recordedChild struct {
	subprocess.ChildSession
	prompted, aborted, disposed atomic.Bool
}

func (r *recordedChild) Prompt(ctx context.Context, prompt subprocess.ChildSessionPrompt) error {
	defer r.prompted.Store(true)
	return r.ChildSession.Prompt(ctx, prompt)
}

func (r *recordedChild) Abort(ctx context.Context) error {
	r.aborted.Store(true)
	return r.ChildSession.Abort(ctx)
}

func (r *recordedChild) Dispose() {
	r.ChildSession.Dispose()
}

func (r *recordedChild) Close() {
	r.disposed.Store(true)
	r.ChildSession.Close()
}

// TestChildAgentSessionEndsWithItsConnection: a session left running belongs to the extension's connection, so closing the connection ends its run (the tool's context is cancelled, the prompt returns) and disposes it, as Node's independent-session-owner.mjs does when the extension runtime closes.
func TestChildAgentSessionEndsWithItsConnection(t *testing.T) {
	var recorded atomic.Pointer[recordedChild]
	host, _ := newChildSessionHostWith(t, true, func(session subprocess.ChildSession) subprocess.ChildSession {
		child := &recordedChild{ChildSession: session}
		recorded.Store(child)
		return child
	})
	ext := loadChildGo(t, host, "strict")
	marker := filepath.Join(t.TempDir(), "cancelled")
	if got := runChildTool(t, ext, "child_hold", `{"marker":`+quoteJSON(marker)+`}`); got["held"] != true {
		t.Fatalf("child_hold = %v", got)
	}
	child := recorded.Load()
	if child == nil || child.disposed.Load() {
		t.Fatal("the session must be live while its connection is open")
	}
	host.Shutdown("the test closes the extension's connection")
	deadline := time.Now().Add(20 * time.Second)
	for !child.disposed.Load() || !child.prompted.Load() {
		if time.Now().After(deadline) {
			t.Fatalf("after the connection closed: disposed=%v, prompt returned=%v; the host must end the run and dispose the session", child.disposed.Load(), child.prompted.Load())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Logf("the extension process ended before its tool recorded the cancellation (%v)", err)
	}
}

// TestChildAgentSessionErrorPaths: the calls that cannot succeed raise with the host's message. The messages that Pi has (a prompt to a running session) are in the Pi-derived rows above; these are the host's own, for the calls a Node extension never makes over a wire.
func TestChildAgentSessionErrorPaths(t *testing.T) {
	host, _ := newChildSessionHost(t)
	got := runChildTool(t, loadChildFused(t, host), "child_failures", `{}`)
	want := map[string]any{
		"unknownModel":            `Model nope/missing not found`,
		"customToolWithoutSchema": `Tool "no_schema" registered by extension "child-session" must define an object parameter schema.`,
		"steerIdle":               "resolved",
		"promptAfterDispose":      "resolved",
		"stateAfterDispose":       "resolved",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("failures = %v, want %v", got, want)
	}
}

func loadChildPython(t *testing.T, host *subprocess.Host, isolation string) extension.Extension {
	t.Helper()
	root := findModuleRoot(t)
	cfg := packedFlagFactory(t, root, "python", "child-session", false)
	cfg.Isolation = isolation
	cfg.ContentHash = "python-child-session-" + fmt.Sprint(len(childSessionPython))
	if err := os.WriteFile(filepath.Join(cfg.Source, cfg.Package+".py"), []byte(childSessionPython), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, failures := host.LoadAll(t.Context(), []subprocess.ExtConfig{cfg})
	if len(failures) != 0 || len(loaded) != 1 {
		t.Fatalf("load: %v", failures)
	}
	return loaded[0]
}

func loadChildRust(t *testing.T, host *subprocess.Host, isolation string) extension.Extension {
	t.Helper()
	root := findModuleRoot(t)
	cfg := packedFlagFactory(t, root, "rust", "child-session", false)
	cfg.Isolation = isolation
	cfg.ContentHash = "rust-child-session-" + fmt.Sprint(len(childSessionRust))
	if err := os.WriteFile(filepath.Join(cfg.Source, "src", "lib.rs"), []byte(childSessionRust), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := fmt.Sprintf("[package]\nname=\"child-session\"\nversion=\"0.0.0\"\nedition=\"2024\"\n[dependencies]\npig-sdk={path=%q}\nserde_json={version=\"1\",features=[\"preserve_order\"]}\n", filepath.ToSlash(filepath.Join(root, "extensions", "sdk-rs")))
	if err := os.WriteFile(filepath.Join(cfg.Source, "Cargo.toml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, failures := host.LoadAll(t.Context(), []subprocess.ExtConfig{cfg})
	if len(failures) != 0 || len(loaded) != 1 {
		t.Fatalf("load: %v", failures)
	}
	return loaded[0]
}

// TestChildAgentSessionNativeSDKsLifecycleAndModelErrors runs the Python and Rust fixtures' lifecycle and model-error scenarios against the same Pi-derived golden as the Go rows.
func TestChildAgentSessionNativeSDKsLifecycleAndModelErrors(t *testing.T) {
	for name, load := range map[string]func(*testing.T, *subprocess.Host, string) extension.Extension{"python": loadChildPython, "rust": loadChildRust} {
		t.Run(name, func(t *testing.T) {
			host, _ := newChildSessionHost(t)
			ext := load(t, host, "strict")
			if got := runChildTool(t, ext, "child_lifecycle", `{}`); !reflect.DeepEqual(got, childSessionWant()["lifecycle"]) {
				t.Fatalf("lifecycle = %v, want Pi's %v", got, childSessionWant()["lifecycle"])
			}
			errorsHost, _ := newChildSessionHostWith(t, false, nil)
			ext = load(t, errorsHost, "strict")
			if got := runChildTool(t, ext, "child_errors", `{}`); !reflect.DeepEqual(got, childSessionWant()["model-errors"]) {
				t.Fatalf("model errors = %v, want Pi's %v", got, childSessionWant()["model-errors"])
			}
		})
	}
}
