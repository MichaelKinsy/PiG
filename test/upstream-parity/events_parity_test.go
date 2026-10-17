package parity

import (
	"reflect"
	"strings"
	"testing"
)

// TestEventTypes_AllUpstreamEventTypesExistInGo asserts that every
// `export interface XxxEvent` in upstream's types.ts has a corresponding Go
// type registered in eventTypeRegistry.
func TestEventTypes_AllUpstreamEventTypesExistInGo(t *testing.T) {
	s, err := LoadUpstream()
	if err != nil {
		t.Fatalf("upstream mirror missing: %v", err)
	}
	for _, decl := range s.EventTypes {
		if _, ok := eventTypeRegistry[decl.Name]; !ok {
			gap(t, "event:"+decl.Name, "upstream event type %s has no entry in "+
				"eventTypeRegistry (add to test/upstream-parity/registry.go "+
				"with the matching extension.<Name> reflect.Type)", decl.Name)
		}
	}
}

// TestEventResults_AllUpstreamEventResultsExistInGo is the same gate for
// `XxxEventResult` interfaces.
func TestEventResults_AllUpstreamEventResultsExistInGo(t *testing.T) {
	s, err := LoadUpstream()
	if err != nil {
		t.Fatalf("upstream mirror missing: %v", err)
	}
	for _, decl := range s.EventResults {
		if _, ok := eventTypeRegistry[decl.Name]; !ok {
			gap(t, "event:"+decl.Name, "upstream event result type %s has no entry in "+
				"eventTypeRegistry", decl.Name)
		}
	}
}

// TestEventFields_NoLegacyInitialismCasing is a drift-prevention
// gate: it scans every registered Go event type and rejects exported field
// names that end in `Id`, `Url`, `Api`, or `Json` (the literal initialism
// without uplift). This catches future workers writing `MessageId` instead
// of `MessageID`.
//
// If a future field genuinely needs to end in one of these (e.g. a domain
// term that happens to spell "Lid"), add an explicit allow-list entry here
// with a comment. Today the allow-list is empty.
func TestEventFields_NoLegacyInitialismCasing(t *testing.T) {
	allowList := map[string]map[string]bool{
		// "TypeName": {"FieldName": true},
	}

	bad := []string{"Id", "Url", "Api", "Json"}
	for typeName, goType := range eventTypeRegistry {
		if goType.Kind() != reflect.Struct {
			continue
		}
		for f := range goType.Fields() {
			if !f.IsExported() {
				continue
			}
			for _, suffix := range bad {
				if strings.HasSuffix(f.Name, suffix) {
					if allowList[typeName][f.Name] {
						continue
					}
					t.Errorf("extension.%s.%s ends in %q (legacy casing); "+
						"use idiomatic Go: %sID/%sURL/%sAPI/%sJSON. "+
						"Helper: parity.CamelToGoField",
						typeName, f.Name, suffix,
						strings.TrimSuffix(f.Name, suffix),
						strings.TrimSuffix(f.Name, suffix),
						strings.TrimSuffix(f.Name, suffix),
						strings.TrimSuffix(f.Name, suffix))
				}
			}
		}
	}
}
