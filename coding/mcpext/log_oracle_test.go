package mcpext

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// formatMcpLogMessage against Pi's log.js: params of every JSON shape (object, array, scalar, null, absent), level and logger of the wrong type, data of every JSON type with line breaks, repeated members,
// escapes, non-ASCII and unusual numbers.
func TestFormatMcpLogMessageMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewSource(33))
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	var scalar func() string
	var value func(depth int) string
	scalar = func() string {
		return pick(`"plain"`, `""`, `"a\nb"`, `"a\r\nb\r\n"`, `"a\rb"`, `"\u2028x\u2029"`, `"tab\there"`, `"日本語 😀"`, `"\ud83d"`, `"quote \" and \\"`, `"\u0000\u001f"`, `"  padded  "`,
			`0`, `-0`, `1`, `1.5`, `1e21`, `1E-7`, `123456789012345678901234567890`, `0.1`, `-1e-7`, `1.0`, `100`, `true`, `false`, `null`)
	}
	value = func(depth int) string {
		if depth <= 0 || rng.Intn(3) == 0 {
			return scalar()
		}
		if rng.Intn(2) == 0 {
			var items []string
			for range rng.Intn(4) {
				items = append(items, value(depth-1))
			}
			return "[" + strings.Join(items, ",") + "]"
		}
		var members []string
		for range rng.Intn(4) {
			members = append(members, fmt.Sprintf("%s:%s", pick(`"a"`, `"b"`, `"a\nb"`, `"10"`, `"2"`, `"__proto__"`, `"日本"`, `""`), value(depth-1)))
		}
		return "{" + strings.Join(members, ",") + "}"
	}
	type probe struct {
		Server string `json:"server"`
		Params string `json:"params"`
		Absent bool   `json:"absent"`
		Millis int64  `json:"millis"`
	}
	var probes []probe
	for range 1500 {
		p := probe{Server: pick("srv", "my server", "日本", "a]b", ""), Millis: rng.Int63n(253402300799999)}
		switch rng.Intn(8) {
		case 0:
			p.Absent = true
		case 1:
			p.Params = value(2)
		default:
			var members []string
			if rng.Intn(4) != 0 {
				members = append(members, `"level":`+pick(`"error"`, `"warning"`, `"info"`, `5`, `null`, `"日本"`, `""`, `{}`))
			}
			if rng.Intn(3) != 0 {
				members = append(members, `"logger":`+pick(`"db"`, `""`, `7`, `null`, `"a b"`, `"x\ny"`))
			}
			if rng.Intn(5) != 0 {
				members = append(members, `"data":`+value(3))
			}
			if rng.Intn(6) == 0 {
				members = append(members, `"data":`+value(2))
			}
			if rng.Intn(5) == 0 {
				members = append(members, `"extra":`+value(1))
			}
			rng.Shuffle(len(members), func(i, j int) { members[i], members[j] = members[j], members[i] })
			p.Params = "{" + strings.Join(members, ",") + "}"
		}
		probes = append(probes, p)
	}
	var want []string
	pioracle.Run(t, `
const m = await load("pi-coding-agent/extensions/mcp/log.js");
emit(input.map((p) => m.formatMcpLogMessage(p.server, p.absent ? undefined : JSON.parse(p.params), new Date(p.millis))));
`, probes, &want)
	bad := 0
	for i, p := range probes {
		var params json.RawMessage
		if !p.Absent {
			params = json.RawMessage(p.Params)
		}
		got := FormatMcpLogMessage(p.Server, params, time.UnixMilli(p.Millis))
		if got != want[i] {
			if bad++; bad <= 8 {
				t.Errorf("params %s (absent=%v):\n got %q\nwant %q", p.Params, p.Absent, got, want[i])
			}
		}
	}
	if bad > 8 {
		t.Errorf("%d mismatches", bad)
	}
}
