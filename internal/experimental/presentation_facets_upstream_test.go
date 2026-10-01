package experimental

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	goruntime "runtime"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/MichaelKinsy/PiG/agent/harness/pico3"
	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/services"
)

func TestServerSelectedPresentationFacetsUpstream(t *testing.T) {
	isolateExperimentalTest(t)
	// startServer spawns the real internal coordinator entry; the test binary dispatches that role.
	t.Setenv(experimentalTestEntryEnv, "1")

	// upstream: packages/coding-agent/test/experimental-presentation-facets.test.ts:32. pig divergence (D64): experimental Radius is designed out, so a Radius route with local plugin paths is still rejected before discovery, by the Unix-only transport guard instead of the Radius plugin-path guard.
	t.Run("rejects local plugin paths for Radius servers", func(t *testing.T) {
		directory := filepath.Join(t.TempDir(), "not-created")
		runtime, err := OpenClientRuntime(t.Context(), ClientCommand{
			Command:        "client",
			Connect:        &TransportAddress{Transport: "radius"},
			PluginPackages: []string{"./local-plugin"},
		}, OpenClientRuntimeOptions{Directory: &directory})
		if runtime != nil {
			t.Cleanup(func() {
				if err := runtime.Dispose(); err != nil {
					t.Error(err)
				}
			})
		}
		if err == nil || err.Error() != "Experimental clients support only Unix transport" {
			t.Fatalf("open Radius runtime error = %v, want D64 Unix-only rejection", err)
		}
		if _, statErr := os.Stat(directory); !os.IsNotExist(statErr) {
			t.Fatalf("rejected Radius route touched server discovery: %v", statErr)
		}
	})

	// upstream: packages/coding-agent/test/experimental-presentation-facets.test.ts:53.
	t.Run("builds conventional plugin entries into the server-owned plugin cache", func(t *testing.T) {
		directory := socketDir(t)
		serverId := uuid.NewString()
		packagePath := filepath.Join(directory, "pi-example-plugin")
		if err := os.MkdirAll(filepath.Join(packagePath, "src"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(packagePath, "package.json"), []byte("{\"name\":\"@earendil-works/test-plugin\",\"version\":\"1.0.0\",\"peerDependencies\":{\"@earendil-works/chord\":\"^0.84.4\",\"@earendil-works/pi-coding-agent\":\"^0.84.4\"}}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		sourcePath := filepath.Join(packagePath, "src", "tui.ts")
		if err := os.WriteFile(sourcePath, []byte("import { defineFacet } from \"@earendil-works/chord\"; import { SlashCommands } from \"@earendil-works/pi-coding-agent/experimental/plugin\"; export default defineFacet({ id: \"built-a\", setup(env) { env.use(SlashCommands); } });\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		plugin, err := CreateServerPluginPackage(directory, serverId, packagePath)
		if err != nil {
			t.Fatal(err)
		}
		first, err := plugin.Build(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if len(first) != 1 {
			t.Fatalf("first build artifacts = %d, want one tui entry", len(first))
		}
		manifestPattern := regexp.MustCompile(`/plugin-builds/` + regexp.QuoteMeta(serverId) + `/pi-example-plugin-[a-f0-9]{12}/chord-facets\.json$`)
		if !manifestPattern.MatchString(filepath.ToSlash(plugin.ManifestPath)) {
			t.Fatalf("manifest path = %q, want %s", plugin.ManifestPath, manifestPattern)
		}
		pluginJSON, err := json.Marshal(first[0].Plugin)
		if err != nil {
			t.Fatal(err)
		}
		var identity map[string]any
		if err := json.Unmarshal(pluginJSON, &identity); err != nil {
			t.Fatal(err)
		}
		if want := (map[string]any{"id": "@earendil-works/test-plugin", "version": "1.0.0"}); !reflect.DeepEqual(identity, want) {
			t.Fatalf("first plugin = %#v, want %#v", identity, want)
		}
		assertPresentationFacetIds(t, CreatePresentationFacetData(first), true, []string{"built-a"})

		if err := os.WriteFile(sourcePath, []byte("import { defineFacet } from \"@earendil-works/chord\"; import { SlashCommands } from \"@earendil-works/pi-coding-agent/experimental/plugin\"; export default defineFacet({ id: \"built-b\", setup(env) { env.use(SlashCommands); } });\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		second, err := plugin.Build(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if len(second) == 0 {
			t.Fatal("second build omitted the tui artifact")
		}
		if second[0].Source == first[0].Source {
			t.Fatal("second build retained the first source")
		}
		assertPresentationFacetIds(t, CreatePresentationFacetData(second), true, []string{"built-b"})

		secondPackagePath := filepath.Join(directory, "second-plugin")
		if err := os.MkdirAll(filepath.Join(secondPackagePath, "src"), 0o700); err != nil {
			t.Fatal(err)
		}
		writes := []struct{ path, text string }{
			{filepath.Join(secondPackagePath, "package.json"), "{\"name\":\"@earendil-works/second-test-plugin\",\"version\":\"1.0.0\",\"peerDependencies\":{\"@earendil-works/chord\":\"^0.84.4\"}}\n"},
			{filepath.Join(secondPackagePath, "src", "tui.ts"), "import { defineFacet } from \"@earendil-works/chord\"; export default defineFacet({ id: \"second-built\", setup() {} });\n"},
		}
		writeErrors := make([]error, len(writes))
		var writing sync.WaitGroup
		for i, write := range writes {
			writing.Go(func() { writeErrors[i] = os.WriteFile(write.path, []byte(write.text), 0o600) })
		}
		writing.Wait()
		for _, err := range writeErrors {
			if err != nil {
				t.Fatal(err)
			}
		}
		running, err := StartServer(t.Context(), StartServerOptions{Directory: new(filepath.Join(directory, "server")), SessionDir: new(filepath.Join(directory, "sessions"))})
		if goruntime.GOOS == "windows" {
			// Pi's ensurePrivateServerDirectory throws before a server starts (packages/coding-agent/src/experimental/server.ts:59).
			if err == nil || err.Error() != "Unix socket directory requires a POSIX user ID" || running != nil {
				t.Fatalf("startup = (%v, %v), want unsupported POSIX directory error", running, err)
			}
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := running.Close(); err != nil {
				t.Error(err)
			}
		})
		runtime, err := OpenClientRuntime(t.Context(), ClientCommand{
			Command:        "client",
			Connect:        &TransportAddress{Transport: "unix", Path: running.SocketPath},
			PluginPackages: []string{packagePath, secondPackagePath},
		}, OpenClientRuntimeOptions{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := runtime.Dispose(); err != nil {
				t.Error(err)
			}
		})
		activated, err := ActivateBuiltinClientServices(t.Context(), runtime.Servers[0])
		if err != nil {
			t.Fatal(err)
		}
		sessionId := uuid.NewString()
		if _, err := activated.Management.Create(t.Context(), services.SessionCreateOptions{Id: &sessionId}); err != nil {
			t.Fatal(err)
		}
		data, err := activated.Plugins.PrepareSession(t.Context(), services.PrepareSessionPluginsRequest{SessionId: sessionId, PackagePaths: []string{packagePath, secondPackagePath}})
		if err != nil {
			t.Fatal(err)
		}
		profile, err := RestoreServerPluginPackageProfile(filepath.Join(directory, "server"), running.ServerId, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(profile, []string{}) {
			t.Fatalf("server profile = %#v, want []", profile)
		}
		assertPresentationFacetIds(t, data, false, []string{"built-b", "second-built"})

		if err := os.WriteFile(sourcePath, []byte("import { defineFacet } from \"@earendil-works/chord\"; import { SlashCommands } from \"@earendil-works/pi-coding-agent/experimental/plugin\"; export default defineFacet({ id: \"built-c\", setup(env) { env.use(SlashCommands); } });\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		opened, err := runtime.Servers[0].Server.Open(chord.RemoteServiceSourceOpenOptions{Services: []string{services.PresentationPluginsDefinition.Id()}, AssertAccess: func() error { return nil }, OnError: func(err error) { t.Error(err) }})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := opened.Dispose(context.Background()); err != nil {
				t.Error(err)
			}
		})
		if err := opened.Ready(t.Context()); err != nil {
			t.Fatal(err)
		}
		plugins, err := chord.UseRemoteClient(opened, services.PresentationPluginsDefinition)
		if err != nil {
			t.Fatal(err)
		}
		data, err = plugins.Reload(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		assertPresentationFacetIds(t, data, false, []string{"built-c", "second-built"})
	})

	// upstream: packages/coding-agent/test/experimental-presentation-facets.test.ts:158.
	t.Run("builds the example plugin package without a package-owned build script", func(t *testing.T) {
		directory := t.TempDir()
		packagePath, err := filepath.Abs(filepath.Join("..", "..", ".upstream", "current", "packages", "coding-agent", "examples", "plugins", "pi-example-plugin"))
		if err != nil {
			t.Fatal(err)
		}
		plugin, err := CreateServerPluginPackage(directory, uuid.NewString(), packagePath)
		if err != nil {
			t.Fatal(err)
		}
		artifacts, err := plugin.Build(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		manifest, err := ReadFacetBundleManifest(plugin.ManifestPath)
		if err != nil {
			t.Fatal(err)
		}
		if want := (FacetBundlePlugin{Id: "@earendil-works/pi-example-plugin", Version: new("1.0.0")}); !reflect.DeepEqual(manifest.Plugin, want) {
			t.Fatalf("manifest plugin = %#v, want %#v", manifest.Plugin, want)
		}
		manifestJSON, err := os.ReadFile(plugin.ManifestPath)
		if err != nil {
			t.Fatal(err)
		}
		var orderedManifest struct {
			Entries json.RawMessage `json:"entries"`
		}
		if err := json.Unmarshal(manifestJSON, &orderedManifest); err != nil {
			t.Fatal(err)
		}
		// Object.keys(manifest.entries) preserves the builder's insertion order.
		entries := json.NewDecoder(bytes.NewReader(orderedManifest.Entries))
		if token, err := entries.Token(); err != nil || token != json.Delim('{') {
			t.Fatalf("manifest entries opening = %v, %v, want object", token, err)
		}
		var names []string
		for entries.More() {
			key, err := entries.Token()
			if err != nil {
				t.Fatal(err)
			}
			name, ok := key.(string)
			if !ok {
				t.Fatalf("entry name = %T, want string", key)
			}
			names = append(names, name)
			var entry json.RawMessage
			if err := entries.Decode(&entry); err != nil {
				t.Fatal(err)
			}
		}
		if want := []string{"session", "tui"}; !reflect.DeepEqual(names, want) {
			t.Fatalf("manifest entry names = %q, want %q", names, want)
		}
		assertPresentationFacetIds(t, CreatePresentationFacetData(artifacts), true, []string{"@earendil-works/pi-example-plugin/tui"})
	})
}

// upstream: packages/coding-agent/test/experimental-presentation-facets.test.ts:124-129,144-147 loads and disposes each generation concurrently while retaining loader order.
func assertPresentationFacetIds(t *testing.T, data pico3.JsonValue, firstOnly bool, want []string) {
	t.Helper()
	loaders, err := CreatePresentationFacetLoaders(data)
	if err != nil {
		t.Fatal(err)
	}
	if firstOnly {
		if len(loaders) == 0 {
			t.Fatal("presentation has no first loader")
		}
		loaders = loaders[:1]
	}
	loaded := make([]chord.LoadedFacets, len(loaders))
	failures := make([]error, len(loaders))
	var tasks sync.WaitGroup
	for i, loader := range loaders {
		tasks.Go(func() { loaded[i], failures[i] = loader.Load(t.Context()) })
	}
	tasks.Wait()
	defer func() {
		for i, set := range loaded {
			if set.Dispose != nil {
				tasks.Go(func() { failures[i] = set.Dispose(t.Context()) })
			}
		}
		tasks.Wait()
		for _, err := range failures {
			if err != nil {
				t.Error(err)
			}
		}
	}()
	for _, err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	var got []string
	for _, set := range loaded {
		for _, facet := range set.Facets {
			got = append(got, facet.Id)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("loaded facet ids = %q, want %q", got, want)
	}
}
