package codingagent

import (
	"math/rand"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// Pi's getToolListError, applyToolModifiers and isToolModifier (core/settings-manager.js) run against the vendored Pi module over seeded random tool lists.
func TestToolModifiersMatchPiOracle(t *testing.T) {
	vocabulary := []string{"read", "bash", "edit", "write", "grep", "+read", "+grep", "-read", "-bash", "-write", "+", "-", "+*", "-gr*", "+a*b", "-*x", "mcp__x__*", "", "+codemode", "-codemode", "++a", "+-a", "read*"}
	rng := rand.New(rand.NewSource(11))
	randomList := func() []string {
		list := make([]string, rng.Intn(7))
		for i := range list {
			list[i] = vocabulary[rng.Intn(len(vocabulary))]
		}
		return list
	}
	type pair struct {
		Base    []string `json:"base"`
		Entries []string `json:"entries"`
	}
	type result struct {
		Error   string   `json:"error"`
		Applied []string `json:"applied"`
	}
	var pairs []pair
	for range 600 {
		pairs = append(pairs, pair{randomList(), randomList()})
	}
	var want []result
	pioracle.Run(t, `
const mod = await load("pi-coding-agent/core/settings-manager.js");
emit(input.map((p) => ({ error: mod.getToolListError(p.entries) ?? "", applied: mod.applyToolModifiers(p.base, p.entries) })));`, pairs, &want)
	mismatches := 0
	for i, p := range pairs {
		got := result{Error: GetToolListError(p.Entries), Applied: ApplyToolModifiers(p.Base, p.Entries)}
		if !reflect.DeepEqual(got, want[i]) {
			if mismatches++; mismatches <= 5 {
				t.Errorf("%q / %q: Go %+v, Pi %+v", p.Base, p.Entries, got, want[i])
			}
		}
	}
}
