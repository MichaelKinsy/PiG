package main

import (
	"maps"
	"slices"
	"strings"
	"testing"
)

// durable-core measures two builds of the core against the conformance gate's reference in one session: each pig target
// keeps its own tracker rule, the fingerprints are durable-bench's published ones (as durable-native's), and every
// command names a program the bundle carries (core/ from durable/core/host/cf/bench/build.mjs, bin/ on PATH); Node runs
// with --expose-gc so every target reports retained memory.
func TestDurableCoreProfile(t *testing.T) {
	p, err := loadProfile("durable-core")
	if err != nil {
		t.Fatal(err)
	}
	native, err := loadProfile("durable-native")
	if err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(p.Fingerprints, native.Fingerprints) {
		t.Errorf("fingerprints %v, durable-native has %v", p.Fingerprints, native.Fingerprints)
	}
	rules := map[string]string{}
	for _, tg := range p.pigs() {
		rules[tg.Name] = p.ruleOf(tg)
	}
	if !maps.Equal(rules, map[string]string{"pig-tinygo": "core-tinygo", "pig-native": "core-native"}) {
		t.Errorf("pig targets and rules %v", rules)
	}
	if ref, ok := p.byRole(roleReference); !ok || ref.Name != "pi" {
		t.Errorf("reference %+v", ref)
	}
	carried := map[string]bool{"{root}/core/bench-pi.mjs": true, "{root}/core/bench-pig.mjs": true, "sqlhost-bench": true, "node": true}
	for _, tg := range p.Targets {
		for _, argv := range [][]string{tg.Seed, tg.Sample} {
			program := argv[0]
			if program == "node" {
				// The Node programs report retained memory only when they can force a collection.
				if !slices.Contains(argv, "--expose-gc") {
					t.Errorf("%s runs node without --expose-gc: %v", tg.Name, argv)
				}
				program = argv[slices.IndexFunc(argv, func(a string) bool { return strings.HasPrefix(a, "{root}/") })]
			}
			if !carried[program] {
				t.Errorf("%s runs %s, which the bundle does not carry", tg.Name, program)
			}
		}
		if !strings.HasPrefix(tg.SeedMeta, "{dir}/") || !strings.HasPrefix(tg.Sample[len(tg.Sample)-3], "{dir}/") {
			t.Errorf("%s keeps its fixture outside its own directory: %v %v", tg.Name, tg.SeedMeta, tg.Sample)
		}
	}
}
