package subprocess

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"
)

// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/8423-extension-factory-failure.test.ts:12
// This guards the failed-API part of the case, including synchronous rejection before each method's own validation.
func TestUpstreamFailedFactoryDisablesCapturedAPI(t *testing.T) {
	nodeCellRequireNode(t)
	probe := filepath.Join(findModuleRoot(t), "test/parity/scenarios/extensions-runtime/testdata/factory-failure/probe.mjs")
	script := fmt.Sprintf(`
import assert from "node:assert/strict";
import { pathToFileURL } from "node:url";
import { Runtime } from "./runtime-node/runtime.mjs";
const { probeFailedAPI } = await import(pathToFileURL(%q));
const failed = new Runtime("<failing>");
failed.api.registerFlag("failed-flag", {type:"boolean", default:true});
assert.equal(failed.api.getFlag("failed-flag"), true);
let heard = 0;
const unsubscribe = failed.api.events.on("factory-failure", () => heard++);
const survivor = new Runtime("<working>");
survivor.commitLoad();
failed.discardLoad();
failed.discardLoad();
failed.commitLoad();
const collections = [failed.handlers, failed.tools, failed.commands, failed.shortcuts, failed.flags, failed.renderers, failed.entryRenderers, failed.providers, failed.registeredProviderConfigs, failed.nativeProviders, failed.providerStreams, failed.oauthProviders];
const sizes = collections.map(collection => collection.size);
let lateEvents = 0;
const results = await probeFailedAPI(failed.api, () => lateEvents++);
for (const result of results) {
  assert.deepEqual(result, {method:result.method, asynchronous:false, error:'Extension "<failing>" failed to load and its API is no longer active.'});
}
assert.deepEqual(results.map(result => result.method).sort(), Object.entries(failed.api).flatMap(([name, value]) => name === "events" ? Object.keys(value).map(key => "events." + key) : [name]).filter(name => !["messageRole", "messageText"].includes(name)).sort());
unsubscribe();
unsubscribe();
survivor.api.events.emit("factory-failure", undefined);
assert.equal(heard, 0);
assert.equal(lateEvents, 0);
assert.deepEqual(collections.map(collection => collection.size), sizes);
assert.equal(failed.markdownTransformer, undefined);
survivor.api.registerFlag("live", {type:"string", default:"still-active"});
assert.equal(survivor.api.getFlag("live"), "still-active");
`, probe)
	if output, err := exec.CommandContext(t.Context(), "node", "--input-type=module", "--eval", script).CombinedOutput(); err != nil {
		t.Fatalf("failed API: %v\n%s", err, output)
	}
}

// The real shared cell catches the factory rejection and then admits the survivor; it must not leave the failed API usable through a retained reference.
func TestFailedFactoryCapturedAPIThroughNodeCell(t *testing.T) {
	nodeCellRequireNode(t)
	root := findModuleRoot(t)
	fixtures := filepath.Join(root, "test/parity/scenarios/extensions-runtime/testdata/factory-failure")
	failed := filepath.Join(fixtures, "failing.mjs")
	host := NewHost(t.TempDir())
	t.Cleanup(func() { host.Shutdown("test done") })
	loaded, failures := host.LoadAll(t.Context(), []ExtConfig{
		{Name: "failing", Source: failed, Enabled: true},
		{Name: "survivor", Source: filepath.Join(fixtures, "survivor.mjs"), Enabled: true},
	})
	if len(failures) != 1 || len(loaded) != 1 || loaded[0].Name != "survivor" {
		t.Fatalf("loaded=%v failures=%v", loaded, failures)
	}
	failure, ok := errors.AsType[*FactoryLoadError](failures[0])
	if !ok || failure.Message != "Failed to load extension: factory failed" {
		t.Fatalf("failure=%v", failures[0])
	}
	var report struct {
		FlagDuringLoad bool `json:"flagDuringLoad"`
		EventCalls     int  `json:"eventCalls"`
		SurvivorEvents int  `json:"survivorEvents"`
		LateEventCalls int  `json:"lateEventCalls"`
		Results        []struct {
			Method       string  `json:"method"`
			Asynchronous bool    `json:"asynchronous"`
			Error        *string `json:"error"`
		} `json:"results"`
	}
	description := loaded[0].Commands["factory-failure-report"].Description
	if err := json.Unmarshal([]byte(description), &report); err != nil {
		t.Fatal(err)
	}
	if !report.FlagDuringLoad || report.EventCalls != 0 || report.SurvivorEvents != 2 || report.LateEventCalls != 0 || len(report.Results) == 0 {
		t.Fatalf("failed factory state: %s", description)
	}
	want := "Extension \"" + failed + "\" failed to load and its API is no longer active."
	for _, result := range report.Results {
		got := "<nil>"
		if result.Error != nil {
			got = *result.Error
		}
		if result.Asynchronous || got != want {
			t.Errorf("%s: asynchronous=%v error=%q; want synchronous %q", result.Method, result.Asynchronous, got, want)
		}
	}
}

func BenchmarkFailedFactoryAPIThroughNodeCell(b *testing.B) {
	nodeCellRequireNode(b)
	fixtures := filepath.Join(findModuleRoot(b), "test/parity/scenarios/extensions-runtime/testdata/factory-failure")
	root, cache := b.TempDir(), b.TempDir()
	configs := []ExtConfig{
		{Name: "failing", Source: filepath.Join(fixtures, "failing.mjs"), Enabled: true},
		{Name: "survivor", Source: filepath.Join(fixtures, "survivor.mjs"), Enabled: true},
	}
	run := func() {
		host := NewHostWithConfigRoot(root, cache)
		loaded, failures := host.LoadAll(b.Context(), configs)
		host.Shutdown("benchmark complete")
		if len(failures) != 1 || len(loaded) != 1 || loaded[0].Name != "survivor" {
			b.Fatalf("loaded=%v failures=%v", loaded, failures)
		}
	}
	run()
	b.ReportAllocs()
	for b.Loop() {
		run()
	}
}
