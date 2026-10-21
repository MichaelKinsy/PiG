package subprocess

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/tsruntime"
)

// Pi loads every extension into the one process that runs it
// (packages/coding-agent/src/core/extensions/loader.ts loadExtensionModule):
// extensions share Pi's library modules and no extension adds a thread, so
// Pi's memory stays flat as extensions are added. A packed Node cell (D20)
// runs its members in one process too; an added member may cost its own
// module and runtime state, not another V8 isolate or another copy of the
// libraries.

// nodeCellCostSource imports Pi library values like an SDK extension does and
// records them, so the probe tool can compare every member's identities. The
// tool reports the process's Worker threads, its RSS and heap after a full GC
// and its peak RSS.
//
// The peak is the Node process's own. On Linux, getrusage's maxRSS (Node's
// resourceUsage().maxRSS) also carries the RSS high-water mark of the address
// space the process was exec'd from (exec_mmap folds the old mm's hiwater_rss
// into signal->maxrss), and Go starts children with CLONE_VM|CLONE_VFORK, so
// there it reports the Go test process's peak whenever that is the larger. The
// probe reads VmHWM, the high-water mark of the process's own address space,
// instead; Darwin's maxRSS starts afresh at exec.
const nodeCellCostSource = `import { Type } from "typebox";
import { getAgentDir } from "@earendil-works/pi-coding-agent";
import { readFileSync } from "node:fs";
import { setFlagsFromString } from "node:v8";
import { runInNewContext } from "node:vm";

const peakRSS = () =>
  process.platform === "linux"
    ? Number(/^VmHWM:\s+(\d+) kB$/m.exec(readFileSync("/proc/self/status", "utf8"))![1]) * 1024
    : process.resourceUsage().maxRSS * 1024;

const libraries: unknown[][] = ((globalThis as any)[Symbol.for("pig.test.nodeCellLibraries")] ??= []);
libraries.push([Type, getAgentDir]);

export default function (pi: any) {
  pi.registerTool({
    name: %[1]q,
    label: %[1]q,
    description: "Node cell cost probe",
    parameters: Type.Object({}),
    async execute() {
      setFlagsFromString("--expose-gc");
      const gc = runInNewContext("gc");
      gc();
      gc();
      const memory = process.memoryUsage();
      const shared = libraries.every(([type, agentDir]) => type === libraries[0][0] && agentDir === libraries[0][1]);
      const report = process.report.getReport() as any;
      const text = JSON.stringify({ members: libraries.length, shared, workers: report.workers.length, rss: memory.rss, heap: memory.heapUsed, external: memory.external, peak: peakRSS() });
      return { content: [{ type: "text", text }], details: {} };
    },
  });
}
`

type nodeCellCost struct {
	Members  int   `json:"members"`
	Shared   bool  `json:"shared"`
	Workers  int   `json:"workers"`
	RSS      int64 `json:"rss"`
	Heap     int64 `json:"heap"`
	External int64 `json:"external"`
	Peak     int64 `json:"peak"`
}

// nodeCellCatalog is shaped like the host's model catalog: 1,500 models of 25 providers, about 1.2 MB of JSON.
func nodeCellCatalog() []map[string]any {
	models := make([]map[string]any, 0, 1500)
	for i := range 1500 {
		provider := fmt.Sprintf("provider-%02d", i%25)
		id := fmt.Sprintf("model-%04d-%s", i, strings.Repeat("x", i%17))
		models = append(models, map[string]any{
			"api": "openai-responses", "baseUrl": "https://" + provider + ".invalid/v1", "compat": nil,
			"contextWindow": 128000 + i, "cost": map[string]any{"cacheRead": 0.05, "cacheWrite": 0.5, "input": 1.25, "output": 10.0},
			"cacheReadCostPer1M": 0.05, "cacheWriteCostPer1M": 0.5, "inputCostPer1M": 1.25, "outputCostPer1M": 10.0,
			"displayName": "Model " + id, "id": id, "modelId": id, "name": "Model " + id, "provider": provider,
			"input": []string{"text", "image"}, "maxTokens": 32000, "reasoning": i%2 == 0,
			"headers": map[string]any{"X-Provider": provider}, "thinkingLevelMap": map[string]any{"high": "high", "low": "low"},
		})
	}
	return models
}

// publishNodeCellCatalog makes the three publications a Session's model wiring makes (internal/codingagent WireModelOperations): the catalog, then the catalog with the registry state, then the same state again.
func publishNodeCellCatalog(t *testing.T, bridge *UIBridge) {
	t.Helper()
	catalog := nodeCellCatalog()
	encoded, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	providers := map[string]any{}
	for i := range 25 {
		providers[fmt.Sprintf("provider-%02d", i)] = map[string]any{"configured": true, "composed": false}
	}
	objects := modelJSONs(catalog...)
	bridge.SetModelCatalog(func() []json.RawMessage { return objects }, func() (json.RawMessage, error) { return encoded, nil })
	bridge.SetHostAction("getModelRegistryState", func() map[string]any {
		return map[string]any{"models": json.RawMessage(encoded), "providers": providers, "typedModels": []any{}}
	})
	bridge.PublishModelCatalog()
}

// nodeCellCostOf loads members cost probes, packed unless isolation says otherwise, optionally publishes the host's model catalog to them, and returns what the last one reports once every member has answered.
func nodeCellCostOf(t *testing.T, members int, isolation string, publish bool) nodeCellCost {
	t.Helper()
	nodeCellRequireNode(t)
	// The cost probe measures V8's heap after a forced collection (--expose-gc), which only the Node runtime offers.
	if request := TypeScriptRuntimeRequest(); request.ID != tsruntime.Node {
		t.Skipf("the V8 heap probe needs the Node runtime; this process requests %q", request.ID)
	}
	root := t.TempDir()
	configs := make([]ExtConfig, 0, members)
	for i := range members {
		name := fmt.Sprintf("cost-%d", i)
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		entry := filepath.Join(dir, "index.ts")
		if err := os.WriteFile(entry, fmt.Appendf(nil, nodeCellCostSource, name), 0o644); err != nil {
			t.Fatal(err)
		}
		configs = append(configs, ExtConfig{Name: name, Source: entry, Enabled: true, Isolation: isolation})
	}
	h := NewHost(t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	bridge := NewUIBridge(func() {})
	h.SetUIBridge(bridge)
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	loaded, errs := h.LoadAll(ctx, configs)
	if len(errs) != 0 || len(loaded) != members {
		t.Fatalf("LoadAll = %d loaded, %v; want %d and no errors", len(loaded), errs, members)
	}
	if publish {
		publishNodeCellCatalog(t, bridge)
	}
	// A member answers after it has handled every frame the host sent it before the call.
	tools := map[string]extension.RegisteredTool{}
	for _, ext := range h.Extensions() {
		tools[ext.Name] = ext.Tools[ext.Name]
	}
	var cost nodeCellCost
	for _, config := range configs {
		tool, ok := tools[config.Name]
		if !ok {
			t.Fatalf("extension %s is not loaded", config.Name)
		}
		result, err := tool.Definition.Execute(ctx, "cost", json.RawMessage(`{}`), nil)
		if err != nil {
			t.Fatalf("cost probe: %v", err)
		}
		if err := json.Unmarshal([]byte(result.Text()), &cost); err != nil {
			t.Fatal(err)
		}
	}
	return cost
}

// Each member's host socket needs IO that continues while a synchronous host call blocks the main thread, but one IO thread serves every socket of the process. A Worker is a V8 isolate and a Node environment, about 10 MiB of RSS before it reads a frame.
func TestNodeCellMembersShareOneIOWorker(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		isolation string
		members   int
		workers   int
	}{
		{isolation: "", members: 5, workers: 1},
		{isolation: "isolated", members: 1, workers: 1},
	} {
		cost := nodeCellCostOf(t, tc.members, tc.isolation, false)
		if cost.Members != tc.members || !cost.Shared {
			t.Fatalf("isolation %q: %d members in the process sharing library identities = %v, want %d sharing them", tc.isolation, cost.Members, cost.Shared, tc.members)
		}
		if cost.Workers != tc.workers {
			t.Errorf("isolation %q: %d members run %d Worker threads, want %d", tc.isolation, tc.members, cost.Workers, tc.workers)
		}
	}
}

// Members two to five import the Pi SDK as the first does and evaluate no second copy of it: they add only their own module and runtime state. The Worker a member would add for its own IO is counted by TestNodeCellMembersShareOneIOWorker, not here.
//
// The probe measures what the process retains after a forced full collection: V8's heapUsed plus the external memory it accounts. A second copy of the SDK's modules lives in that heap. The process's RSS is not that measure and cannot hold a budget of a few MiB: it also holds whatever of the startup transients was still resident when it was read. In 19 runs (8 unloaded, 11 under a load average of 160 to 235) one member read 131.8 to 152.2 MiB of RSS, 6.4 to 27.5 MiB below its peak, and five members 149.7 to 160.8 MiB, so RSS gave 0.8 to 7.2 MiB per added member and failed the 6 MiB budget once. The retained heap and external memory gave 0.20 to 0.25 MiB per added member in every one of those runs and in 46 more (8 unloaded, 38 loaded); a member that retains 8,000 small objects reads 0.79 MiB and one that retains 60,000 reads 4.35 MiB.
func TestNodeCellAddedMembersStayWithinTheirOwnState(t *testing.T) {
	t.Parallel()
	one := nodeCellCostOf(t, 1, "", false)
	five := nodeCellCostOf(t, 5, "", false)
	if !five.Shared {
		t.Fatal("cell members evaluated separate copies of the Pi library")
	}
	const budget = 2 << 20
	added := ((five.Heap + five.External) - (one.Heap + one.External)) / 4
	t.Logf("each added Node cell member retains %.2f MiB of heap and external memory (1 member %.1f MiB, 5 members %.1f MiB; RSS %.1f and %.1f MiB, not asserted)", float64(added)/(1<<20), float64(one.Heap+one.External)/(1<<20), float64(five.Heap+five.External)/(1<<20), float64(one.RSS)/(1<<20), float64(five.RSS)/(1<<20))
	if added > budget {
		t.Errorf("each added Node cell member retains %.2f MiB of heap and external memory, want at most %d MiB", float64(added)/(1<<20), budget>>20)
	}
}

// Pi's extensions read one ModelRegistry (model-registry.ts). The host sends each catalog publication, about 1.2 MB of JSON, to every member's socket; an added member must not pay for its own IO Worker and its own eagerly decoded and normalized copies of three publications, while each keeps its own model objects. The base peaks at about 24 MiB more per member. TestNodeProviderSocketsShareARepeatedLargeFrame proves that the cell reads and decodes a repeated publication once; this test bounds what a member adds to the peak.
//
// The peak depends on when V8 collects the transient publication bytes and envelopes, which a loaded host delays: under a load average of about 250 this branch measured 2.1 to 8.9 MiB per added member, 3.4 to 11.2 MiB with frame sharing disabled, and the base 22.0 to 28.5 MiB. The budget sits between those peaks and the base's.
func TestNodeCellMembersShareRegistryPublications(t *testing.T) {
	t.Parallel()
	one := nodeCellCostOf(t, 1, "", true)
	five := nodeCellCostOf(t, 5, "", true)
	const budget = 16 << 20
	added := (five.Peak - one.Peak) / 4
	t.Logf("each added Node cell member raises the peak RSS by %.1f MiB (1 member %.1f MiB, 5 members %.1f MiB)", float64(added)/(1<<20), float64(one.Peak)/(1<<20), float64(five.Peak)/(1<<20))
	t.Logf("each added Node cell member retains %.2f MiB of heap (1 member %.2f MiB, 5 members %.2f MiB)", float64(five.Heap-one.Heap)/4/(1<<20), float64(one.Heap)/(1<<20), float64(five.Heap)/(1<<20))
	if added > budget {
		t.Errorf("each added Node cell member raises the peak RSS by %.1f MiB, want at most %d MiB", float64(added)/(1<<20), budget>>20)
	}
}
