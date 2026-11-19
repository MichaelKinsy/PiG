//go:build !pig_strip_node_extensions

package coding

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// resource-loader.ts:747-790 loadFinalExtensionSet: across a trust reload the extensions the bootstrap pass loaded stay loaded and are not
// loaded again, so an extension's factory runs once. The extension logs each run, and the settings extension is user-scope, so the bootstrap
// pass (project untrusted) already loads it.
func TestDefaultResourceLoaderTrustReloadKeepsTheBootstrapExtensions(t *testing.T) {
	root := t.TempDir()
	cwd, agentDir := filepath.Join(root, "project"), filepath.Join(root, "agent")
	logFile := filepath.Join(root, "factory.log")
	ext := filepath.Join(root, "ok.ts")
	for path, content := range map[string]string{
		ext:                                      "import { appendFileSync } from \"node:fs\";\nexport default function (pi) { appendFileSync(" + strconv.Quote(logFile) + ", \"run\\n\"); pi.registerCommand(\"zz\", { description: \"d\", handler: async () => {} }); }\n",
		filepath.Join(agentDir, "settings.json"): `{"extensions": [` + strconv.Quote(ext) + `]}`,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	loader := NewDefaultResourceLoader(DefaultResourceLoaderOptions{CWD: cwd, AgentDir: agentDir})
	t.Cleanup(loader.Close)
	var bootstrap int
	err := loader.Reload(ResourceLoaderReloadOptions{ResolveProjectTrust: func(_ context.Context, input ResolveProjectTrustInput) (bool, error) {
		bootstrap = len(input.ExtensionsResult.Extensions)
		return true, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	final := loader.GetExtensions()
	data, _ := os.ReadFile(logFile)
	if bootstrap != 1 || len(final.Extensions) != 1 || len(final.Errors) != 0 {
		t.Fatalf("bootstrap extensions = %d, final extensions = %d, errors = %+v; want 1, 1 and none", bootstrap, len(final.Extensions), final.Errors)
	}
	if runs := strings.Count(string(data), "run"); runs != 1 {
		t.Fatalf("the factory ran %d times across the trust reload, want once", runs)
	}
}

// resource-loader.ts:513-600, :714-745: a settings extension loads through the real Extension Host (it registers its command, the extension
// runs in a live process), and Close stops that host.
func TestDefaultResourceLoaderSuccessfulLoadRunsInALiveHostThatCloseStops(t *testing.T) {
	root := t.TempDir()
	cwd, agentDir := filepath.Join(root, "project"), filepath.Join(root, "agent")
	pidFile := filepath.Join(root, "pid")
	ext := filepath.Join(root, "ok.ts")
	writeExtensionFiles(t, map[string]string{
		ext:                                      "import { writeFileSync } from \"node:fs\";\nexport default function (pi) { writeFileSync(" + strconv.Quote(pidFile) + ", String(process.pid)); pi.registerCommand(\"zz\", { description: \"d\", handler: async () => {} }); }\n",
		filepath.Join(agentDir, "settings.json"): `{"extensions": [` + strconv.Quote(ext) + `]}`,
		filepath.Join(cwd, "keep"):               "",
	})
	loader := NewDefaultResourceLoader(DefaultResourceLoaderOptions{CWD: cwd, AgentDir: agentDir})
	t.Cleanup(loader.Close)
	if err := loader.Reload(); err != nil {
		t.Fatal(err)
	}
	loaded := loader.GetExtensions()
	if len(loaded.Extensions) != 1 || len(loaded.Errors) != 0 || loaded.Runtime == nil {
		t.Fatalf("extensions = %d, errors = %+v, runtime = %v; want one extension, no error and a runtime", len(loaded.Extensions), loaded.Errors, loaded.Runtime)
	}
	if _, ok := loaded.Extensions[0].Commands["zz"]; !ok {
		t.Fatalf("commands of the loaded extension = %v, want zz registered by the factory", loaded.Extensions[0].Commands)
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || !extensionProcessAlive(pid) {
		t.Fatalf("extension process %q is not alive after the load: %v", data, err)
	}
	loader.Close()
	deadline := time.Now().Add(10 * time.Second)
	for extensionProcessAlive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("extension process %d still alive 10s after Close", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// resource-loader.ts:376-383, :714-745, :765-775: ExtensionFactories load with the default extension load. A `builtin:<name>` path runs the
// Builtin entry of that name (hidden), an inline factory loads last as `<inline:name>`, and the extensions of the files, built-ins and inline
// factories share one runtime.
func TestDefaultResourceLoaderRunsTheExtensionFactories(t *testing.T) {
	root := t.TempDir()
	cwd, agentDir := filepath.Join(root, "project"), filepath.Join(root, "agent")
	ext := filepath.Join(root, "ok.ts")
	writeExtensionFiles(t, map[string]string{
		ext:                                      "export default function (pi) { pi.registerCommand(\"file\", { description: \"d\", handler: async () => {} }); }\n",
		filepath.Join(agentDir, "settings.json"): `{"extensions": [` + strconv.Quote(ext) + `]}`,
		filepath.Join(cwd, "keep"):               "",
	})
	loader := NewDefaultResourceLoader(DefaultResourceLoaderOptions{CWD: cwd, AgentDir: agentDir, AdditionalExtensionPaths: []string{"builtin:alpha"}, ExtensionFactories: []extension.InlineExtension{
		extension.NamedInlineExtension{Name: "alpha", Builtin: true, Factory: commandFactory("alpha", nil)},
		commandFactory("inline", nil),
	}})
	t.Cleanup(loader.Close)
	if err := loader.Reload(); err != nil {
		t.Fatal(err)
	}
	loaded := loader.GetExtensions()
	var paths []string
	for _, e := range loaded.Extensions {
		paths = append(paths, e.Path)
	}
	if !slices.Equal(paths, []string{"builtin:alpha", ext, "<inline:1>"}) || len(loaded.Errors) != 0 {
		t.Fatalf("extension paths = %q, errors = %+v; want the built-in (a -e path comes first), the settings file, then the inline factory", paths, loaded.Errors)
	}
	if !loaded.Extensions[0].Hidden {
		t.Fatal("the built-in extension is not hidden")
	}
	if loaded.Runtime == nil || loader.files.host == nil || loaded.Runtime != loader.files.host.Runtime() {
		t.Fatalf("runtime %v is not the runtime of the Host (%v): the extensions do not share one runtime", loaded.Runtime, loader.files.host)
	}
}

// resource-loader.ts reload() has no caller whose end stops the extensions it loaded: the loader owns them until its next load or Close. The context of ReloadWith ends the load, not the extensions. A child agent session reloads its loader inside a wire call that returns long before the session ends; the extension it loaded must still receive the session's events. The handler answers over the extension's own connection, which the host closes with the context the extension was started under.
func TestDefaultResourceLoaderExtensionsOutliveTheContextOfTheReload(t *testing.T) {
	root := t.TempDir()
	cwd, agentDir := filepath.Join(root, "project"), filepath.Join(root, "agent")
	logFile := filepath.Join(root, "events.log")
	ext := filepath.Join(root, "ok.ts")
	writeExtensionFiles(t, map[string]string{
		ext:                                      "import { appendFileSync } from \"node:fs\";\nexport default function (pi) { pi.on(\"session_start\", async (event) => { appendFileSync(" + strconv.Quote(logFile) + ", event.reason + \"\\n\"); }); }\n",
		filepath.Join(agentDir, "settings.json"): `{"extensions": [` + strconv.Quote(ext) + `]}`,
		filepath.Join(cwd, "keep"):               "",
	})
	loader := NewDefaultResourceLoader(DefaultResourceLoaderOptions{CWD: cwd, AgentDir: agentDir})
	t.Cleanup(loader.Close)
	reloadCtx, endReload := context.WithCancel(t.Context())
	if err := loader.ReloadWith(reloadCtx); err != nil {
		t.Fatal(err)
	}
	endReload()
	loaded := loader.GetExtensions()
	if len(loaded.Extensions) != 1 || len(loaded.Errors) != 0 {
		t.Fatalf("extensions = %d, errors = %+v; want one extension and no error", len(loaded.Extensions), loaded.Errors)
	}
	handlers := loaded.Extensions[0].EventHandlers("session_start")
	if len(handlers) != 1 {
		t.Fatalf("session_start handlers = %d, want 1", len(handlers))
	}
	if _, err := handlers[0](extension.SessionStartEvent{Type: "session_start", Reason: "startup"}, t.Context()); err != nil {
		t.Fatalf("the extension did not receive session_start after the context of the reload ended: %v", err)
	}
	if data, _ := os.ReadFile(logFile); string(data) != "startup\n" {
		t.Fatalf("the handler recorded %q, want \"startup\\n\"", data)
	}
	if loaded.UIBridge() == nil {
		t.Fatal("the extensions' host has no bridge for a session to bind its state to")
	}
}
