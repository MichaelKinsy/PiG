package extension

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// D645, the MCP tool exposure row. Pi's getMcpToolExposure reads `overrides[toolName]` on the server's plain `toolExposure` object, or on `{}` when the
// server has none (packages/coding-agent/src/core/mcp-servers.ts:171-173), so a tool named like a member of Object.prototype gets the inherited function
// or object where an exposure string is promised. PiG's overrides hold own entries only: such a tool gets the server's exposure, like any other tool
// without an entry. Each case holds Pi's answer, which the pinned Pi must give in this run, and PiG's; the tool `read` is the ordinary name and must be
// equal.
func TestAnMcpToolNamedLikeAnObjectPrototypeMemberGetsTheServersExposure(t *testing.T) {
	type probe struct {
		Config json.RawMessage `json:"config"`
		Tool   string          `json:"tool"`
	}
	servers := []struct {
		pi     string
		config McpServerConfig
	}{
		{`{"command":"x"}`, McpServerConfig{}},
		{`{"command":"x","exposure":"direct","toolExposure":{}}`, McpServerConfig{Exposure: McpExposure("direct")}},
	}
	cases := []struct {
		tool string
		pi   string
		pig  [2]string
	}{
		{"constructor", "function Object", [2]string{`"codemode"`, `"direct"`}},
		{"toString", "function toString", [2]string{`"codemode"`, `"direct"`}},
		{"__proto__", "object {}", [2]string{`"codemode"`, `"direct"`}},
		{"read", "", [2]string{`"codemode"`, `"direct"`}},
	}
	var probes []probe
	for _, c := range cases {
		for _, server := range servers {
			probes = append(probes, probe{Config: json.RawMessage(server.pi), Tool: c.tool})
		}
	}
	var pi []string
	pioracle.Run(t, `
const { getMcpToolExposure } = await load("pi-coding-agent/core/mcp-servers.js");
emit(input.map(({ config, tool }) => {
	const value = getMcpToolExposure(config, tool);
	if (typeof value === "function") return "function " + value.name;
	return typeof value === "string" ? JSON.stringify(value) : typeof value + " " + JSON.stringify(value);
}));`, probes, &pi)
	if len(pi) != len(probes) {
		t.Fatalf("Pi answered %d of %d probes", len(pi), len(probes))
	}
	for i, c := range cases {
		for j, server := range servers {
			answer := pi[i*len(servers)+j]
			encoded, err := json.Marshal(GetMcpToolExposure(server.config, c.tool))
			if err != nil {
				t.Fatal(err)
			}
			pig := string(encoded)
			t.Logf("getMcpToolExposure(%s, %q): Pi %s, PiG %s", server.pi, c.tool, answer, pig)
			wantPi := c.pi
			if wantPi == "" {
				wantPi = c.pig[j]
			}
			if answer != wantPi {
				t.Errorf("getMcpToolExposure(%s, %q): Pi answers %s, the record says %s", server.pi, c.tool, answer, wantPi)
			}
			if pig != c.pig[j] {
				t.Errorf("GetMcpToolExposure(%s, %q) = %s, want %s", server.pi, c.tool, pig, c.pig[j])
			}
		}
	}
}
