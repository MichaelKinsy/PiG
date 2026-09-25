package subprocess

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// Upstream loadExtensions records each failing extension and keeps loading the
// rest; /reload shows the failures under [Extension issues]. An unresolved
// config must not fail the reload or stop a healthy sibling, and it must be
// reported exactly once.
func TestReloadReportsUnresolvedConfigsWithoutFailing(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatalf("node is required for the extension fixture: %v", err)
	}
	fixture, err := filepath.Abs(filepath.Join("testdata", "ctx-mode.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	healthy := ExtConfig{Name: "ctx-mode", Source: fixture, Enabled: true}
	bad := UnresolvedExtConfig("/pkg/extensions/bad", errors.New("no factory"))
	configs := []ExtConfig{healthy, bad}
	h := NewHost(t.TempDir())
	h.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
	t.Cleanup(func() { h.Shutdown("test done") })

	loaded, err := h.Reload(t.Context())
	if err != nil {
		t.Fatalf("reload with an unresolved config failed: %v", err)
	}
	if len(loaded) != 1 || loaded[0].Name != "ctx-mode" {
		t.Fatalf("loaded = %#v, want the healthy sibling", loaded)
	}
	want := "/pkg/extensions/bad: Failed to load extension: no factory"
	if report := h.LastReloadReport(); len(report.Issues) != 1 || report.Issues[0] != want {
		t.Fatalf("issues = %q, want [%q]", report.Issues, want)
	}

	// The healthy extension's source stops resolving: reload drops it and
	// reports it instead of failing.
	configs = []ExtConfig{UnresolvedExtConfig(fixture, errors.New("broken")), bad}
	loaded, err = h.Reload(t.Context())
	if err != nil {
		t.Fatalf("reload after the source broke failed: %v", err)
	}
	if len(loaded) != 0 {
		t.Fatalf("loaded = %#v, want none", loaded)
	}
	if report := h.LastReloadReport(); len(report.Issues) != 2 || report.Issues[0] != fixture+": Failed to load extension: broken" {
		t.Fatalf("issues = %q", report.Issues)
	}
	if _, errs := h.LoadAll(t.Context(), []ExtConfig{bad}); len(errs) != 1 {
		t.Fatalf("LoadAll errors = %v, want the unresolved config reported", errs)
	}
}

// Upstream reload loads each extension on its own: one that fails to build or
// register is reported and not loaded, and every other extension loads. The
// failing extension's previous runtime is not kept.
func TestReloadIsolatesExtensionLoadFailures(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatalf("node is required for the extension fixture: %v", err)
	}
	fixture, err := filepath.Abs(filepath.Join("testdata", "ctx-mode.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(t.TempDir(), "broken.mjs")
	if err := os.WriteFile(broken, []byte("export default function () { throw new Error(\"register boom\"); }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	healthy := ExtConfig{Name: "ctx-mode", Source: fixture, Enabled: true}
	configs := []ExtConfig{healthy}
	h := NewHost(t.TempDir())
	h.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
	t.Cleanup(func() { h.Shutdown("test done") })
	if _, err := h.Reload(t.Context()); err != nil {
		t.Fatalf("initial reload: %v", err)
	}

	configs = []ExtConfig{healthy, {Name: "broken", Source: broken, Enabled: true}}
	loaded, err := h.Reload(t.Context())
	if err != nil {
		t.Fatalf("reload with a failing extension failed as a whole: %v", err)
	}
	if len(loaded) != 1 || loaded[0].Name != "ctx-mode" {
		t.Fatalf("loaded = %#v, want the healthy extension", loaded)
	}
	report := h.LastReloadReport()
	if report.Error != "" || len(report.Issues) != 1 || !strings.HasPrefix(report.Issues[0], broken+": Failed to load extension: ") {
		t.Fatalf("report error = %q, issues = %q", report.Error, report.Issues)
	}

	// The healthy extension's source now fails to register: its previous
	// runtime is stopped and the failure is reported.
	configs = []ExtConfig{{Name: "ctx-mode", Source: broken, Enabled: true}}
	loaded, err = h.Reload(t.Context())
	if err != nil {
		t.Fatalf("reload after the source broke failed as a whole: %v", err)
	}
	if len(loaded) != 0 {
		t.Fatalf("loaded = %#v, want the failed extension dropped", loaded)
	}
	if report := h.LastReloadReport(); len(report.Issues) != 1 || !slices.Contains(report.Removed, "ctx-mode") {
		t.Fatalf("issues = %q, removed = %q", report.Issues, report.Removed)
	}
}

// A packed cell that fails because of one member must not take the other
// members down: each member is loaded and reported on its own.
func TestPackedCellFailureIsolatesFailingMember(t *testing.T) {
	rootA := writePackedFactoryModule(t, "example.com/isolate/a", "iso-a", "tool_a")
	rootB := writePackedFactoryModule(t, "example.com/isolate/b", "iso-b", "tool_b")
	if err := os.WriteFile(filepath.Join(rootB, "ext.go"), []byte("package ext\n\nfunc Extension() { this does not compile }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	configs := []ExtConfig{
		packedFactoryConfig("iso-a", rootA, "example.com/isolate/a", "ia"),
		packedFactoryConfig("iso-b", rootB, "example.com/isolate/b", "ib"),
	}
	if cells := PlanCells(configs, nil); len(cells) != 1 || len(cells[0].Extensions) != 2 {
		t.Fatalf("cells = %#v, want both members in one packed cell", cells)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Second)
	defer cancel()

	h := NewHost(t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	loaded, errs := h.LoadAll(ctx, configs)
	if len(loaded) != 1 || loaded[0].Name != "iso-a" {
		t.Fatalf("loaded = %#v, want the healthy member", loaded)
	}
	if len(errs) != 1 {
		t.Fatalf("errs = %v, want only the failing member", errs)
	}
	if loadErr, ok := errors.AsType[*ExtensionLoadError](errs[0]); !ok || loadErr.Name != "iso-b" || loadErr.Path != rootB {
		t.Fatalf("err = %#v, want iso-b at %s", errs[0], rootB)
	}

	reloadHost := NewHost(t.TempDir())
	t.Cleanup(func() { reloadHost.Shutdown("test done") })
	reloadHost.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
	loaded, err := reloadHost.Reload(ctx)
	if err != nil {
		t.Fatalf("reload failed as a whole: %v", err)
	}
	if len(loaded) != 1 || loaded[0].Name != "iso-a" {
		t.Fatalf("reloaded = %#v, want the healthy member", loaded)
	}
	if report := reloadHost.LastReloadReport(); len(report.Issues) != 1 || !strings.HasPrefix(report.Issues[0], rootB+": Failed to load extension: ") {
		t.Fatalf("issues = %q", report.Issues)
	}
}

// An unresolved config is an enabled load attempt that is never planned: the
// planner skips it, so the cache and reload never try to build it, and its
// failure is reported exactly once.
func TestUnresolvedConfigIsEnabledButNeverPlanned(t *testing.T) {
	bad := UnresolvedExtConfig("/pkg/extensions/bad", errors.New("no factory"))
	if !bad.Enabled || bad.ResolveError() == nil {
		t.Fatalf("unresolved config = %#v, want an enabled failed attempt", bad)
	}
	if cells := PlanCells([]ExtConfig{bad}, nil); len(cells) != 0 {
		t.Fatalf("cells = %#v, want none", cells)
	}
	h := NewHost(t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	h.SetConfigLoader(func() ([]ExtConfig, error) { return []ExtConfig{bad}, nil })
	if _, err := h.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	if report := h.LastReloadReport(); len(report.Issues) != 1 || len(report.Cells) != 0 {
		t.Fatalf("issues = %q, cells = %#v", report.Issues, report.Cells)
	}
}

// Upstream puts the loader's error in "Failed to load extension: <message>".
// When an extension process exits before connecting, the load error carries
// the cause from its stderr, not only the path of the log holding it.
func TestLoadErrorCarriesExtensionProcessCause(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatalf("node is required for the extension fixture: %v", err)
	}
	dir := t.TempDir()
	cases := map[string]struct{ source, want string }{
		"syntax.ts":  {"export default function (pi) {\n  pi.registerCommand(\"x\", { handler: async () => { foo(, ) } });\n}\n", "SyntaxError"},
		"throws.mjs": {"export default function () { throw new Error(\"register boom\"); }\n", "register boom"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(dir, name)
			if err := os.WriteFile(path, []byte(tc.source), 0o644); err != nil {
				t.Fatal(err)
			}
			h := NewHost(t.TempDir())
			t.Cleanup(func() { h.Shutdown("test done") })
			_, errs := h.LoadAll(t.Context(), []ExtConfig{{Name: strings.TrimSuffix(name, filepath.Ext(name)), Source: path, Enabled: true}})
			if len(errs) != 1 || !strings.Contains(errs[0].Error(), tc.want) {
				t.Fatalf("errs = %v, want the cause %q in the message", errs, tc.want)
			}
		})
	}
}

// Upstream identifies an extension by its path: two copies of one extension
// from different Packages both load (their commands become ask:1 and ask:2).
// A duplicate identity must never stop every other extension from loading.
func TestDuplicateIdentitiesFromDifferentPathsBothLoad(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatalf("node is required for the extension fixture: %v", err)
	}
	dir := t.TempDir()
	write := func(name, body string) string {
		path := filepath.Join(dir, name, "ask.mjs")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	askSource := "export default function (pi) { pi.registerCommand(\"ask\", { description: \"ask\", handler: async () => {} }); }\n"
	first, second := write("one", askSource), write("two", askSource)
	other := write("other", "export default function (pi) { pi.registerCommand(\"other\", { description: \"other\", handler: async () => {} }); }\n")
	configs := []ExtConfig{
		{Name: "ask", Source: first, Enabled: true},
		{Name: "ask", Source: second, Enabled: true},
		{Name: "other", Source: other, Enabled: true},
	}
	h := NewHost(t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	loaded, errs := h.LoadAll(t.Context(), configs)
	if len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	var names []string
	for _, ext := range loaded {
		names = append(names, ext.Name)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"ask", "ask:2", "other"}) {
		t.Fatalf("loaded = %v, want both copies of ask and other", names)
	}

	h.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
	reloaded, err := h.Reload(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded) != 3 {
		t.Fatalf("reloaded = %d extensions, want 3", len(reloaded))
	}
}
