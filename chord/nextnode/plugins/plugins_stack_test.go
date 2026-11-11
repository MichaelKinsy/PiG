package plugins

// pi: packages/chord/src/node/plugins/stack.ts

import (
	"regexp"
	"strings"
	"testing"
)

// Ports packages/chord/test/node/plugins/stack.test.ts: `mapStack` names plugin files and lines through each evaluated
// script's source map; nothing global; a script's map goes with its facet. The plugins are built by the real BuildFacet and
// evaluated by Pi's evaluateFacet and mapped by Pi's mapStack in the Node subprocess, read through the probe (the ops of
// plugins_evaluate_test.go's prelude; the GC cases run in a subprocess started with --expose-gc). The import-time throw and
// the getter cases are also read in the LoadReport of a real load (`*NodeError`.Stack, which is `mapStack(error)`).

// stackOps is the ops of the stack tests.
const stackOps = evaluateOpsPrelude + `
	mapped: async (a) => {
		const prepare = Error.prepareStackTrace;
		const facet = await ops.facetOf(a);
		const error = ops.thrownBy(facet);
		return {
			mapped: stack.mapStack(error),
			raw: error.stack,
			prepareKept: Error.prepareStackTrace === prepare,
			plain: stack.mapStack("not an error"),
		};
	},
	released: async (a) => {
		const hostPackages = await ops.packages(a);
		const held = (() => {
			const facet = ops.run(a, hostPackages);
			return { error: ops.thrownBy(facet), ref: new WeakRef(facet) };
		})();
		const before = stack.mapStack(held.error);
		const collected = await ops.collected(held.ref);
		// The finalization runs after the collection.
		for (let round = 0; round < 10 && stack.mapStack(held.error).includes("notes/helper.ts"); round++) await ops.settle();
		return { before, collected, after: stack.mapStack(held.error) };
	},
	failed: async (a) => {
		const hostPackages = await ops.packages(a);
		let failure;
		try {
			ops.run(a, hostPackages);
		} catch (error) {
			failure = error;
		}
		return stack.mapStack(failure);
	},
	shared: async (a) => {
		const hostPackages = await ops.packages(a);
		const first = ops.run(a, hostPackages);
		const second = (() => new WeakRef(ops.run(a, hostPackages)))();
		const collected = await ops.collected(second);
		for (let round = 0; round < 5; round++) await ops.settle();
		return { collected, mapped: stack.mapStack(ops.thrownBy(first)) };
	},
`

// stackFailing is stack.test.ts's `failing()`: a plugin `notes` whose worker facet's `fail` throws from `helper.ts` line 3,
// built with HOST_PACKAGES.
func stackFailing(t *testing.T) PluginScript {
	t.Helper()
	root := tempRoot(t)
	writePlugin(t, root, "notes", "@test/notes", map[string]string{
		"worker.ts": "import { helper } from \"./helper.ts\";\nexport default { setup: () => {}, fail: () => helper() };",
		"helper.ts": "// A helper.\nexport function helper(): never {\n\tthrow new Error(\"helper failed at \" + Date.now());\n}",
	}, nil)
	return evaluateBuilt(t, root, "@test/notes", "worker", []string{"@earendil-works/chord"})
}

// stackSingle writes a plugin dir (name "@test/<dir>") whose worker.ts is text and builds it with no host packages.
func stackSingle(t *testing.T, dir, text string) PluginScript {
	t.Helper()
	root := tempRoot(t)
	writePlugin(t, root, dir, "@test/"+dir, map[string]string{"worker.ts": text}, nil)
	return evaluateBuilt(t, root, "@test/"+dir, "worker", nil)
}

// stack.test.ts › mapStack › maps a plugin script's positions to the plugin's files and lines; other lines stay; nothing
// global (read through the probe).
func TestStackMapsAPluginScriptsPositionsToThePluginsFilesAndLines(t *testing.T) {
	script := stackFailing(t)
	p := evaluateProbe(t, stackOps)
	got := asMap(t, p.must("mapped", evaluateArgs(t, script, "chord")))
	mapped, _ := got["mapped"].(string)
	if !regexp.MustCompile(`at \S+ \(notes/helper\.ts:3:8\)`).MatchString(mapped) {
		t.Errorf("mapped = %q, want notes/helper.ts:3:8 as a frame", mapped)
	}
	if !regexp.MustCompile(`notes/worker\.ts:2:\d+`).MatchString(mapped) {
		t.Errorf("mapped = %q, want notes/worker.ts:2", mapped)
	}
	if strings.Contains(mapped, "/chord-plugins/") {
		t.Errorf("mapped = %q, still names /chord-plugins/", mapped)
	}
	// The raw stack is untouched: no `Error.prepareStackTrace` of the component's.
	if raw, _ := got["raw"].(string); !strings.Contains(raw, "/chord-plugins/@test/notes/worker.ts?") {
		t.Errorf("raw = %q", raw)
	}
	if got["prepareKept"] != true {
		t.Error("Error.prepareStackTrace changed")
	}
	if got["plain"] != "not an error" {
		t.Errorf("mapStack(\"not an error\") = %v", got["plain"])
	}
}

// stack.test.ts › mapStack › lets a script's source map go with its facet (read through the probe, started with --expose-gc).
func TestStackLetsAScriptsSourceMapGoWithItsFacet(t *testing.T) {
	script := stackFailing(t)
	p := evaluateProbe(t, stackOps, "--expose-gc")
	got := asMap(t, p.must("released", evaluateArgs(t, script, "chord")))
	if before, _ := got["before"].(string); !strings.Contains(before, "notes/helper.ts:3:8") {
		t.Errorf("before = %q", before)
	}
	if got["collected"] != true {
		t.Error("the facet was not collected")
	}
	if after, _ := got["after"].(string); !strings.Contains(after, "/chord-plugins/@test/notes/worker.ts?") {
		t.Errorf("after = %q, want the raw filename once the map is gone", after)
	}
}

// stack.test.ts › mapStack, fix round 1 › maps an import-time throw (the map is registered before the script runs) (Sol)
// (read through the probe, and in the LoadReport's *NodeError.Stack).
func TestStackMapsAnImportTimeThrow(t *testing.T) {
	script := stackSingle(t, "evaluation", "// An import-time throw.\n\nthrow new Error(\"at import\");\nexport default { setup: () => {} };\n")
	p := evaluateProbe(t, stackOps)
	if got, _ := p.must("failed", evaluateArgs(t, script, "none")).(string); !strings.Contains(got, "evaluation/worker.ts:3:") {
		t.Errorf("mapStack(failure) = %q", got)
	}
	if err := evaluateFailure(t, evaluateReport(t, "worker", script), "@test/evaluation"); !strings.Contains(err.Stack, "evaluation/worker.ts:3:") {
		t.Errorf("reported stack = %q", err.Stack)
	}
}

// stack.test.ts › mapStack, fix round 1 › maps a throw while its default export is read (a getter) (Sol) (read through the
// probe, and in the LoadReport's *NodeError.Stack).
func TestStackMapsAThrowWhileItsDefaultExportIsRead(t *testing.T) {
	script := stackSingle(t, "getter", "// A getter failure.\nexport default {\n\tget setup() { throw new Error(\"getter boom\"); } };\n")
	p := evaluateProbe(t, stackOps)
	if got, _ := p.must("failed", evaluateArgs(t, script, "none")).(string); !strings.Contains(got, "getter/worker.ts:3:") {
		t.Errorf("mapStack(failure) = %q", got)
	}
	if err := evaluateFailure(t, evaluateReport(t, "worker", script), "@test/getter"); !strings.Contains(err.Stack, "getter/worker.ts:3:") {
		t.Errorf("reported stack = %q", err.Stack)
	}
}

// stack.test.ts › mapStack, fix round 1 › keeps a script's map while any evaluation of it lives: two hosts share it (Sol,
// Opus N3) (read through the probe, started with --expose-gc).
func TestStackKeepsAScriptsMapWhileAnyEvaluationOfItLives(t *testing.T) {
	script := stackFailing(t)
	p := evaluateProbe(t, stackOps, "--expose-gc")
	got := asMap(t, p.must("shared", evaluateArgs(t, script, "chord")))
	if got["collected"] != true {
		t.Error("the second evaluation was not collected")
	}
	if mapped, _ := got["mapped"].(string); !strings.Contains(mapped, "notes/helper.ts:3:8") {
		t.Errorf("mapped = %q, want the first evaluation's error mapped", mapped)
	}
}
