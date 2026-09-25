// This file checks the public Go Context against upstream ExtensionContext.
// Every member must be ported, deferred, translated, or explicitly additive.

package parity

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// upstreamContextMembers lists the pinned upstream ExtensionContext surface.
var upstreamContextMembers = []string{
	"ui",                 // types.ts:295: ExtensionUIContext
	"mode",               // types.ts:304: ExtensionMode
	"hasUI",              // types.ts:297
	"cwd",                // types.ts:299
	"sessionManager",     // types.ts:301: ReadonlySessionManager
	"modelRegistry",      // types.ts:303: ModelRegistry
	"model",              // types.ts:305: Model<any> | undefined
	"isIdle",             // types.ts:307: () => boolean
	"signal",             // handler context.Context carries cancellation
	"abort",              // types.ts:311: () => void
	"hasPendingMessages", // types.ts:313: () => boolean
	"shutdown",           // types.ts:315: () => void
	"getContextUsage",    // types.ts:317: () => ContextUsage | undefined
	"compact",            // types.ts:319: (options?: CompactOptions) => void
	"getSystemPrompt",    // types.ts:321: () => string
	"isProjectTrusted",   // types.ts: () => boolean
	"scopedModels",       // types.ts: readonly ScopedModel[]
	"thinkingLevel",      // types.ts: ThinkingLevel | undefined
}

// upstreamToContextGoName converts a camelCase upstream member name to
// the Go method/field name on `extension.Context`.
func upstreamToContextGoName(camel string) string {
	overrides := map[string]string{
		"ui":                 "UI",
		"hasUI":              "HasUI",
		"cwd":                "CWD",
		"sessionManager":     "SessionManager",
		"modelRegistry":      "ModelRegistry",
		"model":              "Model",
		"isIdle":             "IsIdle",
		"abort":              "Abort",
		"hasPendingMessages": "HasPendingMessages",
		"shutdown":           "Shutdown",
		"getContextUsage":    "GetContextUsage",
		"compact":            "Compact",
		"getSystemPrompt":    "GetSystemPrompt",
		// signal is carried by the handler context.Context.
	}
	if v, ok := overrides[camel]; ok {
		return v
	}
	return CamelToPascal(camel)
}

// deferredContextMembers records upstream members not yet on Go Context.
var deferredContextMembers = map[string]string{
	"scopedModels":  "unported: no extension.Context counterpart for readonly ScopedModel[]",
	"thinkingLevel": "unported on extension.Context; subprocess SDKs expose it via getThinkingLevel",
}

// translatedContextMembers records upstream members represented outside the
// public Go Context type.
var translatedContextMembers = map[string]string{
	"signal": "handler context.Context carries cancellation",
}

// pigOnlyContextMembers records Go Context members without a direct upstream
// Context member.
var pigOnlyContextMembers = map[string]string{
	// D23: Piglet-scoped active-tool Context API.
	// Upstream has no piglet-driven tool scoping. These methods enable the
	// piglet extension (D18) to read flags, inspect all tools with source
	// metadata, and apply per-source scoping via SetActiveTools.
	"GetFlagValue":   "D23",
	"GetAllTools":    "D23",
	"GetActiveTools": "D23",
	"SetActiveTools": "D23",

	"SendUserMessage": "upstream ExtensionAPI method placed on Go handler Context",
}

func goContextPublicMembers() map[string]bool {
	t := reflect.TypeFor[*extension.Context]()
	out := map[string]bool{}
	// Methods on *Context.
	for method := range t.Methods() {
		out[method.Name] = true
	}
	// Exported fields on Context (none today: assertActive/cwd/hasUI
	// are unexported: but if a future row adds an exported field, the
	// gate must see it).
	if t.Kind() == reflect.Pointer {
		st := t.Elem()
		for f := range st.Fields() {
			if f.IsExported() {
				out[f.Name] = true
			}
		}
	}
	return out
}

// TestContext_AllUpstreamMembersAccounted requires each upstream member to be
// ported, deferred, or translated into another Go surface.
func TestContext_AllUpstreamMembersAccounted(t *testing.T) {
	goMembers := goContextPublicMembers()

	for _, upstreamName := range upstreamContextMembers {
		if reason, translated := translatedContextMembers[upstreamName]; translated {
			t.Logf("upstream ExtensionContext.%s translated: %s", upstreamName, reason)
			continue
		}

		goName := upstreamToContextGoName(upstreamName)

		// Bucket 1: ported.
		if goName != "" && goMembers[goName] {
			continue
		}

		// Bucket 2: deferred.
		if reason, deferred := deferredContextMembers[upstreamName]; deferred {
			t.Logf("upstream ExtensionContext.%s deferred: %s", upstreamName, reason)
			continue
		}

		t.Errorf("upstream ExtensionContext.%s has no extension.Context.%s and no deferred or translated disposition", upstreamName, goName)
	}
}

// TestContext_NoUnclassifiedPiGMembers rejects unclassified Go members.
func TestContext_NoUnclassifiedPiGMembers(t *testing.T) {
	expected := map[string]bool{}
	for _, upstreamName := range upstreamContextMembers {
		if _, translated := translatedContextMembers[upstreamName]; translated {
			continue
		}
		if goName := upstreamToContextGoName(upstreamName); goName != "" {
			expected[goName] = true
		}
	}

	goMembers := goContextPublicMembers()
	for goName := range goMembers {
		if expected[goName] {
			continue
		}
		if div, documented := pigOnlyContextMembers[goName]; documented {
			t.Logf("extension.Context.%s → documented pig-only member (%s)", goName, div)
			continue
		}

		t.Errorf("extension.Context.%s has no upstream counterpart "+
			"and is not in pigOnlyContextMembers. "+
			"Either remove it (preferred: keeps sync surface minimal), "+
			"or add it to pigOnlyContextMembers with a numbered DIVERGENCES.md entry.",
			goName)
	}
}

// TestContext_DeferredMembersHaveValidPhaseRows rejects stale deferrals.
func TestContext_DeferredMembersHaveValidPhaseRows(t *testing.T) {
	goMembers := goContextPublicMembers()
	for upstreamName, row := range deferredContextMembers {
		goName := upstreamToContextGoName(upstreamName)
		if goName == "" {
			continue
		}
		if goMembers[goName] {
			t.Errorf("extension.Context.%s exists but is still in deferredContextMembers "+
				"(row %q). Remove the entry from deferredContextMembers.",
				goName, row)
		}
	}
}

// TestContext_UpstreamMemberListIsAlphabeticallyDistinct rejects duplicate and
// unknown disposition entries.
func TestContext_UpstreamMemberListIsAlphabeticallyDistinct(t *testing.T) {
	seen := map[string]bool{}
	for _, name := range upstreamContextMembers {
		if seen[name] {
			t.Errorf("upstreamContextMembers contains duplicate %q", name)
		}
		seen[name] = true
	}

	upstreamSet := map[string]bool{}
	for _, name := range upstreamContextMembers {
		upstreamSet[name] = true
	}
	for name := range deferredContextMembers {
		if !upstreamSet[name] {
			t.Errorf("deferredContextMembers references %q which is not in upstreamContextMembers",
				name)
		}
	}
	for name := range translatedContextMembers {
		if !upstreamSet[name] {
			t.Errorf("translatedContextMembers references %q which is not in upstreamContextMembers", name)
		}
	}

	for name := range deferredContextMembers {
		if _, alsoTranslated := translatedContextMembers[name]; alsoTranslated {
			t.Errorf("%q is both deferred and translated", name)
		}
	}
}

// TestContextParityGate_DivergenceRefsResolve validates only values that cite a
// numbered ledger record.
func TestContextParityGate_DivergenceRefsResolve(t *testing.T) {
	known := loadAllLedgerNumbers(t)

	for member, ref := range pigOnlyContextMembers {
		if divergenceRefRE.MatchString(ref) && !known[ref] {
			t.Errorf("pigOnlyContextMembers[%q] = %q, but no `## %s` section exists in DIVERGENCES.md or ADDITIVE_FEATURES.md",
				member, ref, ref)
		}
	}
	for member, ref := range translatedContextMembers {
		if divergenceRefRE.MatchString(ref) && !known[ref] {
			t.Errorf("translatedContextMembers[%q] = %q, but no `## %s` ledger record exists", member, ref, ref)
		}
	}
}
