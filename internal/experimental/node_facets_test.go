package experimental

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/services"
)

// upstream: packages/chord/src/context/index.ts:94-117 and packages/chord/test/context.test.ts:73-88. Cancelling an observer rejects only that waiter, not the retained producer Promise.
func TestNodeFacetPromiseWaitCancellationRetiresOnlyObserver(t *testing.T) {
	isolateExperimentalTest(t)
	root := t.TempDir()
	driverPath, err := filepath.Abs("node-facets/driver.mjs")
	if err != nil {
		t.Fatal(err)
	}
	driverJSON, err := json.Marshal(driverPath)
	if err != nil {
		t.Fatal(err)
	}
	module := filepath.Join(root, "observe-driver.mjs")
	// Observe the real driver's task exit, not Conn's earlier cancellation response. The immediate is an event-loop checkpoint after the ordered cancel frame, not a wall-clock delay.
	writeNodeFacetFile(t, module, `import { createRequire } from "node:module";
const { createFacetBridge: createDriver } = createRequire(import.meta.url)(`+string(driverJSON)+`);
export function createFacetBridge(host) {
  const driver = createDriver(host);
  const exits = [];
  return {
    sync: args => driver.sync(args),
    async request(args, signal) {
      if (args.op === "exits") return new Promise(resolve => setImmediate(() => resolve(exits.slice())));
      if (args.op !== "await") return driver.request(args, signal);
      host.callSync({ observer: args.observer });
      try { return await driver.request(args, signal); }
      finally { exits.push(args.observer); }
    }
  };
}
`)
	entered := map[string]chan struct{}{"cancelled": make(chan struct{}), "survivor": make(chan struct{})}
	bridge, err := subprocess.OpenFacetBridge(t.Context(), subprocess.FacetBridgeOptions{Directory: root, TemporaryDirectory: root, Module: module, Call: func(_ context.Context, _ string, raw json.RawMessage) (json.RawMessage, error) {
		var event struct{ Observer string }
		if err := json.Unmarshal(raw, &event); err != nil {
			return nil, err
		}
		if ready := entered[event.Observer]; ready != nil {
			close(ready)
		}
		return json.RawMessage(`null`), nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := bridge.Close(); err != nil {
			t.Error(err)
		}
	})
	call := func(ctx context.Context, synchronous bool, args any, target any) error {
		payload, err := json.Marshal(args)
		if err != nil {
			return err
		}
		var raw json.RawMessage
		if synchronous {
			raw, err = bridge.CallSync(ctx, payload)
		} else {
			raw, err = bridge.Call(ctx, payload)
		}
		if err != nil || target == nil {
			return err
		}
		return json.Unmarshal(raw, target)
	}
	source := `let settle; const pending = new Promise(resolve => { settle = resolve; }); module.exports.default = {id:"waiter",setup(){},pending,settle};`
	digest := sha256.Sum256([]byte(source))
	artifact := FacetBundleArtifact{Format: FacetBundleArtifactFormat, FormatVersion: FacetBundleArtifactFormatVersion, Plugin: FacetBundlePlugin{Id: "waiter"}, EntryName: "session", Entry: FacetBundleEntry{File: "entry.cjs", Integrity: "sha256-" + base64.StdEncoding.EncodeToString(digest[:]), ExternalImports: []string{}}, Source: source}
	var facets []nodeFacetReference
	if err := call(t.Context(), false, map[string]any{"op": "load", "artifact": artifact}, &facets); err != nil {
		t.Fatal(err)
	}
	if len(facets) != 1 || facets[0].Id != "waiter" {
		t.Fatalf("loaded facets=%#v, want the fixture's waiter facet", facets)
	}
	var pending, settle nodeFacetValue
	for name, target := range map[string]*nodeFacetValue{"pending": &pending, "settle": &settle} {
		if err := call(t.Context(), true, map[string]any{"op": "get", "id": facets[0].Reference, "property": name}, target); err != nil {
			t.Fatal(err)
		}
	}
	cancelledCtx, cancel := context.WithCancel(t.Context())
	survivorCtx, stopSurvivor := context.WithCancel(t.Context())
	var workers sync.WaitGroup
	t.Cleanup(func() {
		cancel()
		stopSurvivor()
		workers.Wait()
		if err := call(context.Background(), true, map[string]any{"op": "invoke", "id": settle.Id, "args": []nodeFacetValue{}}, nil); err != nil {
			t.Error(err)
		}
	})
	type outcome struct {
		value nodeFacetValue
		err   error
	}
	results := map[string]chan outcome{}
	for name, ctx := range map[string]context.Context{"cancelled": cancelledCtx, "survivor": survivorCtx} {
		result := make(chan outcome, 1)
		results[name] = result
		workers.Go(func() {
			var value nodeFacetValue
			err := call(ctx, false, map[string]any{"op": "await", "id": pending.Id, "observer": name}, &value)
			result <- outcome{value, err}
		})
		select {
		case <-entered[name]:
		case got := <-result:
			t.Fatalf("%s waiter returned before its entry barrier: %#v, %v", name, got.value, got.err)
		}
	}
	cancel()
	if got := <-results["cancelled"]; !errors.Is(got.err, context.Canceled) {
		t.Fatalf("cancelled observer=%#v, %v; want context cancellation", got.value, got.err)
	}
	// The cancel frame is queued ahead of this request, but Node reads socket frames in separate event-loop turns on Windows, so a single checkpoint can precede the abort. Poll until the exit is recorded; the producer stays unsettled throughout.
	var exits []string
	for deadline := time.Now().Add(10 * time.Second); ; {
		exits = nil
		if err := call(t.Context(), false, map[string]any{"op": "exits"}, &exits); err != nil {
			t.Fatal(err)
		}
		if len(exits) > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !reflect.DeepEqual(exits, []string{"cancelled"}) {
		t.Fatalf("Node waiter exits before producer settlement=%q, want [cancelled]", exits)
	}
	select {
	case got := <-results["survivor"]:
		t.Fatalf("another observer ended before producer settlement: %#v, %v", got.value, got.err)
	default:
	}
	want := json.RawMessage(`"completed later"`)
	if err := call(t.Context(), true, map[string]any{"op": "invoke", "id": settle.Id, "args": []nodeFacetValue{{Kind: "json", Value: want}}}, nil); err != nil {
		t.Fatal(err)
	}
	got := <-results["survivor"]
	if got.err != nil || got.value.Kind != "json" || string(got.value.Value) != string(want) {
		t.Fatalf("surviving observer=%#v, %v; want %s", got.value, got.err, want)
	}
	var repeated nodeFacetValue
	if err := call(t.Context(), false, map[string]any{"op": "await", "id": pending.Id, "observer": "repeated"}, &repeated); err != nil || repeated.Kind != "json" || string(repeated.Value) != string(want) {
		t.Fatalf("repeated observer=%#v, %v; want %s", repeated, err, want)
	}
}

func TestNodeFacetsRegisterReplaceRunAndDispose(t *testing.T) {
	isolateExperimentalTest(t)
	root := t.TempDir()
	entry := filepath.Join(root, "presentation.ts")
	build := func(description string) chord.LoadedFacets {
		t.Helper()
		text, err := json.Marshal(description)
		if err != nil {
			t.Fatal(err)
		}
		writeNodeFacetFile(t, entry, `import { SlashCommands, PresentationUI } from "@earendil-works/pi-coding-agent/experimental/plugin";
export default { id: "test/presentation", setup(env) {
  const commands = env.use(SlashCommands);
  const ui = env.use(PresentationUI);
  env.onActivate(() => env.own(commands.replace({
    name: "node-command", description: `+string(text)+`, argumentHint: "<value>",
    getArgumentCompletions(prefix) { return [{value:prefix+"!",label:"completion"}]; },
    async run(args, context) {
      const selected = await ui.select("choose", [{value:"alpha",label:"Alpha"},{value:"beta",label:"Beta",description:"second"}], undefined, context);
      ui.showStatus(args+":"+selected, context);
    }
  })));
}};
`)
		result, err := BundleFacets(t.Context(), BundleFacetsOptions{Plugin: FacetBundlePlugin{Id: "test-presentation"}, Entries: []FacetEntrySource{{Name: "tui", Source: entry}}, Outdir: filepath.Join(root, "bundle"), External: []string{"@earendil-works/pi-coding-agent/experimental/plugin"}})
		if err != nil {
			t.Fatal(err)
		}
		artifact, err := ReadFacetBundleArtifact(FacetBundleArtifactReadOptions{ManifestPath: result.ManifestPath, Entry: "tui"})
		if err != nil {
			t.Fatal(err)
		}
		loaders, err := CreatePresentationFacetLoaders(CreatePresentationFacetData([]FacetBundleArtifact{artifact}))
		if err != nil {
			t.Fatal(err)
		}
		loaded, err := chord.CombineFacetLoaders(loaders...).Load(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { disposeBundleFacets(t, loaded) })
		return loaded
	}
	registry := services.NewSlashCommandRegistry()
	ui := &nodeFacetTestUI{}
	loaded := build("first")
	host, err := chord.CreateFacetHost(t.Context(), chord.FacetOptions{Facets: append([]chord.Facet{services.CreateSlashCommandsRuntimeFacet(registry), {Id: "ui", Setup: func(env *chord.FacetEnvironment) error {
		return chord.ProvideService[services.PresentationUI](env, services.PresentationUIDefinition, ui)
	}}}, loaded.Facets...)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := host.Dispose(context.Background()); err != nil {
			t.Error(err)
		}
	})
	first := registry.List()
	if len(first) != 1 || first[0].Name != "node-command" || first[0].Description == nil || *first[0].Description != "first" || first[0].ArgumentHint == nil || *first[0].ArgumentHint != "<value>" {
		t.Fatalf("commands=%#v", first)
	}
	completions, err := first[0].GetArgumentCompletions("x")
	if err != nil || !reflect.DeepEqual(completions, []services.SlashCommandCompletion{{Value: "x!", Label: "completion"}}) {
		t.Fatalf("completions=%#v, %v", completions, err)
	}
	result, err := first[0].Run(t.Context(), "hello")
	if err != nil || result != nil {
		t.Fatalf("run=%#v, %v", result, err)
	}
	if !reflect.DeepEqual(ui.statuses, []string{"hello:beta"}) {
		t.Fatalf("status=%q", ui.statuses)
	}
	replacement := build("second")
	if err := host.Reload(t.Context(), replacement.Facets); err != nil {
		t.Fatal(err)
	}
	disposeBundleFacets(t, loaded)
	second := registry.List()
	if len(second) != 1 || second[0].Description == nil || *second[0].Description != "second" || second[0].RegistrationIdentity() == first[0].RegistrationIdentity() {
		t.Fatalf("replacement=%#v", second)
	}
	if _, err := second[0].Run(t.Context(), "again"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ui.statuses, []string{"hello:beta", "again:beta"}) {
		t.Fatalf("replacement status=%q", ui.statuses)
	}
	if err := host.Dispose(t.Context()); err != nil {
		t.Fatal(err)
	}
	if commands := registry.List(); len(commands) != 0 {
		t.Fatalf("commands survived disposal: %#v", commands)
	}
	disposeBundleFacets(t, replacement)
}

type nodeFacetTestUI struct{ statuses []string }

func (ui *nodeFacetTestUI) Select(ctx context.Context, title string, items []services.PresentationSelectItem, selected *string) (*string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	want := []services.PresentationSelectItem{{Value: "alpha", Label: "Alpha"}, {Value: "beta", Label: "Beta", Description: new("second")}}
	if title != "choose" || selected != nil || !reflect.DeepEqual(items, want) {
		return nil, errors.New("selector arguments changed at the bridge")
	}
	return new("beta"), nil
}
func (ui *nodeFacetTestUI) ShowStatus(ctx context.Context, message string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	ui.statuses = append(ui.statuses, message)
	return nil
}

func TestNodeFacetsRemoteMethodsAndState(t *testing.T) {
	isolateExperimentalTest(t)
	root := t.TempDir()
	entry := filepath.Join(root, "session.ts")
	writeNodeFacetFile(t, entry, `import { defineService } from "@earendil-works/chord";
const Counter = defineService("test.node-counter");
export default {id:"counter",setup(env){
  const state=env.replicatedState({value:1});
  env.provide(Counter,{state,async add(amount,context){state.change(context,draft=>{draft.value+=amount;});return state.value.value;}});
}};
`)
	result, err := BundleFacets(t.Context(), BundleFacetsOptions{Plugin: FacetBundlePlugin{Id: "counter"}, Entries: []FacetEntrySource{{Name: "session", Source: entry}}, Outdir: filepath.Join(root, "bundle")})
	if err != nil {
		t.Fatal(err)
	}
	loader, err := CreateSessionPluginFacetLoader([]string{result.ManifestPath})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := loader.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { disposeBundleFacets(t, loaded) })
	host, err := chord.CreateFacetHost(t.Context(), chord.FacetOptions{Facets: loaded.Facets})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := host.Dispose(context.Background()); err != nil {
			t.Error(err)
		}
	})
	binding, err := chord.CreateRemoteServiceBinding(chord.RemoteServiceBindingOptions{Services: []string{"test.node-counter"}, Transport: chord.NewLoopbackTransport(host.Services())})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := binding.Dispose(context.Background()); err != nil {
			t.Error(err)
		}
	})
	counter, err := binding.Use("test.node-counter")
	if err != nil {
		t.Fatal(err)
	}
	if err := binding.Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
	state, err := counter.State("state")
	if err != nil {
		t.Fatal(err)
	}
	var values []chord.JsonValue
	remove, err := state.Subscribe(func(value chord.JsonValue, _ context.Context, _ chord.ReplicatedStateDelivery) error {
		values = append(values, value)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer remove()
	value, err := chord.CallResult[int](t.Context(), counter, "add", 4)
	if err != nil || value != 5 {
		t.Fatalf("add=%d, %v", value, err)
	}
	want := []chord.JsonValue{map[string]any{"value": float64(1)}, map[string]any{"value": float64(5)}}
	if !reflect.DeepEqual(values, want) {
		t.Fatalf("state publications=%#v, want %#v", values, want)
	}
	if err := host.Dispose(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := binding.Dispose(t.Context()); err != nil {
		t.Fatal(err)
	}
	disposeBundleFacets(t, loaded)
}

// upstream: packages/chord/src/services/instances.ts:#start applies an isolated observer synchronously during instance delivery, leaves its Promise unawaited, and reports only a rejection that precedes cancellation.
func TestNodeFacetObserverRunsPrefixWithoutAwaitingPromise(t *testing.T) {
	isolateExperimentalTest(t)
	root := t.TempDir()
	entry := filepath.Join(root, "session.ts")
	writeNodeFacetFile(t, entry, `import { defineService } from "@earendil-works/chord";
const Probe = defineService("test.keyed-probe");
const Log = defineService("test.node-observe-log");
const seen = [];
export default {id:"observer",setup(env){
  env.provide(Log,{async read(){return seen.slice();}});
  env.observe(Probe,async()=>{
    seen.push("observed-"+(seen.length+1));
    await Promise.resolve();
    if(seen.length===1)throw new Error("observer continuation failed");
    await new Promise(()=>{});
  });
}};
`)
	result, err := BundleFacets(t.Context(), BundleFacetsOptions{Plugin: FacetBundlePlugin{Id: "observer"}, Entries: []FacetEntrySource{{Name: "session", Source: entry}}, Outdir: filepath.Join(root, "bundle")})
	if err != nil {
		t.Fatal(err)
	}
	loader, err := CreateSessionPluginFacetLoader([]string{result.ManifestPath})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := loader.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { disposeBundleFacets(t, loaded) })
	var spawner *chord.ServiceSpawner[keyedProbe]
	provider := chord.Facet{Id: "@test/probe-provider", Setup: func(env *chord.FacetEnvironment) error {
		var err error
		spawner, err = chord.ProvideMany(env, keyedProbeDefinition)
		return err
	}}
	spawnProbe := func(key string) {
		t.Helper()
		state, err := chord.NewReplicatedState(&keyedProbeState{Value: key})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := spawner.Spawn(key, &keyedProbeService{state: state, replace: func(string) error { return nil }}); err != nil {
			t.Fatal(err)
		}
	}
	reported := make(chan error, 4)
	host, err := chord.CreateFacetHost(t.Context(), chord.FacetOptions{Facets: append([]chord.Facet{provider}, loaded.Facets...), OnError: func(err error) { reported <- err }})
	if err != nil {
		t.Fatal(err)
	}
	hostDisposed := false
	t.Cleanup(func() {
		if !hostDisposed {
			if err := host.Dispose(context.Background()); err != nil {
				t.Error(err)
			}
		}
	})
	binding, err := chord.CreateRemoteServiceBinding(chord.RemoteServiceBindingOptions{Services: []string{"test.node-observe-log"}, Transport: chord.NewLoopbackTransport(host.Services())})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := binding.Dispose(context.Background()); err != nil {
			t.Error(err)
		}
	})
	logService, err := binding.Use("test.node-observe-log")
	if err != nil {
		t.Fatal(err)
	}
	if err := binding.Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
	readSeen := func() []string {
		t.Helper()
		seen, err := chord.CallResult[[]string](t.Context(), logService, "read")
		if err != nil {
			t.Fatal(err)
		}
		return seen
	}

	spawnProbe("first")
	if got, want := readSeen(), []string{"observed-1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("observer prefixes after first spawn = %q, want %q", got, want)
	}
	if err := <-reported; err == nil || !strings.Contains(err.Error(), "observer continuation failed") {
		t.Fatalf("reported continuation error = %v, want observer continuation failed", err)
	}
	// The second observer Promise never settles; Spawn returns after its synchronous prefix.
	spawnProbe("second")
	if got, want := readSeen(), []string{"observed-1", "observed-2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("observer prefixes after second spawn = %q, want %q", got, want)
	}
	if err := binding.Dispose(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := host.Dispose(t.Context()); err != nil {
		t.Fatal(err)
	}
	hostDisposed = true
	select {
	case err := <-reported:
		t.Fatalf("cancelled observer reported %v", err)
	default:
	}
}

// upstream: packages/chord/src/node/bundle-loader.ts:57-82. The digest covers decoded UTF-8 source, and source maps remain optional text payloads.
func TestReadFacetBundleArtifact(t *testing.T) {
	root := nodeFacetTestDirectory(t)
	for _, tc := range []struct {
		name, source, decoded string
		sourceMap             *string
	}{
		{name: "ordinary", source: "module.exports = {};\n", decoded: "module.exports = {};\n"},
		{name: "empty source"},
		{name: "decoded text", source: "\xe2\x82", decoded: "\ufffd"},
		{name: "source map", source: "abc", decoded: "abc", sourceMap: new("{\"version\":3}\n")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			directory := filepath.Join(root, tc.name)
			digest := sha256.Sum256([]byte(tc.decoded))
			entry := FacetBundleEntry{File: "entry.cjs", Integrity: "sha256-" + base64.StdEncoding.EncodeToString(digest[:]), ExternalImports: []string{"node:fs"}}
			if tc.sourceMap != nil {
				entry.SourceMap = new("entry.cjs.map")
				writeNodeFacetFile(t, filepath.Join(directory, *entry.SourceMap), *tc.sourceMap)
			}
			plugin := FacetBundlePlugin{Id: "test-bundle", Version: new("1")}
			manifest := FacetBundleManifest{Format: FacetBundleFormat, FormatVersion: FacetBundleFormatVersion, Plugin: plugin, Entries: map[string]FacetBundleEntry{"worker": entry}}
			data, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(directory, FacetBundleManifestFile)
			writeNodeFacetFile(t, path, string(data))
			writeNodeFacetFile(t, filepath.Join(directory, entry.File), tc.source)
			got, err := ReadFacetBundleArtifact(FacetBundleArtifactReadOptions{ManifestPath: path, Entry: "worker"})
			want := FacetBundleArtifact{Format: "chord.facet-bundle-artifact", FormatVersion: 2, Plugin: plugin, EntryName: "worker", Entry: entry, Source: tc.decoded, SourceMapContents: tc.sourceMap}
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("artifact = %#v, error=%v; want %#v", got, err, want)
			}
			writeNodeFacetFile(t, filepath.Join(directory, entry.File), "corrupted")
			_, err = ReadFacetBundleArtifact(FacetBundleArtifactReadOptions{ManifestPath: path, Entry: "worker"})
			if err == nil || err.Error() != "Facet bundle integrity check failed for entry.cjs" {
				t.Fatalf("corruption error = %v", err)
			}
		})
	}
}

// upstream: packages/chord/src/node/bundle-loader.ts:61-72. Entry identity is checked before I/O, and source integrity is checked before reading the optional map.
func TestReadFacetBundleArtifactErrors(t *testing.T) {
	root := nodeFacetTestDirectory(t)
	path := filepath.Join(root, FacetBundleManifestFile)
	_, err := ReadFacetBundleArtifact(FacetBundleArtifactReadOptions{ManifestPath: path})
	if err == nil || err.Error() != "Facet bundle entry name must not be empty" {
		t.Fatalf("empty entry error = %v", err)
	}
	manifest := strings.Replace(nodeFacetManifestJSON, `"file":"entry.cjs"`, `"file":"entry.cjs","sourceMap":"missing.map"`, 1)
	writeNodeFacetFile(t, path, manifest)
	_, err = ReadFacetBundleArtifact(FacetBundleArtifactReadOptions{ManifestPath: path, Entry: "missing"})
	if err == nil || err.Error() != "Facet bundle test-bundle has no entry named missing" {
		t.Fatalf("missing entry error = %v", err)
	}
	_, err = ReadFacetBundleArtifact(FacetBundleArtifactReadOptions{ManifestPath: path, Entry: "worker"})
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing source error = %v", err)
	}
	writeNodeFacetFile(t, filepath.Join(root, "entry.cjs"), "abc")
	_, err = ReadFacetBundleArtifact(FacetBundleArtifactReadOptions{ManifestPath: path, Entry: "worker"})
	if err == nil || err.Error() != "Facet bundle integrity check failed for entry.cjs" {
		t.Fatalf("integrity must precede source-map read: %v", err)
	}
	// SHA-256("abc"), independently specified by the standard hash test vector.
	manifest = strings.Replace(manifest, "sha256-pending", "sha256-ungWv48Bz+pBQUDeXa4iI7ADYaOWF3qctBD/YfIAFa0=", 1)
	writeNodeFacetFile(t, path, manifest)
	_, err = ReadFacetBundleArtifact(FacetBundleArtifactReadOptions{ManifestPath: path, Entry: "worker"})
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing source-map error = %v", err)
	}
}

// upstream: packages/chord/src/node/bundle-loader.ts:44-54,353-426.
func TestReadFacetBundleManifest(t *testing.T) {
	root := nodeFacetTestDirectory(t)
	path := filepath.Join(root, FacetBundleManifestFile)
	writeNodeFacetFile(t, path, nodeFacetManifestJSON)
	manifest, err := ReadFacetBundleManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	want := FacetBundleManifest{
		Format: "chord.facet-bundle", FormatVersion: 2,
		Plugin:  FacetBundlePlugin{Id: "test-bundle", Version: new("1")},
		Entries: map[string]FacetBundleEntry{"worker": {File: "entry.cjs", Integrity: "sha256-pending", ExternalImports: []string{"@earendil-works/chord"}}},
	}
	if !reflect.DeepEqual(manifest, want) {
		t.Fatalf("manifest = %#v, want %#v", manifest, want)
	}
	// A read validates metadata, not the integrity of an entry that has not been read. It returns a new snapshot on every invocation.
	manifest.Entries["worker"].ExternalImports[0] = "modified"
	*manifest.Plugin.Version = "modified"
	delete(manifest.Entries, "worker")
	second, err := ReadFacetBundleManifest(path)
	if err != nil || !reflect.DeepEqual(second, want) {
		t.Fatalf("reread = %#v, %v; want %#v", second, err, want)
	}
	t.Chdir(root)
	relative, err := ReadFacetBundleManifest(FacetBundleManifestFile)
	if err != nil || !reflect.DeepEqual(relative, want) {
		t.Fatalf("relative read = %#v, %v; want %#v", relative, err, want)
	}
}

// upstream: packages/chord/src/node/bundle-loader.ts:47-53. I/O and syntax are wrapped; manifest validation errors are not.
func TestReadFacetBundleManifestReadErrors(t *testing.T) {
	root := nodeFacetTestDirectory(t)
	path := filepath.Join(root, FacetBundleManifestFile)
	_, err := ReadFacetBundleManifest(path)
	want := "Could not read facet bundle manifest " + path
	if err == nil || err.Error() != want || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing manifest error = %v, want %q with ENOENT cause", err, want)
	}
	for _, input := range []string{"", "{", "{} {}", "\ufeff" + nodeFacetManifestJSON} {
		writeNodeFacetFile(t, path, input)
		_, err = ReadFacetBundleManifest(path)
		if err == nil || err.Error() != want || errors.Unwrap(err) == nil {
			t.Fatalf("input %q: error = %v, want %q with JSON cause", input, err, want)
		}
	}
	writeNodeFacetFile(t, path, "null")
	_, err = ReadFacetBundleManifest(path)
	if err == nil || err.Error() != "Invalid facet bundle manifest format in "+path || errors.Unwrap(err) != nil {
		t.Fatalf("shape failure must not be a read failure: %v", err)
	}
}

// upstream: packages/chord/src/node/bundle-loader.ts:353-426. Preserve validation order, including optional-null rejection and external string validation before duplicate detection.
func TestReadFacetBundleManifestValidation(t *testing.T) {
	root := nodeFacetTestDirectory(t)
	path := filepath.Join(root, FacetBundleManifestFile)
	cases := []struct{ name, old, value, message string }{
		{"format", `"chord.facet-bundle"`, `"wrong"`, "Invalid facet bundle manifest format in " + path},
		{"plugin null", `{"id":"test-bundle","version":"1"}`, `null`, "Facet bundle manifest has an invalid plugin identity in " + path},
		{"plugin array", `{"id":"test-bundle","version":"1"}`, `[]`, "Facet bundle manifest has an invalid plugin identity in " + path},
		{"empty id", `"id":"test-bundle"`, `"id":""`, "Facet bundle manifest has an invalid plugin identity in " + path},
		{"null id", `"id":"test-bundle"`, `"id":null`, "Facet bundle manifest has an invalid plugin identity in " + path},
		{"empty plugin version", `"version":"1"`, `"version":""`, "Facet bundle manifest has an invalid plugin version in " + path},
		{"null plugin version", `"version":"1"`, `"version":null`, "Facet bundle manifest has an invalid plugin version in " + path},
		{"numeric plugin version", `"version":"1"`, `"version":1`, "Facet bundle manifest has an invalid plugin version in " + path},
		{"invalid entry name", `"worker"`, `""`, "Facet bundle manifest has an invalid entry in " + path},
		{"missing file", `"file":"entry.cjs",`, ``, "Facet bundle entry worker has no file"},
		{"null file", `"file":"entry.cjs"`, `"file":null`, "Facet bundle entry worker has no file"},
		{"empty file", `"file":"entry.cjs"`, `"file":""`, "Facet bundle entry worker must be a filename relative to its manifest"},
		{"parent file", `"file":"entry.cjs"`, `"file":"../entry.cjs"`, "Facet bundle entry worker must be a filename relative to its manifest"},
		{"directory file", `"file":"entry.cjs"`, `"file":"sub/entry.cjs"`, "Facet bundle entry worker must be a filename relative to its manifest"},
		{"dot file", `"file":"entry.cjs"`, `"file":"."`, "Facet bundle entry worker must be a filename relative to its manifest"},
		{"dotdot file", `"file":"entry.cjs"`, `"file":".."`, "Facet bundle entry worker must be a filename relative to its manifest"},
		{"absolute file", `"file":"entry.cjs"`, `"file":"/entry.cjs"`, "Facet bundle entry worker must be a filename relative to its manifest"},
		{"missing integrity", `"integrity":"sha256-pending",`, ``, "Facet bundle entry worker has no integrity"},
		{"null integrity", `"integrity":"sha256-pending"`, `"integrity":null`, "Facet bundle entry worker has no integrity"},
		{"empty integrity", `"sha256-pending"`, `"sha256-"`, "Facet bundle entry has an invalid SHA-256 integrity value"},
		{"other digest", `"sha256-pending"`, `"sha512-pending"`, "Facet bundle entry has an invalid SHA-256 integrity value"},
		{"missing imports", `"externalImports":["@earendil-works/chord"]`, `"ignored":true`, "Facet bundle entry worker has invalid external imports"},
		{"null imports", `["@earendil-works/chord"]`, `null`, "Facet bundle entry worker has invalid external imports"},
		{"scalar imports", `["@earendil-works/chord"]`, `"@earendil-works/chord"`, "Facet bundle entry worker has invalid external imports"},
		{"null import", `["@earendil-works/chord"]`, `[null]`, "Facet bundle entry worker has invalid external imports"},
		{"duplicate imports", `["@earendil-works/chord"]`, `["a","a"]`, "Facet bundle entry worker has duplicate external imports"},
		{"invalid before duplicate", `["@earendil-works/chord"]`, `["a","a",42]`, "Facet bundle entry worker has invalid external imports"},
		{"null source map", `"file":"entry.cjs"`, `"sourceMap":null,"file":"entry.cjs"`, "Facet bundle entry worker has an invalid source map"},
		{"empty source map", `"file":"entry.cjs"`, `"sourceMap":"","file":"entry.cjs"`, "Facet bundle entry worker source map must be a filename relative to its manifest"},
		{"parent source map", `"file":"entry.cjs"`, `"sourceMap":"../entry.map","file":"entry.cjs"`, "Facet bundle entry worker source map must be a filename relative to its manifest"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			writeNodeFacetFile(t, path, strings.Replace(nodeFacetManifestJSON, tc.old, tc.value, 1))
			_, err := ReadFacetBundleManifest(path)
			if err == nil || err.Error() != tc.message {
				t.Fatalf("error = %v, want %q", err, tc.message)
			}
		})
	}
	for _, entries := range []string{"null", "[]", "{}", `{"worker":null}`, `{"worker":[]}`} {
		t.Run("entries="+entries, func(t *testing.T) {
			input := `{"format":"chord.facet-bundle","formatVersion":2,"plugin":{"id":"test"},"entries":` + entries + `}`
			writeNodeFacetFile(t, path, input)
			_, err := ReadFacetBundleManifest(path)
			message := "Facet bundle manifest has no entries in " + path
			if strings.HasPrefix(entries, `{"worker"`) {
				message = "Facet bundle manifest has an invalid entry in " + path
			}
			if err == nil || err.Error() != message {
				t.Fatalf("error = %v, want %q", err, message)
			}
		})
	}
}

// upstream: packages/chord/src/node/bundle-loader.ts:357-358 uses String(value), not JSON.stringify or Go formatting.
func TestReadFacetBundleManifestVersionDiagnostics(t *testing.T) {
	root := nodeFacetTestDirectory(t)
	path := filepath.Join(root, FacetBundleManifestFile)
	cases := []struct{ input, text string }{
		{`"formatVersion":null`, "null"}, {`"formatVersion":"2"`, "2"},
		{`"formatVersion":true`, "true"}, {`"formatVersion":false`, "false"},
		{`"formatVersion":-0`, "0"}, {`"formatVersion":1e999`, "Infinity"},
		{`"formatVersion":-1e999`, "-Infinity"}, {`"formatVersion":0.000001`, "0.000001"},
		{`"formatVersion":1e-7`, "1e-7"}, {`"formatVersion":1e21`, "1e+21"},
		{`"formatVersion":{}`, "[object Object]"}, {`"formatVersion":[]`, ""},
		{`"formatVersion":[null,[1,"v"],{}]`, ",1,v,[object Object]"},
		{`"ignored":2`, "undefined"},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			writeNodeFacetFile(t, path, strings.Replace(nodeFacetManifestJSON, `"formatVersion":2`, tc.input, 1))
			_, err := ReadFacetBundleManifest(path)
			message := fmt.Sprintf("Unsupported facet bundle manifest version in %s: %s", path, tc.text)
			if err == nil || err.Error() != message {
				t.Fatalf("error = %v, want %q", err, message)
			}
		})
	}
	for _, version := range []string{`{"toString":null}`, `[{"toString":42}]`} {
		writeNodeFacetFile(t, path, strings.Replace(nodeFacetManifestJSON, `"formatVersion":2`, `"formatVersion":`+version, 1))
		_, err := ReadFacetBundleManifest(path)
		if err == nil || err.Error() != "Cannot convert object to primitive value" {
			t.Fatalf("version %s: error = %v, want conversion error", version, err)
		}
	}
}

// upstream: packages/chord/src/node/bundle-loader.ts:44-54,353-426 and node:fs readFile(..., "utf8"). Unknown fields and syntactically valid but unresolved external specifiers are not rejected by the manifest reader.
func TestReadFacetBundleManifestJSONSemantics(t *testing.T) {
	root := nodeFacetTestDirectory(t)
	path := filepath.Join(root, FacetBundleManifestFile)
	input := strings.Replace(nodeFacetManifestJSON, `"id":"test-bundle","version":"1"`, `"id":"old","id":"\ud800","extra":1e999`, 1)
	input = strings.Replace(input, `["@earendil-works/chord"]`, `["","./relative","node:fs"]`, 1)
	input = strings.Replace(input, `"file":"entry.cjs"`, `"sourceMap":"entry.map","file":"entry.cjs"`, 1)
	writeNodeFacetFile(t, path, input)
	manifest, err := ReadFacetBundleManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Plugin.Id != "\xed\xa0\x80" || manifest.Plugin.Version != nil {
		t.Fatalf("plugin = %#v, want preserved UTF-16 id and absent version", manifest.Plugin)
	}
	entry := manifest.Entries["worker"]
	if !reflect.DeepEqual(entry.ExternalImports, []string{"", "./relative", "node:fs"}) || entry.SourceMap == nil || *entry.SourceMap != "entry.map" {
		t.Fatalf("entry = %#v", entry)
	}
	for _, tc := range []struct{ bytes, decoded string }{{"\xe2\x82", "\ufffd"}, {"\xff\xff", "\ufffd\ufffd"}, {"\xed\xa0\x80", "\ufffd\ufffd\ufffd"}} {
		writeNodeFacetFile(t, path, strings.Replace(nodeFacetManifestJSON, "test-bundle", tc.bytes, 1))
		got, err := ReadFacetBundleManifest(path)
		if err != nil || got.Plugin.Id != tc.decoded {
			t.Fatalf("bytes %x: id = %q, error = %v; want %q", tc.bytes, got.Plugin.Id, err, tc.decoded)
		}
	}
	writeNodeFacetFile(t, path, strings.Replace(nodeFacetManifestJSON, `"worker"`, `"__proto__"`, 1))
	manifest, err = ReadFacetBundleManifest(path)
	if err != nil || len(manifest.Entries) != 0 {
		t.Fatalf("__proto__ must not create an own entry: %#v, %v", manifest, err)
	}
	writeNodeFacetFile(t, path, `{"format":"chord.facet-bundle","formatVersion":2,"plugin":{"id":"test"},"entries":{"later":{},"10":{},"2":{}}}`)
	_, err = ReadFacetBundleManifest(path)
	if err == nil || err.Error() != "Facet bundle entry 2 has no file" {
		t.Fatalf("numeric Object.entries ordering: %v", err)
	}
	writeNodeFacetFile(t, path, strings.Replace(nodeFacetManifestJSON, `"file":"entry.cjs"`, `"file":"sub\\entry.cjs"`, 1))
	manifest, err = ReadFacetBundleManifest(path)
	if runtime.GOOS == "windows" {
		if err == nil || err.Error() != "Facet bundle entry worker must be a filename relative to its manifest" {
			t.Fatalf("Windows backslash path: %v", err)
		}
	} else if err != nil || manifest.Entries["worker"].File != `sub\entry.cjs` {
		t.Fatalf("POSIX backslash filename: %#v, %v", manifest, err)
	}
}
