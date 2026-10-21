package extensionconformance

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Same-tick visibility of the Node runtime. Pi's extension API reads and writes one object graph, so a write an extension makes is visible to the very next
// line of its code: ModelRuntime.registerProvider recomposes the provider and rewrites the model snapshot before it returns (model-runtime.ts:928-949,
// 951-957, 964-992), AgentSession.getCommands reads the runner's live command Map (agent-session.ts:3391-3414), and agent.abort() aborts the run's
// AbortController synchronously (agent.ts:336-342, agent-session.ts:3472-3478). The Node runtime answers these reads from state the host replicates, so
// each row writes and reads in one handler turn. The expected values are what Pi 1.3.0 prints for the same extension (oracle runs in the commit message).

// nodeSyncRig is a Node extension host over the production model registry: a real registry behind the Host's provider callbacks, and the registry
// snapshots the binary's bridge publishes (coding/cli/extensions.go wireSubprocessModelRegistry).
type nodeSyncRig struct {
	*nodeAPIRig
	services *coding.AgentSessionServices
}

func newNodeSyncRig(t *testing.T, isolation string, actions *subprocess.HostCallbacks, fixtures ...nodeAPIFixture) *nodeSyncRig {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (starts Node subprocesses)")
	}
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	agentDir := filepath.Join(home, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	notify, status := &[]string{}, &[]string{}
	ui := newRecordingUI(notify, status)
	bridge := subprocess.NewUIBridge(func() {})
	bridge.SetUIContext(ui)
	bridge.SetNotifyFunc(ui.RecordNotify)
	if actions != nil {
		bridge.SetActions(actions)
	}
	runtime := services.ModelRuntime()
	detach := codingagent.WireModelOperations(bridge, codingagent.ModelOperationBindings{ModelLookup: runtime.GetModel, ModelCatalog: func(...string) []*ai.Model { return runtime.GetModels() }, Registry: services.Registry().ModelRegistry})
	t.Cleanup(detach)
	h, loaded := loadNodeFixturesWith(t, isolation, bridge, fixtures, func(h *subprocess.Host) {
		h.SetProviderCallbacks(services.Registry().RegisterExtensionProvider, services.Registry().UnregisterProvider)
		h.SetNativeProviderCallback(services.Registry().RegisterNativeProvider)
		// The binary binds the virtual models its extensions register to the registry once they loaded (flushExtensionHostVirtualModels); a later registration applies at once.
		h.Runtime().BindProviderActions(extension.ProviderActions{RegisterVirtualModel: services.Registry().RegisterVirtualModel, UnregisterVirtualModel: services.Registry().UnregisterVirtualModel}, nil)
	})
	rig := &nodeAPIRig{t: t, host: h, bridge: bridge, ui: ui, exts: map[string]extension.Extension{}}
	for _, ext := range loaded {
		rig.exts[ext.Name] = ext
	}
	if len(rig.exts) != len(fixtures) {
		t.Fatalf("loaded %d of %d extensions", len(rig.exts), len(fixtures))
	}
	rig.runner = inproc.NewRunner(loaded, t.TempDir(), h.Runtime())
	bridge.SetUIPromptScope(rig.runner)
	return &nodeSyncRig{nodeAPIRig: rig, services: services}
}

// reported waits for the notification "<tag>|<value>" and returns the value.
func (r *nodeSyncRig) reported(tag string) string {
	r.t.Helper()
	var value string
	waitFor(r.t, func() bool {
		for _, line := range r.notifications() {
			if rest, ok := strings.CutPrefix(line, tag+"|"); ok {
				value = rest
				return true
			}
		}
		return false
	})
	return value
}

// The reads a registered provider changes, as one JSON array: find, getAll, getAvailable, hasConfiguredAuth, getProviderAuthStatus and isUsingOAuth.
const nodeSyncReads = `
const reads = (mr, provider, id) => [
  mr.find(provider, id) !== undefined,
  mr.getAll().some((m) => m.provider === provider),
  mr.getAvailable().some((m) => m.provider === provider),
  mr.hasConfiguredAuth({ provider }),
  mr.getProviderAuthStatus(provider),
  mr.isUsingOAuth({ provider }),
];
const line = (ctx, tag, value) => ctx.ui.notify(tag + "|" + JSON.stringify(value), "info");
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
`

const nodeSyncRegistryFixture = nodeSyncReads + `
const model = { id: "m", name: "M", reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 1000, maxTokens: 100 };
const config = (baseUrl) => ({ baseUrl, apiKey: "k", api: "openai-completions", models: [model] });
export default function (pi) {
  // The audit's repro (model-runtime.ts:928-949): a config registration, then every read of the facade in the same turn, then the unregistration.
  pi.registerCommand("config", { handler: async (_args, ctx) => {
    const mr = ctx.modelRegistry;
    line(ctx, "config-before", reads(mr, "sync-x", "m"));
    mr.registerProvider("sync-x", config("http://x.invalid/v1"));
    line(ctx, "config-registered", reads(mr, "sync-x", "m"));
    line(ctx, "config-ids", mr.getRegisteredProviderIds().includes("sync-x"));
    mr.unregisterProvider("sync-x");
    line(ctx, "config-unregistered", reads(mr, "sync-x", "m"));
    await sleep(300);
    line(ctx, "config-settled", reads(mr, "sync-x", "m"));
  } });
  // The same registration and unregistration after the handler yielded: the host's later publications keep the view the write made.
  pi.registerCommand("settle", { handler: async (_args, ctx) => {
    const mr = ctx.modelRegistry;
    mr.registerProvider("sync-y", config("http://y.invalid/v1"));
    await sleep(300);
    line(ctx, "settle-registered", reads(mr, "sync-y", "m"));
    mr.unregisterProvider("sync-y");
    await sleep(300);
    line(ctx, "settle-unregistered", reads(mr, "sync-y", "m"));
  } });
  // A registration over a built-in provider changes its models in place, and its unregistration restores them (model-runtime.ts:942-948): the models stay.
  pi.registerCommand("builtin", { handler: async (_args, ctx) => {
    const mr = ctx.modelRegistry;
    const summary = () => { const models = mr.getAll().filter((m) => m.provider === "anthropic"); return [models.length > 0, models[0]?.baseUrl]; };
    line(ctx, "builtin-before", summary());
    mr.registerProvider("anthropic", { baseUrl: "http://anthropic.invalid" });
    line(ctx, "builtin-overridden", summary());
    mr.unregisterProvider("anthropic");
    line(ctx, "builtin-restored", summary());
  } });
  // Re-registration merges over the earlier one (model-runtime.ts:934-940): the second registration's models replace the first's in the same turn.
  pi.registerCommand("replace", { handler: async (_args, ctx) => {
    const mr = ctx.modelRegistry;
    mr.registerProvider("sync-z", config("http://z.invalid/v1"));
    mr.registerProvider("sync-z", { ...config("http://z.invalid/v2"), models: [{ ...model, id: "m2" }] });
    line(ctx, "replace", [mr.find("sync-z", "m") !== undefined, mr.find("sync-z", "m2") !== undefined, mr.find("sync-z", "m2")?.baseUrl]);
  } });
  // Many registrations in a row while the host publishes the catalog from another thread: no publication may undo a write the extension already made.
  pi.registerCommand("churn", { handler: async (args, ctx) => {
    const mr = ctx.modelRegistry;
    const wrong = [];
    for (let i = 0; i < Number(args); i++) {
      const id = "churn-" + (i % 3);
      mr.registerProvider(id, config("http://churn.invalid/v1"));
      if (mr.find(id, "m") === undefined) wrong.push("registered " + i);
      mr.unregisterProvider(id);
      if (mr.find(id, "m") !== undefined) wrong.push("unregistered " + i);
      if (i % 10 === 0) await sleep(1);
    }
    line(ctx, "churn", wrong);
  } });
}
`

// model-runtime.ts:928-957 and model-registry.ts:64-74. Pi 1.3.0 prints, for the config registration in a handler:
//
//	before        [false,false,false,false,{"configured":false},false]
//	registered    [true,true,true,true,{"configured":true,"source":"fallback"},false]
//	unregistered  [false,false,false,true,{"configured":true,"source":"environment","label":"configured provider"},false]   (hasConfiguredAuth and the status are Pi's provisional marks, which its async availability refresh clears)
//	settled       [false,false,false,false,{"configured":false},false]
//
// An unregistration removes the models in the same turn: find, getAll and getAvailable. The provisional marks are not mirrored: the host answers with the settled state.
func TestNodeRegistryProviderRegistrationIsVisibleInTheSameTick(t *testing.T) {
	eachNodeAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newNodeSyncRig(t, isolation, nil, nodeAPIFixture{"nodesync-registry", nodeSyncRegistryFixture})
		rig.command("nodesync-registry", "config", "")
		const unset = `[false,false,false,false,{"configured":false},false]`
		for tag, want := range map[string]string{
			"config-before":     unset,
			"config-registered": `[true,true,true,true,{"configured":true,"source":"fallback"},false]`,
			"config-ids":        `true`,
			"config-settled":    unset,
		} {
			if got := rig.reported(tag); got != want {
				t.Errorf("%s = %s, want %s", tag, got, want)
			}
		}
		if got := rig.reported("config-unregistered"); !strings.HasPrefix(got, "[false,false,false,") {
			t.Errorf("config-unregistered = %s, want find, getAll and getAvailable to report the models gone", got)
		}
	})
}

// The host's later publications keep what the write made: the registered provider is still there after the handler yielded, and the unregistered one stays gone.
func TestNodeRegistryRegistrationSurvivesLaterPublications(t *testing.T) {
	eachNodeAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newNodeSyncRig(t, isolation, nil, nodeAPIFixture{"nodesync-registry", nodeSyncRegistryFixture})
		rig.command("nodesync-registry", "settle", "")
		if got, want := rig.reported("settle-registered"), `[true,true,true,true,{"configured":true,"source":"fallback"},false]`; got != want {
			t.Errorf("settle-registered = %s, want %s", got, want)
		}
		if got, want := rig.reported("settle-unregistered"), `[false,false,false,false,{"configured":false},false]`; got != want {
			t.Errorf("settle-unregistered = %s, want %s", got, want)
		}
	})
}

// model-runtime.ts:942-948: a built-in provider's registration is an overlay. Pi 1.3.0 prints [true,"http://anthropic.invalid"] after it and
// [true,"https://api.anthropic.com"] after its unregistration, so the unregistration recomposes the built-in and removes no model.
func TestNodeRegistryBuiltinOverlayRegistrationIsVisibleInTheSameTick(t *testing.T) {
	eachNodeAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newNodeSyncRig(t, isolation, nil, nodeAPIFixture{"nodesync-registry", nodeSyncRegistryFixture})
		rig.command("nodesync-registry", "builtin", "")
		for tag, want := range map[string]string{
			"builtin-before":     `[true,"https://api.anthropic.com"]`,
			"builtin-overridden": `[true,"http://anthropic.invalid"]`,
			"builtin-restored":   `[true,"https://api.anthropic.com"]`,
		} {
			if got := rig.reported(tag); got != want {
				t.Errorf("%s = %s, want %s", tag, got, want)
			}
		}
	})
}

// model-runtime.ts:934-940: a second registration of a provider merges over the first and replaces its models, in the turn that registers it. The second registration is complete on its own, as Pi's
// validation requires (a models list needs a baseUrl and an api): Pi 1.3.0 prints [false,true,"http://z.invalid/v2"], the first registration's model gone and the second's there with the new baseUrl.
func TestNodeRegistryReRegistrationIsVisibleInTheSameTick(t *testing.T) {
	eachNodeAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newNodeSyncRig(t, isolation, nil, nodeAPIFixture{"nodesync-registry", nodeSyncRegistryFixture})
		rig.command("nodesync-registry", "replace", "")
		if got, want := rig.reported("replace"), `[false,true,"http://z.invalid/v2"]`; got != want {
			t.Errorf("replace = %s, want %s", got, want)
		}
	})
}

// The registry publishes its catalog from the host's goroutines (another extension's registration, a refresh, a login), so a publication can be in
// flight while the extension writes. Every read of every write must still see that write.
func TestNodeRegistryWritesSurviveConcurrentPublications(t *testing.T) {
	eachNodeAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newNodeSyncRig(t, isolation, nil, nodeAPIFixture{"nodesync-registry", nodeSyncRegistryFixture})
		stop := make(chan struct{})
		var published sync.WaitGroup
		published.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
					rig.bridge.PublishModelCatalog()
					time.Sleep(time.Millisecond)
				}
			}
		})
		t.Cleanup(func() { close(stop); published.Wait() })
		rig.command("nodesync-registry", "churn", "60")
		if got := rig.reported("churn"); got != "[]" {
			t.Errorf("churn = %s, want every read to see its write", got)
		}
	})
}

const nodeSyncNativeFixture = nodeSyncReads + `
const nativeModel = { id: "nm", name: "NM", api: "openai-completions", provider: "native-sync", baseUrl: "http://native.invalid/v1", reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 1000, maxTokens: 100 };
const nativeProvider = {
  id: "native-sync", name: "Native Sync", baseUrl: nativeModel.baseUrl,
  getModels: () => [nativeModel],
  auth: { apiKey: { name: "API key", check: async () => ({ type: "api_key", source: "native" }), resolve: async () => ({ auth: { apiKey: "key" } }) } },
  stream() { throw new Error("unused"); }, streamSimple() { throw new Error("unused"); },
};
const definition = { provider: "sync-v", id: "v1", name: "V One", contextWindow: 1000, maxTokens: 100, input: ["text"], route: () => { throw new Error("unused"); } };
export default function (pi) {
  pi.registerCommand("native", { handler: async (_args, ctx) => {
    const mr = ctx.modelRegistry;
    line(ctx, "native-before", reads(mr, "native-sync", "nm"));
    mr.registerProvider(nativeProvider);
    line(ctx, "native-registered", reads(mr, "native-sync", "nm"));
    mr.unregisterProvider("native-sync");
    line(ctx, "native-unregistered", reads(mr, "native-sync", "nm"));
    await sleep(300);
    line(ctx, "native-settled", reads(mr, "native-sync", "nm"));
  } });
  pi.registerCommand("virtual", { handler: async (_args, ctx) => {
    const mr = ctx.modelRegistry;
    line(ctx, "virtual-before", reads(mr, "sync-v", "v1"));
    pi.registerVirtualModel(definition);
    line(ctx, "virtual-registered", reads(mr, "sync-v", "v1"));
    pi.unregisterVirtualModel("sync-v", "v1");
    line(ctx, "virtual-unregistered", reads(mr, "sync-v", "v1"));
  } });
  pi.registerCommand("virtual-settle", { handler: async (_args, ctx) => {
    const mr = ctx.modelRegistry;
    pi.registerVirtualModel({ ...definition, provider: "sync-w" });
    await sleep(300);
    line(ctx, "virtual-settle-registered", reads(mr, "sync-w", "v1"));
    pi.unregisterVirtualModel("sync-w", "v1");
    await sleep(300);
    line(ctx, "virtual-settle-unregistered", reads(mr, "sync-w", "v1"));
  } });
  pi.registerCommand("virtual-churn", { handler: async (args, ctx) => {
    const mr = ctx.modelRegistry;
    const wrong = [];
    for (let i = 0; i < Number(args); i++) {
      const provider = "churn-v-" + (i % 3);
      pi.registerVirtualModel({ ...definition, provider });
      if (mr.find(provider, "v1") === undefined) wrong.push("registered " + i);
      pi.unregisterVirtualModel(provider, "v1");
      if (mr.find(provider, "v1") !== undefined) wrong.push("unregistered " + i);
      if (i % 10 === 0) await sleep(1);
    }
    line(ctx, "virtual-churn", wrong);
  } });
  pi.registerCommand("virtual-keep", { handler: async (_args, ctx) => pi.registerVirtualModel({ ...definition, provider: "sync-k" }) });
}
`

// model-runtime.ts:889-905 (registerNativeProvider), 951-957 (unregisterProvider): a native Provider's models are visible in the turn that registers it and gone in
// the turn that unregisters it. getAvailable follows the provider's own availability check, which the host answers asynchronously, so it is not read here.
func TestNodeRegistryNativeProviderRegistrationIsVisibleInTheSameTick(t *testing.T) {
	eachNodeAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newNodeSyncRig(t, isolation, nil, nodeAPIFixture{"nodesync-native", nodeSyncNativeFixture})
		rig.command("nodesync-native", "native", "")
		if got := rig.reported("native-before"); !strings.HasPrefix(got, "[false,false,false,false,") {
			t.Errorf("native-before = %s", got)
		}
		if got := rig.reported("native-registered"); !strings.HasPrefix(got, "[true,true,") {
			t.Errorf("native-registered = %s, want find and getAll to see the provider's models", got)
		}
		for _, tag := range []string{"native-unregistered", "native-settled"} {
			if got := rig.reported(tag); !strings.HasPrefix(got, "[false,false,false,") {
				t.Errorf("%s = %s, want the models gone", tag, got)
			}
		}
	})
}

// model-runtime.ts:964-992: a virtual model is visible in the turn that registers it, and a provider of only virtual models needs no credentials, so it is configured and
// available at once. Pi 1.3.0 prints, for the registration in a handler:
//
//	before        [false,false,false,false,{"configured":false},false]
//	registered    [true,true,true,true,{"configured":true,"source":"environment","label":"virtual"},false]
//	unregistered  [false,false,false,true,{"configured":true,"source":"environment","label":"virtual"},false]   (hasConfiguredAuth and the status are Pi's provisional marks, which its async availability refresh clears)
//
// The registrations reach the host as the production binary binds them (flushExtensionHostVirtualModels), so the host registry holds them when the call returns.
func TestNodeRegistryVirtualModelRegistrationIsVisibleInTheSameTick(t *testing.T) {
	eachNodeAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newNodeSyncRig(t, isolation, nil, nodeAPIFixture{"nodesync-native", nodeSyncNativeFixture})
		rig.command("nodesync-native", "virtual", "")
		if got := rig.reported("virtual-before"); got != `[false,false,false,false,{"configured":false},false]` {
			t.Errorf("virtual-before = %s", got)
		}
		if got, want := rig.reported("virtual-registered"), `[true,true,true,true,{"configured":true,"source":"environment","label":"virtual"},false]`; got != want {
			t.Errorf("virtual-registered = %s, want %s", got, want)
		}
		if got := rig.reported("virtual-unregistered"); !strings.HasPrefix(got, "[false,false,false,") {
			t.Errorf("virtual-unregistered = %s, want find, getAll and getAvailable to report the model gone", got)
		}
	})
}

// The host's later publications keep what the write made: the registered virtual model is still there after the handler yielded, and the unregistered one stays gone.
func TestNodeRegistryVirtualModelSurvivesLaterPublications(t *testing.T) {
	eachNodeAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newNodeSyncRig(t, isolation, nil, nodeAPIFixture{"nodesync-native", nodeSyncNativeFixture})
		rig.command("nodesync-native", "virtual-settle", "")
		if got, want := rig.reported("virtual-settle-registered"), `[true,true,true,true,{"configured":true,"source":"environment","label":"virtual"},false]`; got != want {
			t.Errorf("virtual-settle-registered = %s, want %s", got, want)
		}
		if got := rig.reported("virtual-settle-unregistered"); !strings.HasPrefix(got, "[false,false,false,") {
			t.Errorf("virtual-settle-unregistered = %s, want the model gone", got)
		}
	})
}

// A publication can be in flight while the extension registers or removes a virtual model, and the host's own refresh publishes after each call. Every read of every write must still see that write.
func TestNodeRegistryVirtualWritesSurviveConcurrentPublications(t *testing.T) {
	eachNodeAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newNodeSyncRig(t, isolation, nil, nodeAPIFixture{"nodesync-native", nodeSyncNativeFixture})
		stop := make(chan struct{})
		var published sync.WaitGroup
		published.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
					rig.bridge.PublishModelCatalog()
					time.Sleep(time.Millisecond)
				}
			}
		})
		t.Cleanup(func() { close(stop); published.Wait() })
		rig.command("nodesync-native", "virtual-churn", "60")
		if got := rig.reported("virtual-churn"); got != "[]" {
			t.Errorf("virtual-churn = %s, want every read to see its write", got)
		}
	})
}

const nodeSyncReaderFixture = nodeSyncReads + `
export default function (pi) {
  pi.registerCommand("read-virtual", { handler: async (args, ctx) => line(ctx, "read-virtual-" + args, reads(ctx.modelRegistry, "sync-k", "v1")) });
}
`

// The model registry is the session's, shared by every extension (Pi's one ModelRuntime). A virtual model one Node extension registers reaches another extension's
// ctx.modelRegistry once the host publishes it: the registry's change listener does not see virtual models, so the host publishes the catalog when it serves the call.
// The reader asks again until it sees the model, and fails when the host never publishes it.
func TestNodeRegistryVirtualModelReachesAnotherExtension(t *testing.T) {
	eachNodeAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newNodeSyncRig(t, isolation, nil, nodeAPIFixture{"nodesync-native", nodeSyncNativeFixture}, nodeAPIFixture{"nodesync-reader", nodeSyncReaderFixture})
		rig.command("nodesync-native", "virtual-keep", "")
		got := ""
		for poll := range 100 {
			tag := "read-virtual-" + strconv.Itoa(poll)
			rig.command("nodesync-reader", "read-virtual", strconv.Itoa(poll))
			if got = rig.reported(tag); strings.HasPrefix(got, "[true,true,true,true,") {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Errorf("the reader never saw the virtual model: last read = %s", got)
	})
}

// runControl is the run a Session would hold: its context is agent.signal, and the abort action is agent.abort().
type runControl struct {
	mu     sync.Mutex
	run    context.Context
	cancel context.CancelFunc
}

func (c *runControl) current() context.Context {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.run
}

func (c *runControl) begin(t *testing.T, notify func()) {
	run, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	c.mu.Lock()
	c.run, c.cancel = run, cancel
	c.mu.Unlock()
	notify()
}

func (c *runControl) end(notify func()) {
	c.mu.Lock()
	c.run, c.cancel = nil, nil
	c.mu.Unlock()
	notify()
}

func (c *runControl) abort() {
	c.mu.Lock()
	cancel := c.cancel
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

const nodeSyncAbortFixture = nodeSyncReads + `
export default function (pi) {
  // agent-session.ts:3472-3478 and agent.ts:336-342: ctx.abort() aborts the run's signal before it returns, and the signal a tool receives is that run's.
  pi.registerTool({
    name: "abort_probe", label: "abort", description: "Aborts the run and reads its signals",
    parameters: { type: "object", properties: {} },
    async execute(_id, _params, signal, _onUpdate, ctx) {
      const held = ctx.signal;
      const before = [held?.aborted, signal?.aborted, ctx.signal === signal];
      ctx.abort();
      line(ctx, "abort-tool", { before, after: [held?.aborted, signal?.aborted, ctx.signal?.aborted] });
      return { content: [{ type: "text", text: "done" }] };
    },
  });
  pi.registerCommand("abort", { handler: async (_args, ctx) => {
    const held = ctx.signal;
    const before = held === undefined ? "none" : held.aborted;
    ctx.abort();
    line(ctx, "abort-command", [before, held === undefined ? "none" : held.aborted, ctx.signal === undefined ? "none" : ctx.signal.aborted]);
  } });
  // A guard of the shape the audit names: abort, then check the signal without yielding.
  pi.registerCommand("guard", { handler: async (_args, ctx) => {
    ctx.abort();
    line(ctx, "guard", ctx.signal?.aborted === true ? "stopped early" : "ran to completion");
  } });
}
`

// agent.ts:336-342: Agent.abort aborts activeRun's controller, and the signal that run handed out is aborted before abort() returns. Pi 1.3.0 prints, for the tool,
// {"before":[false,false,true],"after":[true,true,true]}. The host's run_signal frame arrives later and agrees with it.
func TestNodeContextAbortAbortsTheRunSignalInTheSameTick(t *testing.T) {
	eachNodeAPIIsolation(t, func(t *testing.T, isolation string) {
		var run runControl
		rig := newNodeSyncRig(t, isolation, &subprocess.HostCallbacks{GetSignal: run.current, Abort: run.abort}, nodeAPIFixture{"nodesync-abort", nodeSyncAbortFixture})
		changed := rig.bridge.RunSignalChanged
		// The host's abort lands after the runtime's own. A run ends only once its context is cancelled, or the late abort would reach the next run.
		landed := func() {
			if held := run.current(); held != nil {
				waitFor(t, func() bool { return held.Err() != nil })
			}
		}

		run.begin(t, changed)
		if _, err := rig.execute("nodesync-abort", "abort_probe", "call-1"); err != nil {
			t.Fatal(err)
		}
		if got, want := rig.reported("abort-tool"), `{"before":[false,false,true],"after":[true,true,true]}`; got != want {
			t.Errorf("abort-tool = %s, want %s", got, want)
		}

		// The next run is a new signal: the abort of the earlier run does not reach it, so the signal the command holds before it aborts is live, and the same object is aborted after.
		landed()
		run.end(changed)
		run.begin(t, changed)
		rig.command("nodesync-abort", "abort", "")
		if got, want := rig.reported("abort-command"), `[false,true,true]`; got != want {
			t.Errorf("abort-command = %s, want %s", got, want)
		}
		landed()
		run.end(changed)
		run.begin(t, changed)
		rig.command("nodesync-abort", "guard", "")
		if got, want := rig.reported("guard"), `"stopped early"`; got != want {
			t.Errorf("guard = %s, want %s", got, want)
		}

		// No run, no signal: abort aborts nothing and throws nothing (agent.ts:340-342 reads activeRun with `?.`).
		landed()
		run.end(changed)
		rig.command("nodesync-abort", "abort", "")
		waitFor(t, func() bool {
			lines := 0
			for _, line := range rig.notifications() {
				if line == `abort-command|["none","none","none"]` {
					lines++
				}
			}
			return lines == 1
		})
	})
}

const nodeSyncCommandsFixture = nodeSyncReads + `
export default function (pi) {
  const extensionCommands = () => pi.getCommands().filter((command) => command.source === "extension");
  // loader.ts:303-318 writes the extension's own command Map, and agent-session.ts:3391-3414 reads it live: a command registered in a handler is listed by the next getCommands.
  pi.registerCommand("late", { handler: async (_args, ctx) => {
    const before = extensionCommands().map((command) => command.name);
    pi.registerCommand("late-added", { description: "first", handler: async () => {} });
    const added = extensionCommands().find((command) => command.name === "late-added");
    line(ctx, "commands-first", [before.includes("late-added"), added !== undefined, added?.description, added?.source]);
    pi.registerCommand("late-added", { description: "second", handler: async () => {} });
    line(ctx, "commands-second", extensionCommands().filter((command) => command.name === "late-added").map((command) => command.description));
  } });
}
`

// agent-session.ts:3391-3414: getCommands reads the runner's registered commands live. Pi 1.3.0 prints [false,true,"first","extension"] for the first registration
// and ["second"] after the same name is registered again.
func TestNodeGetCommandsListsACommandRegisteredAfterLoad(t *testing.T) {
	eachNodeAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newNodeSyncRig(t, isolation, nil, nodeAPIFixture{"nodesync-commands", nodeSyncCommandsFixture})
		// The catalog every mode binds: the runner's extension commands, versioned so a state push re-encodes it only when it changed.
		catalog := &codingagent.SlashCommandCatalog{Runner: rig.runner}
		var version codingagent.CommandCatalogVersion
		rig.bridge.SetHostAction("getCommands", func() []subprocess.CommandInfo { return catalog.SubprocessCommands() })
		rig.bridge.SetHostAction("getCommandsVersion", func() uint64 { return version.Version(catalog, rig.runner) })
		rig.command("nodesync-commands", "late", "")
		if got, want := rig.reported("commands-first"), `[false,true,"first","extension"]`; got != want {
			t.Errorf("commands-first = %s, want %s", got, want)
		}
		if got, want := rig.reported("commands-second"), `["second"]`; got != want {
			t.Errorf("commands-second = %s, want %s", got, want)
		}
	})
}
