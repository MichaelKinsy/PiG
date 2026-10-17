package codingagent

import (
	"encoding/json"
	"math"
	"math/rand"
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// pi: packages/coding-agent/src/core/http-dispatcher.ts

// parseHttpIdleTimeoutMs over JSON values a settings file can hold (numbers, strings in every numeric spelling Number() accepts, null, booleans, containers) and
// formatHttpIdleTimeoutMs over timeouts. Pi converts a string with Number(trimmed), which reads hex, octal and binary literals, exponents, Infinity and
// padding of JS whitespace that Go's ParseFloat does not (and reads Go's "inf", "nan" and "0x1p4" differently).
func TestHTTPIdleTimeoutMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewSource(251))
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	var values []json.RawMessage
	number := func() string {
		return pick("0", "-0", "1", "30000", "299999.9", "0.5", "1e3", "1.5e3", "1E2", "-1", "-0.5", "12345678901", "9007199254740993", "1e21", "1e308", "5e-324", "0.0000001", "2147483648", "4294967296", "5e18", "9223372036854774784", "9223372036854775808")
	}
	for range 1500 {
		switch rng.Intn(5) {
		case 0:
			values = append(values, json.RawMessage(number()))
		case 1:
			values = append(values, json.RawMessage(pick("null", "true", "false", "[]", "[5]", `["7"]`, "{}", `{"a":1}`)))
		default:
			text := pick(number(), "0x10", "0X1F", "0b101", "0B11", "0o17", "0O7", "+5", "-5", ".5", "5.", "1_000", "1e3", "1e", "e3", "Infinity", "-Infinity", "+Infinity", "NaN", "infinity", "inf", "nan",
				"disabled", "DISABLED", "Disabled", "dİsabled", "diſabled", "DIſABLED", "disabled ", "  disabled", "ſ", "", " ", "0x", "0x1p4", "1,000", "١٢٣", "1 2", "5s", "30 sec", "0.0", "00012", "1e1000", "-1e1000", "0x1G", "1.5.2", "+-1", "--1", "1e+3", "1e-3")
			if rng.Intn(3) == 0 {
				pad := pick(" ", "\t", "\n", "\u00a0", "\ufeff", "\u2028", "\u3000", "\u0085", "\u200b", "\v", "\f")
				text = pad + text + pad
			}
			encoded, _ := json.Marshal(text)
			values = append(values, encoded)
		}
	}
	timeouts := []int{0, 1, 30000, 60000, 120000, 300000, 999, 1500, 2500, 45000, 100, 1234567, 86400000, 7, 500000, 5_000_000_000_000_000_000, 9_223_372_036_854_774_784}
	var want struct {
		Parsed []*float64 `json:"parsed"`
		Format []string   `json:"format"`
	}
	pioracle.Run(t, `
const { parseHttpIdleTimeoutMs, formatHttpIdleTimeoutMs } = await load("pi-coding-agent/core/http-dispatcher.js");
emit({ parsed: input.values.map((value) => parseHttpIdleTimeoutMs(value) ?? null), format: input.timeouts.map((ms) => formatHttpIdleTimeoutMs(ms)) });`, map[string]any{"values": values, "timeouts": timeouts}, &want)
	failures := 0
	for i, raw := range values {
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			t.Fatal(err)
		}
		got, ok := parseHTTPIdleTimeoutMs(value)
		if want.Parsed[i] != nil && *want.Parsed[i] >= 1<<63 {
			// Go's int cannot hold a double that large; PiG saturates where Pi keeps the number.
			if !ok || got != math.MaxInt {
				t.Errorf("%s: PiG (%d, %v), Pi %g: want the saturated value", raw, got, ok, *want.Parsed[i])
			}
			continue
		}
		switch {
		case want.Parsed[i] == nil && ok, want.Parsed[i] != nil && !ok, ok && float64(got) != *want.Parsed[i]:
			if failures++; failures <= 12 {
				expected := "undefined"
				if want.Parsed[i] != nil {
					expected = strconv.FormatFloat(*want.Parsed[i], 'g', -1, 64)
				}
				t.Errorf("%s: PiG (%d, %v), Pi %s", raw, got, ok, expected)
			}
		}
	}
	for i, ms := range timeouts {
		if got := formatHTTPIdleTimeoutMs(ms); got != want.Format[i] {
			t.Errorf("format %d: PiG %q, Pi %q", ms, got, want.Format[i])
		}
	}
	if failures > 0 {
		t.Errorf("%d of %d values differ", failures, len(values))
	}
}
