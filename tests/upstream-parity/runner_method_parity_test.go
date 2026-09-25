// Package parity: Runner method parity gate.
//
// Mirrors api_parity_test.go but for upstream `ExtensionRunner` (a class,
// not an interface: so it isn't picked up by the existing parser, which
// parses interfaces only).
//
// Surfaces drift between upstream Runner and pig Runner. Activated in the
// post-forward-back pass to close Gap #7 (and prevent regressions of
// Gap #2: silent pig-only public methods accreting on Runner).
//
// **What this gate enforces:**
//
//  1. Every public method on upstream `ExtensionRunner` has a matching
//     method on pig `inproc.Runner` (with idiomatic naming) OR is
//     listed in `deferredRunnerMethods` with a row reference.
//
//  2. Every public method on pig `inproc.Runner` that has NO upstream
//     counterpart is listed in `pigOnlyRunnerMethods` with the reason it
//     is a legitimate Go SDK-surface addition (not a behavioral divergence).
//
// **Why a hardcoded list and not an AST parse?** Upstream's runner.ts is
// a class with private fields, async methods, generic parameters, getter
// syntax, etc.: a robust parser is significant work. The hardcoded list
// is maintained alongside upstream sync (a checkbox in the sync workflow
// at AGENTS.md "Upstream sync"). When upstream adds or renames a public
// method, the sync worker updates this list AND `inproc.Runner`. The gate
// catches drift at test time.
//
// **Maintenance burden.** ~5 minutes per upstream sync to walk
// `.upstream/current/.../runner.ts`, grep public methods, diff against
// the list below. Acceptable given the cost of NOT having the gate.
package parity

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// upstreamRunnerMethods is the manually-maintained list of public methods
// on upstream `ExtensionRunner` as of the version pinned in
// `cmd/pig/main.go::UpstreamVersion`.
//
// Source: `.upstream/current/packages/coding-agent/src/core/extensions/runner.ts`
//
// To extract: `grep -nE "^	[a-zA-Z]" runner.ts` and exclude lines starting
// with `private`. Methods listed here use upstream camelCase verbatim.
//
// **Last reconciled:** against pinned UpstreamVersion 0.84.0.
//
// Extract from the class body rather than the whole file, which also holds
// interface declarations and control-flow keywords, and allow for the `async`
// prefix that hides every emit* method from a naive match:
//
//	awk 'NR>268 && /^}/{exit} NR>268' runner.ts |
//	  grep -E '^\t(async )?[a-zA-Z_]+[(<]' | grep -vE '^\t(private|static)'
//
// The list stood at 0.69.0 while the pin moved to 0.84.0, so six methods added
// upstream in between were never checked and addErrorListener stayed listed
// after upstream renamed it to onError.
var upstreamRunnerMethods = []string{
	// Lifecycle and binding
	"bindCore",           // runner.ts:261: wires runtime/sessionManager/modelRegistry
	"bindCommandContext", // runner.ts:333: wires command-context handlers
	"setUIContext",       // runner.ts:352
	"getUIContext",       // runner.ts:356
	"hasUI",              // runner.ts:360
	"getExtensionPaths",  // runner.ts:364
	"invalidate",         // runner.ts:461
	"shutdown",           // runner.ts:558
	// Added upstream since the list was last reconciled at 0.69.0.
	"onError",                   // runner.ts:558
	"getMarkdownTransformers",   // runner.ts:589
	"getModelRegistry",          // runner.ts:639
	"getActiveTools",            // runner.ts:664
	"emitMessageEnd",            // runner.ts:835
	"emitBeforeProviderHeaders", // runner.ts:1050

	// Registries
	"getAllRegisteredTools",  // runner.ts:369
	"getToolDefinition",      // runner.ts:382
	"getFlags",               // runner.ts:392
	"setFlagValue",           // runner.ts:404
	"getFlagValues",          // runner.ts:408
	"getShortcuts",           // runner.ts:412
	"getShortcutDiagnostics", // runner.ts:457
	"getRegisteredCommands",  // runner.ts:541
	"getCommand",             // runner.ts:550
	"getCommandDiagnostics",  // runner.ts:546
	"getMessageRenderer",     // runner.ts:495
	"getEntryRenderer",       // runner.ts:585

	// Error listener subsystem
	"addErrorListener", // runner.ts:474
	"emitError",        // runner.ts:479

	// Inspection
	"hasHandlers", // runner.ts:485

	// Context factories
	"createContext",        // runner.ts:566
	"createCommandContext", // runner.ts:629

	// Emit dispatch
	"emit",                      // runner.ts:976
	"emitBoundary",              // runner.ts:927
	"emitToolResult",            // runner.ts:1075
	"emitToolCall",              // runner.ts:757
	"emitUserBash",              // runner.ts:780
	"emitContext",               // runner.ts:809
	"emitBeforeProviderRequest", // runner.ts:841
	"emitBeforeAgentStart",      // runner.ts:875
	"emitResourcesDiscover",     // runner.ts:941
	"emitInput",                 // runner.ts:990
}

// deferredRunnerMethods names upstream methods that are intentionally
// not yet ported, each with the reason it remains deferred.
//
// Removing an entry from this map without adding the corresponding Go method
// on inproc.Runner causes the gate to fail.
var deferredRunnerMethods = map[string]string{
	// Flags read-side ported; setFlagValue/getFlagValues touch
	// runtime.flagValues, which is part of the ExtensionRuntime and not yet wired.
	"setFlagValue":  "deferred: flag write-side not wired on inproc.Runner",
	"getFlagValues": "deferred: flag write-side not wired on inproc.Runner",

	// Added upstream after this list was last reconciled, and unported. The
	// stale denominator, not a decision, is why they were never reported.
	"getMarkdownTransformers": "unported: no markdown-transformer accessor on inproc.Runner",
	"getModelRegistry":        "unported: no model-registry accessor on inproc.Runner",
	"getActiveTools":          "unported: inproc.Runner exposes Tools, not the active-tool subset",
	// Present as AddErrorListener under pig's earlier name; see
	// pigOnlyRunnerMethods. The capability is ported, the spelling is not.
	"onError": "spelling: ported as inproc.Runner.AddErrorListener; rename pending",
}

// pigOnlyRunnerMethods names public methods on inproc.Runner that have
// NO upstream counterpart. Each is a Go SDK-surface addition (a read-only
// accessor over the same wire protocol), not a behavioral divergence: the
// DIVERGENCES.md header excludes language-surface differences. Each MUST carry
// a rationale here so pig-only methods cannot accrete silently.
var pigOnlyRunnerMethods = map[string]string{
	"IsStale":        "SDK-surface: read-only staleness accessor (test/lifecycle seam)",
	"StaleMessage":   "SDK-surface: read-only staleness accessor (test/lifecycle seam)",
	"ExtensionCount": "SDK-surface: read-only count accessor (avoids exposing the slice)",
	"ExtensionNames": "SDK-surface: read-only names accessor (avoids exposing the slice)",
	"ExecuteCommand": "SDK-surface: active-runner command bridge for AgentSession/RPC invocation parity",
	// Upstream's withUIPrompt is private because every ctx.ui call passes
	// through the runner's wrapped UI context. Pig's subprocess dialogs reach
	// the terminal through the subprocess UI bridge, which opens the same
	// runner-owned scope.
	"BeginUIPrompt": "upstream private withUIPrompt, exposed to the subprocess UI bridge",
	// Upstream renamed addErrorListener to onError. Pig keeps the older name,
	// so the method is present on both sides under different spellings rather
	// than absent; renaming it is a public-surface change to sequence
	// deliberately, not to fold into a list reconciliation.
	"AddErrorListener": "upstream onError under pig's earlier name; rename pending",
	// Upstream emitContext runs the context phase then the context_with_system
	// phase. Pig's Session splits the system messages out around the context
	// phase (typed agent messages), so the second phase is its own entry point.
	"EmitContextWithSystem": "SDK-surface: second phase of upstream emitContext (context_with_system)",
	"EmitContextTracked":    "SDK-surface: upstream emitContext conversation phase plus its sameMessages identity verdict, which restoreSystemMessages consumes (REFNL-003)",
}

// upstreamToGoMethodName converts a camelCase upstream method name to the
// idiomatic Go PascalCase.
func upstreamToGoMethodName(camel string) string {
	// Upstream's `getXxx` accessor pattern → Go's idiomatic field-style
	// name (drop the "get" prefix). E.g. getAllRegisteredTools → Tools,
	// getToolDefinition → GetToolDefinition (kept "Get" because there's
	// also Tools(); the pair Tools()+GetToolDefinition() is idiomatic Go).
	overrides := map[string]string{
		"getAllRegisteredTools":  "Tools",
		"getToolDefinition":      "GetToolDefinition",
		"getFlags":               "Flags",
		"getShortcuts":           "Shortcuts",
		"getShortcutDiagnostics": "ShortcutDiagnostics",
		"getRegisteredCommands":  "Commands",
		"getCommand":             "Command",
		"getCommandDiagnostics":  "CommandDiagnostics",
		"getMessageRenderer":     "MessageRenderer",
		"getEntryRenderer":       "EntryRenderer",
		"getExtensionPaths":      "ExtensionPaths",
		"addErrorListener":       "AddErrorListener",
		"emitError":              "EmitError",
		"hasHandlers":            "HasHandlers",
		"invalidate":             "Invalidate",
		// Emit family: capitalize the leading "e".
		"emit":                      "Emit",
		"emitBoundary":              "EmitBoundary",
		"emitToolCall":              "EmitToolCall",
		"emitToolResult":            "EmitToolResult",
		"emitUserBash":              "EmitUserBash",
		"emitContext":               "EmitContext",
		"emitBeforeProviderRequest": "EmitBeforeProviderRequest",
		"emitBeforeAgentStart":      "EmitBeforeAgentStart",
		"emitResourcesDiscover":     "EmitResourcesDiscover",
		"emitInput":                 "EmitInput",
		// DispatchContext exposes upstream's private createContext to the Go bridge.
		"createContext": "DispatchContext",
		// upstream bindCommandContext → BindCommandActions (Go naming)
		"bindCommandContext":   "BindCommandActions",
		"createCommandContext": "CreateCommandContext",
		"shutdown":             "Shutdown",
	}
	if v, ok := overrides[camel]; ok {
		return v
	}
	return CamelToPascal(camel)
}

func goRunnerMethods() map[string]bool {
	t := reflect.TypeFor[*inproc.Runner]()
	out := map[string]bool{}
	for method := range t.Methods() {
		out[method.Name] = true
	}
	return out
}

// TestRunner_AllUpstreamMethodsPortedOrDeferred enforces that every public
// method on upstream `ExtensionRunner` either has a matching Go method on
// `inproc.Runner` or is listed in `deferredRunnerMethods` with a reason.
func TestRunner_AllUpstreamMethodsPortedOrDeferred(t *testing.T) {
	goMethods := goRunnerMethods()

	for _, upstreamName := range upstreamRunnerMethods {
		goName := upstreamToGoMethodName(upstreamName)

		if goMethods[goName] {
			continue
		}

		if row, deferred := deferredRunnerMethods[upstreamName]; deferred {
			t.Logf("upstream ExtensionRunner.%s → deferred to %s", upstreamName, row)
			continue
		}

		t.Errorf("upstream ExtensionRunner.%s has no inproc.Runner.%s "+
			"and is not in deferredRunnerMethods. "+
			"Either port it, add it to deferredRunnerMethods with a reason, "+
			"or document a deliberate divergence in DIVERGENCES.md.",
			upstreamName, goName)
	}
}

// TestRunner_NoUnclassifiedPiGMethods rejects public methods without an
// upstream mapping or an explicit PiG disposition.
func TestRunner_NoUnclassifiedPiGMethods(t *testing.T) {
	// Build the set of expected Go method names (those with an upstream
	// counterpart, after name conversion).
	expected := map[string]bool{}
	for _, upstreamName := range upstreamRunnerMethods {
		if goName := upstreamToGoMethodName(upstreamName); goName != "" {
			expected[goName] = true
		}
	}

	goMethods := goRunnerMethods()
	for goName := range goMethods {
		// Defense-in-depth: any *ForTest method should NOT be visible from
		// outside the inproc package. If one shows up here, export_test.go
		// has accidentally promoted a test seam to the public API.
		if hasForTestSuffix(goName) {
			t.Errorf("inproc.Runner.%s is a *ForTest seam visible from outside the inproc package: "+
				"move it to export_test.go (which restricts visibility to inproc_test package).", goName)
			continue
		}
		if expected[goName] {
			continue
		}
		if div, documented := pigOnlyRunnerMethods[goName]; documented {
			t.Logf("inproc.Runner.%s → documented pig-only method (%s)", goName, div)
			continue
		}

		t.Errorf("inproc.Runner.%s has no upstream counterpart "+
			"and is not in pigOnlyRunnerMethods. "+
			"Either remove it (preferred: keeps sync surface minimal), "+
			"or add it to pigOnlyRunnerMethods with an SDK-surface rationale.",
			goName)
	}
}

// hasForTestSuffix returns true for methods whose names end in "ForTest".
// In a healthy build, no such method should be visible from outside the
// inproc package: export_test.go is compiled only into the inproc_test
// package's test binary, NOT into other packages' test binaries. This
// helper exists so the parity gate can flag accidental promotions of a
// seam from export_test.go to a regular .go file.
func hasForTestSuffix(name string) bool {
	const suffix = "ForTest"
	return len(name) > len(suffix) && name[len(name)-len(suffix):] == suffix
}

// TestRunner_DeferredMethodsHaveValidPhaseRows is a soft gate: when a
// future row lands one of the deferred methods, the developer must
// remove it from `deferredRunnerMethods`. This test loudly fails if a
// "deferred" method is actually present on the Go Runner: surfacing
// that the deferral entry is stale and should be deleted.
func TestRunner_DeferredMethodsHaveValidPhaseRows(t *testing.T) {
	goMethods := goRunnerMethods()
	for upstreamName, row := range deferredRunnerMethods {
		goName := upstreamToGoMethodName(upstreamName)
		if goMethods[goName] {
			t.Errorf("inproc.Runner.%s exists but is still in deferredRunnerMethods "+
				"(row %q). Remove the entry from deferredRunnerMethods.",
				goName, row)
		}
	}
}

// TestRunner_UpstreamMethodListIsAlphabeticallyDistinct is a sanity gate:
// the list above must contain no duplicates. If a worker adds the same
// method name twice during a sync, the gate catches it before it produces
// confusing test output.
func TestRunner_UpstreamMethodListIsAlphabeticallyDistinct(t *testing.T) {
	seen := map[string]bool{}
	for _, name := range upstreamRunnerMethods {
		if seen[name] {
			t.Errorf("upstreamRunnerMethods contains duplicate %q", name)
		}
		seen[name] = true
	}
	// Bonus: ensure deferredRunnerMethods only references entries that
	// actually exist in the upstream method list.
	upstreamSet := map[string]bool{}
	for _, name := range upstreamRunnerMethods {
		upstreamSet[name] = true
	}
	for name := range deferredRunnerMethods {
		if !upstreamSet[name] {
			t.Errorf("deferredRunnerMethods references %q which is not in upstreamRunnerMethods",
				name)
		}
	}
}
