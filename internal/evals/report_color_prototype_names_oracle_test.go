package evals

import (
	"runtime"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// D645, the TERM row. Node's getColorDepth looks the lowercased TERM up in the plain object TERM_ENVS (lib/internal/tty.js), so for TERM=constructor and
// TERM=__proto__ it returns a member inherited from Object.prototype as the depth, which is not above two colors, and never reaches COLORTERM. PiG's table is a
// Go map: such a TERM is unknown and the depth is the one COLORTERM gives. Each case holds Node's answer, which the Node of this run must give, and PiG's; the
// last three are ordinary values and must be equal.
func TestATermNamedLikeAnObjectPrototypeMemberHasTheDefaultColorDepth(t *testing.T) {
	cases := []struct {
		env       map[string]string
		node, pig bool
	}{
		{map[string]string{"TERM": "constructor", "COLORTERM": "x"}, false, true},
		{map[string]string{"TERM": "__proto__", "COLORTERM": "x"}, false, true},
		{map[string]string{"TERM": "Constructor", "COLORTERM": "x"}, false, true},
		{map[string]string{"TERM": "constructor"}, false, false},
		{map[string]string{"TERM": "__proto__"}, false, false},
		{map[string]string{"TERM": "nosuchterm", "COLORTERM": "x"}, true, true},
		{map[string]string{"TERM": "nosuchterm"}, false, false},
		{map[string]string{"TERM": "xterm"}, true, true},
	}
	envs := make([]map[string]string, len(cases))
	for i, c := range cases {
		envs[i] = c.env
	}
	var node []bool
	pioracle.Run(t, `
const tty = await import("node:tty");
emit(input.map((env) => tty.WriteStream.prototype.getColorDepth.call({}, env) > 2));`, envs, &node)
	if len(node) != len(cases) {
		t.Fatalf("Node answered %d of %d environments", len(node), len(cases))
	}
	for i, c := range cases {
		lookup := func(name string) (string, bool) {
			value, ok := c.env[name]
			return value, ok
		}
		if runtime.GOOS == "windows" {
			// Node's getColorDepth takes the win32 branch before consulting TERM_ENVS,
			// returning >= 16 colors on every supported Windows release; verify PiG's windows branch matches live Node.
			if wantWin := nodeColorDepthAboveTwo(lookup, "windows"); node[i] != wantWin {
				t.Errorf("getColorDepth(%v) > 2 on windows: Node answers %v, PiG answers %v", c.env, node[i], wantWin)
			}
		} else if node[i] != c.node {
			t.Errorf("getColorDepth(%v) > 2: Node answers %v, the record says %v", c.env, node[i], c.node)
		}
		if got := nodeColorDepthAboveTwo(lookup, "linux"); got != c.pig {
			t.Errorf("nodeColorDepthAboveTwo(%v) = %v, want %v", c.env, got, c.pig)
		}
	}
}
