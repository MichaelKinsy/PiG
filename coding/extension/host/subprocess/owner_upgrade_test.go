package subprocess

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/mod/modfile"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/gosdkbundle"
	"github.com/MichaelKinsy/PiG/internal/sdkpack"
	"github.com/MichaelKinsy/PiG/internal/zstdblob"
)

// A standalone may import the staged SDK through its legacy module path, including SDK self-imports.
func TestLegacyStandaloneBuildsStagedSDKSubpackages(t *testing.T) {
	root := t.TempDir()
	sdkDir := stagedSDKDir(t, root, "go")
	frame, err := gosdkbundle.Source.ReadFile(sdkpack.ArchiveName)
	if err != nil {
		t.Fatal(err)
	}
	blob, err := zstdblob.Decode(frame)
	if err != nil {
		t.Fatal(err)
	}
	if err := sdkpack.Walk(blob, func(name string, data []byte) error {
		path := filepath.Join(sdkDir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, data, 0o644)
	}); err != nil {
		t.Fatal(err)
	}
	for _, replacement := range []string{"./missing-sdk", sdkDir} {
		t.Run(filepath.Base(replacement), func(t *testing.T) {
			src := t.TempDir()
			// A go.mod path containing a space (a Windows profile, or CI's
			// "pig extension acceptance" TEMP) must be quoted, as a user's would be.
			writeOwnerFixture(t, src, "go.mod", fmt.Sprintf("module example.com/standalone\n\ngo 1.26\n\nrequire github.com/mainstai/pig/extensions/sdk v0.0.0\nreplace github.com/mainstai/pig/extensions/sdk => %s\n", modfile.AutoQuote(filepath.ToSlash(replacement))))
			writeOwnerFixture(t, src, "main.go", `package main
import sdk "github.com/mainstai/pig/extensions/sdk"
func main() { e := sdk.New("standalone"); e.Command("probe", "alive", func(sdk.Context, string) error { return nil }); if err := e.Run(); err != nil { panic(err) } }
`)
			before, err := os.ReadFile(filepath.Join(src, "go.mod"))
			if err != nil {
				t.Fatal(err)
			}
			h := NewHostWithConfigRoot(t.TempDir(), root)
			t.Cleanup(func() { h.Shutdown("test done") })
			loaded, err := h.Load(t.Context(), ExtConfig{Name: "standalone", Source: src, Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			if err := loaded.Commands["probe"].Handler(t.Context(), ""); err != nil {
				t.Fatal(err)
			}
			after, err := os.ReadFile(filepath.Join(src, "go.mod"))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("authored go.mod changed")
			}
			transient, err := filepath.Glob(filepath.Join(src, ".pig-build-*"))
			if err != nil {
				t.Fatal(err)
			}
			if len(transient) != 0 {
				t.Fatalf("build temporaries retained: %v", transient)
			}
		})
	}
}

// Pi loader.ts:599-636 loads both paths. Host keys must not become required SDK registration names.
func TestGoDuplicateFolderIdentitiesLoadAndReload(t *testing.T) {
	for _, sameModule := range []bool{false, true} {
		t.Run(fmt.Sprintf("same-module-%t", sameModule), func(t *testing.T) {
			var configs []ExtConfig
			for i := range 2 {
				module := fmt.Sprintf("example.com/ask%d", i)
				if sameModule {
					module = "example.com/ask"
				}
				src := t.TempDir()
				writeOwnerFixture(t, src, "go.mod", "module "+module+"\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n")
				writeOwnerFixture(t, src, "ext.go", fmt.Sprintf(`package ask
import sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
func Extension() *sdk.Extension { e:=sdk.New("ask"); e.Command("ask",%q,func(ctx sdk.Context,_ string)error{return ctx.SetSessionName(%q)}); return e }
`, fmt.Sprintf("copy-%d", i), fmt.Sprintf("copy-%d", i)))
				configs = append(configs, packedFactoryConfig("ask", src, module, fmt.Sprintf("copy-%d", i)).SelectedAs(src))
			}
			h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
			bridge := NewUIBridge(func() {})
			dispatched := make(chan string, 1)
			bridge.SetHostAction("setSessionName", func(name string) error { dispatched <- name; return nil })
			h.SetUIBridge(bridge)
			t.Cleanup(func() { h.Shutdown("test done") })
			loaded, errs := h.LoadAll(t.Context(), configs)
			if len(errs) != 0 {
				t.Fatalf("load: %v", errs)
			}
			assertCopies := func(loaded []extension.Extension) {
				t.Helper()
				if len(loaded) != len(configs) {
					t.Fatalf("loaded %d, want %d", len(loaded), len(configs))
				}
				for i, ext := range loaded {
					if ext.Path != configs[i].Source {
						t.Fatalf("path = %q, want %q", ext.Path, configs[i].Source)
					}
					if err := ext.Commands["ask"].Handler(t.Context(), ""); err != nil {
						t.Fatal(err)
					}
					if got := <-dispatched; got != fmt.Sprintf("copy-%d", i) {
						t.Fatalf("copy %d dispatched %q", i, got)
					}
				}
			}
			assertCopies(loaded)
			h.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
			loaded, err := h.Reload(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if report := h.LastReloadReport(); len(report.Issues) != 0 {
				t.Fatalf("reload: %v", report.Issues)
			}
			assertCopies(loaded)
		})
	}
}

// duplicateGoFolderConfigs writes count Go factory extensions that all register as "ext", two to a module path, built against the repository's SDK.
func duplicateGoFolderConfigs(tb testing.TB, count int) []ExtConfig {
	tb.Helper()
	tb.Setenv("PIG_SDK_GO_ROOT", filepath.Join(findModuleRoot(tb), "extensions", "sdk"))
	var configs []ExtConfig
	for i := range count {
		root := tb.TempDir()
		module := fmt.Sprintf("example.com/ext%d", i/2)
		writeOwnerFixture(tb, root, "go.mod", "module "+module+"\n\ngo 1.26\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n")
		writeOwnerFixture(tb, root, "ext.go", `package ext
import sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
func Extension() *sdk.Extension {e:=sdk.New("ext");e.Command("probe","",func(sdk.Context,string)error{return nil});return e}
`)
		configs = append(configs, packedFactoryConfig("ext", root, module, fmt.Sprint(i)))
	}
	return configs
}

// The cells of a load look for their built images under keys made from one reading of the SDK tree, which they share
// (runtimecell.WithLoadTreeHashes); a build publishes its image under a key made from a reading of its own, under its build lock. The two are the
// same key: after a load has built thirteen Go extensions, the reload finds the image of every cell in the cache and builds none.
func TestReloadFindsEveryCellUnderTheKeyItsBuildPublished(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles packed Go cells")
	}
	configRoot := t.TempDir()
	configs := duplicateGoFolderConfigs(t, 13)
	h := NewHostWithConfigRoot(configRoot, configRoot)
	t.Cleanup(func() { h.Shutdown("test done") })
	if _, errs := h.LoadAll(t.Context(), configs); len(errs) != 0 {
		t.Fatal(errs)
	}
	h.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
	if _, err := h.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	report := h.LastReloadReport()
	if len(report.Issues) != 0 {
		t.Fatalf("reload: %v", report.Issues)
	}
	built := 0
	for _, cell := range report.Cells {
		if cell.Hash == "" {
			continue
		}
		built++
		if !cell.Cached || cell.BuildDuration != 0 {
			t.Errorf("cell %s %v: cached %v, built for %s; want the image the load built, found under %s", cell.Key, cell.Extensions, cell.Cached, cell.BuildDuration, cell.Hash)
		}
	}
	if built < 2 {
		t.Fatalf("the reload reports %d cells with a built image, want the cells of thirteen extensions: %+v", built, report.Cells)
	}
}

func BenchmarkDuplicateGoFolderStartup(b *testing.B) {
	configRoot := b.TempDir()
	configs := duplicateGoFolderConfigs(b, 13)
	load := func() {
		h := NewHostWithConfigRoot(configRoot, configRoot)
		loaded, errs := h.LoadAll(b.Context(), configs)
		if len(errs) != 0 {
			h.Shutdown("benchmark failed")
			b.Fatal(errs)
		}
		if len(loaded) != len(configs) {
			h.Shutdown("benchmark failed")
			b.Fatal("missing extension")
		}
		for _, ext := range loaded {
			if err := ext.Commands["probe"].Handler(b.Context(), ""); err != nil {
				h.Shutdown("benchmark failed")
				b.Fatal(err)
			}
		}
		h.Shutdown("benchmark done")
	}
	load()
	b.ReportAllocs()
	for b.Loop() {
		load()
	}
}

func writeOwnerFixture(t testing.TB, root, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
