package extensionconformance

// A Piglet Binary built with `build.fusedReload: source` runs a fused Go member whose source changed as a subprocess built from that
// source (D31). These tests drive the extension host as such a Binary does: a fused resolver that knows the digest baked for the
// member, and a configuration of the member's name that carries its source.

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/fusepack"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

const hybridName = "hybrid"

// hybridSource is the member's source: its version command answers version, and it appends each session event it gets to the log.
const hybridSource = `package hybridext

import (
	"fmt"
	"os"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

const version = %q

func record(event string) sdk.EventFunc {
	return func(_ sdk.Context, data map[string]any) (any, error) {
		if log := os.Getenv("PIG_HYBRID_LOG"); log != "" {
			f, err := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
			if err != nil {
				return nil, err
			}
			defer func() { _ = f.Close() }()
			_, _ = fmt.Fprintf(f, "%%s %%s %%v\n", %q, event, data["reason"])
		}
		return nil, nil
	}
}

func Extension() *sdk.Extension {
	e := sdk.New(%q)
	e.Command("noop", "Return at once", func(sdk.Context, string) error { return nil })
	e.Command("version", "Notify the version", func(ctx sdk.Context, _ string) error { ctx.Notify(version, "info"); return nil })
	e.Command("decorate", "Set a status and a widget", func(ctx sdk.Context, _ string) error {
		ctx.SetStatus("hybrid", "on")
		return ctx.SetWidget("hybrid", []string{"line"})
	})
	e.OnSessionStart(record("session_start"))
	e.OnSessionShutdown(record("session_shutdown"))
	return e
}
`

// hybridFusedCopy is the copy of the member compiled into the binary: the same registrations, answering "fused".
func hybridFusedCopy(name string) *sdk.Extension {
	record := func(event string) sdk.EventFunc {
		return func(_ sdk.Context, data map[string]any) (any, error) {
			if log := os.Getenv("PIG_HYBRID_LOG"); log != "" {
				f, err := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
				if err != nil {
					return nil, err
				}
				defer func() { _ = f.Close() }()
				_, _ = fmt.Fprintf(f, "%s %s %v\n", name, event, data["reason"])
			}
			return nil, nil
		}
	}
	e := sdk.New(name)
	e.Command("noop", "Return at once", func(sdk.Context, string) error { return nil })
	e.Command("version", "Notify the version", func(ctx sdk.Context, _ string) error { ctx.Notify("fused", "info"); return nil })
	e.Command("decorate", "Set a status and a widget", func(ctx sdk.Context, _ string) error {
		ctx.SetStatus("hybrid", "on")
		return ctx.SetWidget("hybrid", []string{"line"})
	})
	e.OnSessionStart(record("session_start"))
	e.OnSessionShutdown(record("session_shutdown"))
	return e
}

// hybridFused is the resolver of a Piglet Binary that fused the members of baked, built with `build.fusedReload: source`.
type hybridFused struct{ baked map[string]string }

func (r hybridFused) FusedServe(cfg subprocess.ExtConfig) (func(net.Conn) error, bool) {
	if _, ok := r.baked[cfg.Name]; !ok {
		return nil, false
	}
	return func(conn net.Conn) error {
		return sdk.ServeFactory(conn, func() *sdk.Extension { return hybridFusedCopy(cfg.Name) })
	}, true
}

func (r hybridFused) BakedSourceDigest(name string) (string, bool) {
	digest, ok := r.baked[name]
	return digest, ok
}

// hybridNoKey is the same Binary built without the key: it fused the members and baked no digest, so it is no subprocess.FusedSourceResolver.
type hybridNoKey struct{ inner hybridFused }

func (r hybridNoKey) FusedServe(cfg subprocess.ExtConfig) (func(net.Conn) error, bool) {
	return r.inner.FusedServe(cfg)
}

func hybridEdit(t *testing.T, dir, name, version string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "extension.go"), fmt.Appendf(nil, hybridSource, version, name, name), 0o600); err != nil {
		t.Fatal(err)
	}
}

// hybridConfig writes the source of member name at version into dir and returns the configuration that carries it.
func hybridConfig(t *testing.T, dir, name, version string) subprocess.ExtConfig {
	t.Helper()
	module := "example.test/" + strings.ReplaceAll(name, "-", "")
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module "+module+"\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	hybridEdit(t, dir, name, version)
	return subprocess.ExtConfig{Name: name, Source: dir, Enabled: true, RuntimeKind: "subprocess", RuntimeLanguage: "go", Isolation: "isolated", EntrypointKind: "factory",
		SDKName: "github.com/MichaelKinsy/PiG/extensions/sdk", ModulePath: module, Package: module, Factory: "Extension"}
}

func hybridDigest(t *testing.T, dir string) string {
	t.Helper()
	digest, err := subprocess.FusedSourceDigest(dir)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

// installHybrid installs resolver as the Binary's fused resolver for the test.
func installHybrid(t *testing.T, resolver subprocess.FusedResolver) {
	t.Helper()
	sdk.SetHostHelpers(fusepack.HostHelpers())
	subprocess.SetFusedResolver(resolver)
	t.Cleanup(func() { subprocess.SetFusedResolver(nil) })
}

type hybridHost struct {
	host    *subprocess.Host
	dir     string
	configs []subprocess.ExtConfig
	loaded  []extension.Extension
	errs    []error
	runner  *inproc.Runner
	notices chan string
}

// startHybridHost starts a host in configRoot (a new directory when empty) and loads configs.
func startHybridHost(t *testing.T, configRoot string, configs ...subprocess.ExtConfig) *hybridHost {
	t.Helper()
	return startHybridHostWithUI(t, configRoot, newRecordingUI(new([]string), new([]string)), configs...)
}

// startHybridHostWithUI is startHybridHost drawing on ui.
func startHybridHostWithUI(t *testing.T, configRoot string, ui extension.UIContext, configs ...subprocess.ExtConfig) *hybridHost {
	t.Helper()
	s := &hybridHost{dir: t.TempDir(), configs: configs, notices: make(chan string, 64)}
	if configRoot == "" {
		s.host = subprocess.NewHost(s.dir)
	} else {
		s.host = subprocess.NewHostWithConfigRoot(s.dir, configRoot)
	}
	t.Cleanup(func() { s.host.Shutdown("done") })
	bridge := subprocess.NewUIBridge(func() {})
	bridge.SetUIContext(ui)
	bridge.SetNotifyFunc(func(message, _ string) { s.notices <- message })
	s.host.SetUIBridge(bridge)
	s.host.SetConfigLoader(func() ([]subprocess.ExtConfig, error) { return slices.Clone(configs), nil })
	s.loaded, s.errs = s.host.LoadAll(t.Context(), slices.Clone(configs))
	if len(s.loaded) != len(configs) {
		t.Fatalf("load: %d of %d loaded, %v", len(s.loaded), len(configs), s.errs)
	}
	s.runner = inproc.NewRunner(s.loaded, s.dir)
	return s
}

// reload runs the reload a session runs: session_shutdown with reason reload, Host.Reload, a new runner, session_start with reason reload.
func (s *hybridHost) reload(t *testing.T) *subprocess.ReloadReport {
	t.Helper()
	if _, err := s.runner.Emit(t.Context(), extension.SessionShutdownEvent{Type: "session_shutdown", Reason: "reload"}); err != nil {
		t.Fatal(err)
	}
	s.runner.Invalidate("")
	loaded, err := s.host.Reload(t.Context())
	report := s.host.LastReloadReport()
	if err != nil || len(loaded) != len(s.configs) {
		t.Fatalf("reload: %v, %d loaded, %+v", err, len(loaded), report)
	}
	s.loaded, s.runner = loaded, inproc.NewRunner(loaded, s.dir)
	if _, err := s.runner.Emit(t.Context(), extension.SessionStartEvent{Type: "session_start", Reason: "reload"}); err != nil {
		t.Fatal(err)
	}
	return report
}

func (s *hybridHost) member(t *testing.T, name string) extension.Extension {
	t.Helper()
	for _, ext := range s.loaded {
		if ext.Name == name {
			return ext
		}
	}
	t.Fatalf("no extension %s loaded", name)
	return extension.Extension{}
}

// version asks member name for its version.
func (s *hybridHost) version(t *testing.T, name string) string {
	t.Helper()
	if err := s.member(t, name).Commands["version"].Handler(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	select {
	case v := <-s.notices:
		return v
	case <-time.After(30 * time.Second):
		t.Fatal("no version notice")
		return ""
	}
}

// listing is what an extension listing shows of the loaded extensions: name, path, resolved path and source.
func (s *hybridHost) listing() string {
	var b strings.Builder
	for _, ext := range s.loaded {
		_, _ = fmt.Fprintf(&b, "%s | %s | %s | %+v\n", ext.Name, ext.Path, ext.ResolvedPath, ext.SourceInfo)
	}
	return b.String()
}

func hybridSettledGoroutines() int {
	n := -1
	for range 50 {
		runtime.GC()
		now := runtime.NumGoroutine()
		if now == n {
			return n
		}
		n = now
		time.Sleep(100 * time.Millisecond)
	}
	return n
}

func skipWithoutGoBuilds(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("builds a Go extension")
	}
}

// TestFusedMemberReloadsFromItsEditedSource: after an edit of its source and a reload, a fused member answers from the edited source,
// as the same extension loaded from source does (docs/site/docs/piglet-binaries.md: fused behavior must match source subprocess
// behavior). Before the hybrid the reload ran the compiled factory again and the member answered "fused". The fused copy is stopped
// as one unit, the listing does not change, and the placement names the source.
func TestFusedMemberReloadsFromItsEditedSource(t *testing.T) {
	skipWithoutGoBuilds(t)
	for _, selected := range []string{"", "builtin:" + hybridName} {
		t.Run("selected as "+selected, func(t *testing.T) {
			source := t.TempDir()
			cfg := hybridConfig(t, source, hybridName, "v0")
			if selected != "" {
				cfg = cfg.SelectedAs(selected)
			}
			installHybrid(t, hybridFused{baked: map[string]string{hybridName: hybridDigest(t, source)}})
			s := startHybridHost(t, "", cfg)
			if len(s.errs) != 0 {
				t.Fatalf("load: %v", s.errs)
			}
			if v := s.version(t, hybridName); v != "fused" {
				t.Fatalf("the member answers %q before the edit, want the copy compiled into the binary", v)
			}
			listing, old := s.listing(), s.member(t, hybridName)
			if selected != "" && !strings.Contains(listing, selected) {
				t.Fatalf("listing %q does not show %s", listing, selected)
			}
			// An unchanged source reloads fused.
			if report := s.reload(t); len(report.Cells) != 0 || len(report.Issues) != 0 {
				t.Fatalf("reload of the unchanged member: %+v", report)
			}
			if v := s.version(t, hybridName); v != "fused" {
				t.Fatalf("the unchanged member answers %q after a reload", v)
			}

			hybridEdit(t, source, hybridName, "v1")
			report := s.reload(t)
			if v := s.version(t, hybridName); v != "v1" {
				t.Fatalf("FUSEDEDITROW after an edit of its source and a reload the fused member answers %q, want v1 from the edited source", v)
			}
			if len(report.Issues) != 0 || len(report.Cells) != 1 {
				t.Fatalf("reload report: %+v", report)
			}
			if cell := report.Cells[0]; cell.Strategy != subprocess.CellStrategyIsolated || cell.Reason != "subprocess, rebuilt from "+source || !cell.Replaced {
				t.Fatalf("placement = %s %q replaced=%t, want isolated, %q, replacing the fused copy", cell.Strategy, cell.Reason, cell.Replaced, "subprocess, rebuilt from "+source)
			}
			if got := s.listing(); got != listing {
				t.Fatalf("listing after the switch:\n%s\nbefore:\n%s", got, listing)
			}
			// The fused copy's registrations are the old Extension value: a call through it reaches no code and reports the stop.
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			err := old.Commands["version"].Handler(ctx, "")
			cancel()
			select {
			case v := <-s.notices:
				t.Fatalf("the stopped fused copy still answers: %q", v)
			default:
			}
			if err == nil || !strings.Contains(err.Error(), "stopped by the host") {
				t.Fatalf("a call through the stopped fused copy: %v, want the host's stopped error", err)
			}

			// It stays a subprocess on later reloads, and an edit is picked up again.
			hybridEdit(t, source, hybridName, "v2")
			s.reload(t)
			if v := s.version(t, hybridName); v != "v2" {
				t.Fatalf("after a second edit the member answers %q", v)
			}
			// The source the binary was built from is fused again.
			hybridEdit(t, source, hybridName, "v0")
			if report := s.reload(t); len(report.Cells) != 0 {
				t.Fatalf("reload of the source the binary was built from: %+v", report.Cells)
			}
			if v := s.version(t, hybridName); v != "fused" {
				t.Fatalf("with the source it was built from the member answers %q", v)
			}
		})
	}
}

// TestFusedMemberWithoutTheKeyReloadsItsCompiledCopy: a Binary built without `build.fusedReload: source` bakes no digest, and a
// reload after an edit runs the compiled copy again, as before the key existed.
func TestFusedMemberWithoutTheKeyReloadsItsCompiledCopy(t *testing.T) {
	skipWithoutGoBuilds(t)
	source := t.TempDir()
	cfg := hybridConfig(t, source, hybridName, "v0")
	installHybrid(t, hybridNoKey{hybridFused{baked: map[string]string{hybridName: ""}}})
	s := startHybridHost(t, "", cfg)
	hybridEdit(t, source, hybridName, "v1")
	report := s.reload(t)
	if v := s.version(t, hybridName); v != "fused" {
		t.Fatalf("without the key the member answers %q after an edit and a reload, want the compiled copy", v)
	}
	if len(s.errs) != 0 || len(report.Cells) != 0 || len(report.Issues) != 0 {
		t.Fatalf("load %v, reload %+v", s.errs, report)
	}
}

// TestFusedMemberSwitchLeavesNothingRunning: after the switch the process holds the goroutines a host holds that loaded the
// subprocess copy directly, so nothing of the fused copy is left running.
func TestFusedMemberSwitchLeavesNothingRunning(t *testing.T) {
	skipWithoutGoBuilds(t)
	source := t.TempDir()
	cfg := hybridConfig(t, source, hybridName, "v0")
	baked := hybridDigest(t, source)
	hybridEdit(t, source, hybridName, "v1")

	// The subprocess copy loaded directly: no member is fused.
	before := hybridSettledGoroutines()
	installHybrid(t, hybridFused{})
	direct := startHybridHost(t, "", cfg)
	if v := direct.version(t, hybridName); v != "v1" {
		t.Fatalf("the direct copy answers %q", v)
	}
	directGoroutines := hybridSettledGoroutines() - before
	direct.host.Shutdown("done")

	hybridEdit(t, source, hybridName, "v0")
	before = hybridSettledGoroutines()
	subprocess.SetFusedResolver(hybridFused{baked: map[string]string{hybridName: baked}})
	s := startHybridHost(t, "", cfg)
	if v := s.version(t, hybridName); v != "fused" {
		t.Fatalf("the member answers %q before the edit", v)
	}
	fusedGoroutines := hybridSettledGoroutines() - before
	hybridEdit(t, source, hybridName, "v1")
	s.reload(t)
	if v := s.version(t, hybridName); v != "v1" {
		t.Fatalf("after the switch the member answers %q", v)
	}
	switched := hybridSettledGoroutines() - before
	t.Logf("GOROUTINEROW over an idle process: fused copy +%d, subprocess copy loaded directly +%d, subprocess copy after the switch +%d", fusedGoroutines, directGoroutines, switched)
	if switched != directGoroutines {
		t.Fatalf("after the switch the process holds %d goroutines more than idle, a directly loaded subprocess copy %d", switched, directGoroutines)
	}
}

// TestFusedMemberSwitchKeepsReloadOrderAndReasons: Pi's /reload restarts every extension with session_shutdown and then
// session_start, both with reason reload, in configured order. The member keeps its place in that order when it becomes a
// subprocess, beside an extension that did not change.
func TestFusedMemberSwitchKeepsReloadOrderAndReasons(t *testing.T) {
	skipWithoutGoBuilds(t)
	log := filepath.Join(t.TempDir(), "events.log")
	t.Setenv("PIG_HYBRID_LOG", log)
	otherSource, memberSource := t.TempDir(), t.TempDir()
	// The unchanged subprocess extension is configured first, the fused member second.
	other := hybridConfig(t, otherSource, "hybrid-other", "other")
	member := hybridConfig(t, memberSource, hybridName, "v0")
	installHybrid(t, hybridFused{baked: map[string]string{hybridName: hybridDigest(t, memberSource)}})
	s := startHybridHost(t, "", other, member)
	events := func() []string {
		t.Helper()
		data, err := os.ReadFile(log)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(log); err != nil {
			t.Fatal(err)
		}
		return strings.Split(strings.TrimSpace(string(data)), "\n")
	}
	want := []string{
		"hybrid-other session_shutdown reload", "hybrid session_shutdown reload",
		"hybrid-other session_start reload", "hybrid session_start reload",
	}
	s.reload(t)
	if v := s.version(t, hybridName); v != "fused" {
		t.Fatalf("the unchanged member answers %q", v)
	}
	if got := events(); !slices.Equal(got, want) {
		t.Fatalf("reload of the fused member: events %q, want %q", got, want)
	}
	hybridEdit(t, memberSource, hybridName, "v1")
	s.reload(t)
	if v := s.version(t, hybridName); v != "v1" {
		t.Fatalf("after the switch the member answers %q", v)
	}
	if got := events(); !slices.Equal(got, want) {
		t.Fatalf("the reload that switches the member: events %q, want %q", got, want)
	}
	s.reload(t)
	if got := events(); !slices.Equal(got, want) {
		t.Fatalf("a reload after the switch: events %q, want %q", got, want)
	}
	if names := []string{s.loaded[0].Name, s.loaded[1].Name}; !slices.Equal(names, []string{"hybrid-other", hybridName}) {
		t.Fatalf("loaded order %q", names)
	}
}

// TestFusedMemberWithoutAToolchainKeepsItsCompiledCopyAndWarnsOnce: with the source changed and no Go toolchain, the compiled copy
// runs, and the start and the reload each report one warning with the builder's readiness message and its remedy.
func TestFusedMemberWithoutAToolchainKeepsItsCompiledCopyAndWarnsOnce(t *testing.T) {
	source := t.TempDir()
	cfg := hybridConfig(t, source, hybridName, "v0")
	installHybrid(t, hybridFused{baked: map[string]string{hybridName: hybridDigest(t, source)}})
	hybridEdit(t, source, hybridName, "v1")
	t.Setenv("PATH", "")
	const readiness = `cannot build go extension: the "go" toolchain is not installed; install it, or run this extension from a Piglet Binary or prebuilt package (which need no toolchain)`
	wantWarning := "source at " + source + " differs from the fused copy; no Go toolchain: running the fused copy: " + readiness

	s := startHybridHost(t, "", cfg)
	if len(s.errs) != 1 || s.errs[0].Error() != `extension "hybrid" (fused): `+wantWarning {
		t.Fatalf("start reports %q, want one warning %q", s.errs, wantWarning)
	}
	if got := s.host.LoadErrors(); len(got) != 1 || !strings.Contains(got[0], wantWarning) {
		t.Fatalf("LoadErrors = %q", got)
	}
	if v := s.version(t, hybridName); v != "fused" {
		t.Fatalf("without a toolchain the member answers %q at a start", v)
	}
	report := s.reload(t)
	if v := s.version(t, hybridName); v != "fused" {
		t.Fatalf("without a toolchain the member answers %q after a reload", v)
	}
	if len(report.Issues) != 1 || report.Issues[0] != source+": "+wantWarning {
		t.Fatalf("reload issues %q, want one: %q", report.Issues, source+": "+wantWarning)
	}
	if len(report.Cells) != 1 || report.Cells[0].Reason != "fused (source differs; toolchain missing)" || report.Cells[0].Strategy != "fused" {
		t.Fatalf("placement %+v, want fused (source differs; toolchain missing)", report.Cells)
	}

	// The source is gone: the compiled copy runs and one warning says the source cannot be read.
	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}
	report = s.reload(t)
	if v := s.version(t, hybridName); v != "fused" {
		t.Fatalf("without its source the member answers %q", v)
	}
	if len(report.Issues) != 1 || !strings.Contains(report.Issues[0], "cannot be read as the Go module of the fused copy: running the fused copy") || len(report.Cells) != 1 || report.Cells[0].Reason != "fused (source unreadable)" {
		t.Fatalf("reload without the source: %+v", report)
	}
}

// TestChangedFusedMemberStartsFromTheBuildCache: a start with a changed member builds the member once; the next start runs the
// cached artifact. The evidence is the build cache itself: the first start adds the member's artifact to it, and the second
// start leaves every file of it as it was (a build writes its artifact anew).
func TestChangedFusedMemberStartsFromTheBuildCache(t *testing.T) {
	skipWithoutGoBuilds(t)
	source, configRoot := t.TempDir(), t.TempDir()
	cache := func() []string {
		t.Helper()
		var files []string
		err := filepath.WalkDir(filepath.Join(configRoot, "cache"), func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			files = append(files, fmt.Sprintf("%s %d %d", path, info.Size(), info.ModTime().UnixNano()))
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		slices.Sort(files)
		return files
	}
	cfg := hybridConfig(t, source, hybridName, "v0")
	installHybrid(t, hybridFused{baked: map[string]string{hybridName: hybridDigest(t, source)}})

	unchanged := startHybridHost(t, configRoot, cfg)
	if v := unchanged.version(t, hybridName); v != "fused" {
		t.Fatalf("a start with the unchanged source answers %q, want the fused copy", v)
	}
	unchanged.host.Shutdown("done")
	before := cache()

	hybridEdit(t, source, hybridName, "v1")
	first := startHybridHost(t, configRoot, cfg)
	if v := first.version(t, hybridName); v != "v1" {
		t.Fatalf("a start with the changed source answers %q, want v1", v)
	}
	first.host.Shutdown("done")
	built := cache()
	if slices.Equal(built, before) {
		t.Fatalf("the first start with the changed source left no artifact in the build cache %s: %q", filepath.Join(configRoot, "cache"), built)
	}
	second := startHybridHost(t, configRoot, cfg)
	if v := second.version(t, hybridName); v != "v1" {
		t.Fatalf("the second start answers %q, want v1", v)
	}
	if after := cache(); !slices.Equal(after, built) {
		t.Fatalf("the second start changed the build cache, so it built again:\n%s\nafter the first start:\n%s", strings.Join(after, "\n"), strings.Join(built, "\n"))
	}
}

// decorationUI records the statuses and widgets the host sets and clears.
type decorationUI struct {
	*recordingUI
	mu    sync.Mutex
	calls []string
}

func (u *decorationUI) add(call string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.calls = append(u.calls, call)
}

func (u *decorationUI) take() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	calls := u.calls
	u.calls = nil
	return calls
}

func (u *decorationUI) SetStatus(key, text string) { u.add("status " + key + "=" + text) }

func (u *decorationUI) SetWidget(key string, lines []string, _ *extension.ExtensionWidgetOptions) {
	u.add(fmt.Sprintf("widget %s=%q", key, lines))
}

func (u *decorationUI) SetWidgetFactory(key string, factory extension.WidgetFactory, _ *extension.ExtensionWidgetOptions) {
	u.add(fmt.Sprintf("widget-factory %s set=%t", key, factory != nil))
}

// TestFusedMemberDecorationsAcrossReloadMatchTheSubprocessCopy: what the host tells the UI about a status and a widget an extension
// set, over a reload, is the same for a fused member and for the same extension as a subprocess. The row is logged: it says whether
// the host clears them at a reload.
func TestFusedMemberDecorationsAcrossReloadMatchTheSubprocessCopy(t *testing.T) {
	skipWithoutGoBuilds(t)
	run := func(t *testing.T, resolver hybridFused) (set, reloaded []string) {
		t.Helper()
		source := t.TempDir()
		cfg := hybridConfig(t, source, hybridName, "v0")
		if resolver.baked != nil {
			resolver.baked[hybridName] = hybridDigest(t, source)
		}
		installHybrid(t, resolver)
		ui := &decorationUI{recordingUI: newRecordingUI(new([]string), new([]string))}
		s := startHybridHostWithUI(t, "", ui, cfg)
		ui.take()
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		if err := s.member(t, hybridName).Commands["decorate"].Handler(ctx, ""); err != nil {
			t.Fatal(err)
		}
		// A status is a push without an answer: the version round trip that follows it on the same connection orders it.
		wantVersion := "v0"
		if resolver.baked != nil {
			wantVersion = "fused"
		}
		if v := s.version(t, hybridName); v != wantVersion {
			t.Fatalf("the member answers %q, want %s", v, wantVersion)
		}
		set = ui.take()
		s.reload(t)
		if v := s.version(t, hybridName); v != wantVersion {
			t.Fatalf("after the reload the member answers %q, want %s", v, wantVersion)
		}
		return set, ui.take()
	}
	fusedSet, fusedReloaded := run(t, hybridFused{baked: map[string]string{}})
	directSet, directReloaded := run(t, hybridFused{})
	t.Logf("DECORATIONROW fused: set %q, at the reload %q", fusedSet, fusedReloaded)
	t.Logf("DECORATIONROW subprocess: set %q, at the reload %q", directSet, directReloaded)
	if len(fusedSet) == 0 {
		t.Fatal("the UI saw no status and no widget from the fused member")
	}
	if !slices.Equal(fusedSet, directSet) || !slices.Equal(fusedReloaded, directReloaded) {
		t.Fatalf("a fused member and its subprocess copy differ in what the UI is told:\nfused: set %q, at the reload %q\nsubprocess: set %q, at the reload %q", fusedSet, fusedReloaded, directSet, directReloaded)
	}
}
