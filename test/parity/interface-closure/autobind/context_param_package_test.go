package main

import "testing"

// chord's Context (packages/chord/src/types.ts:15) is imported by every module of chord, durable, server and env, not only their roots:
// packages/durable/src/env/index.ts:1 `import type { Context } from "@earendil-works/chord"` and `absolutePath(path, context: Context)` at :178.
func TestContextParamAppliesToSubpackagesOfTheChordFamily(t *testing.T) {
	for pkg, want := range map[string]bool{
		"chord": true, "chord/context": true, "durable": true, "durable/env": true, "durable/env/node": true, "durable/storage/jsonl": true,
		"server": true, "server/routing": true, "env": true, "ai": false, "ai/compat": false, "coding-agent": false, "tui": false,
	} {
		chordKind, otherKind := "chord", "other"
		imports := func(from string) map[string]*string {
			kind := from
			return map[string]*string{kind: &kind}
		}
		tab := aliasTable{ctxImportKey + "durable": imports("chord"), ctxImportKey + "server": imports("chord"), ctxImportKey + "env": imports("chord"),
			ctxImportKey + "ai": imports("other"), ctxImportKey + "coding-agent": {"chord": &chordKind, "other": &otherKind}}
		c := &checker{pkg: pkg, aliases: tab}
		got := c.env().SignalBag != nil && c.env().SignalBag("Context")
		if got != want {
			t.Errorf("package %q: SignalBag(Context) = %v, want %v", pkg, got, want)
		}
	}
}

// S1e: a coding-agent handler receives its extension context as the last argument (packages/coding-agent/src/core/extensions/types.ts,
// ExtensionHandler `(event, ctx: ExtensionContext)`), which Go carries in context.Context (extension.ContextFromContext). The same
// type names in other packages are not an invocation context.
func TestExtensionContextParamIsTheGoContext(t *testing.T) {
	for _, tc := range []struct {
		pkg, typ string
		want     bool
	}{
		{"coding-agent", "ExtensionContext", true},
		{"coding-agent", "ExtensionCommandContext", true},
		{"coding-agent", "ExtensionToolContext", true},
		{"coding-agent", "ExtensionUIContext", false},
		{"tui", "ExtensionContext", false},
		{"ai", "ExtensionContext", false},
	} {
		c := &checker{pkg: tc.pkg}
		if got := c.env().SignalBag(tc.typ); got != tc.want {
			t.Errorf("package %q: SignalBag(%s) = %v, want %v", tc.pkg, tc.typ, got, tc.want)
		}
	}
}
