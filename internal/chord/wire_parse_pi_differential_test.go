package chord

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type wireParseCase struct {
	Kind    string          `json:"kind"`
	Input   json.RawMessage `json:"input"`
	Result  wireOutcome     `json:"result"`
	Control *wireOutcome    `json:"control"`
}

type wireOutcome struct {
	Ok      json.RawMessage `json:"ok"`
	Error   string          `json:"error"`
	Message string          `json:"message"`
}

// canonicalJSON re-encodes a JSON text through chordjson.Decode. Object keys keep their order, so the comparison is order-sensitive; only number spelling and whitespace are normalized.
func canonicalJSON(t *testing.T, raw []byte) string {
	t.Helper()
	value, err := chordjson.Decode(raw)
	if err != nil {
		t.Fatalf("canonical %s: %v", raw, err)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// unorderedJSON re-encodes a JSON text through a Go map, so object keys sort.
func unorderedJSON(t *testing.T, raw []byte) string {
	t.Helper()
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("unordered %s: %v", raw, err)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// Pi packages/chord/src/services/wire.ts:67-248 (decodeServiceControlCall, parseServiceCall, parseServiceCatalogue, the subscription
// snapshot and provider update parsers, plain and wire form) against the installed chord 1.1.0 dist, by testdata/wire_parse_differential.mjs:
// seeded random mutations of valid wire values (replaced, deleted and added fields). Each Go parser must accept exactly what Pi accepts,
// reject the rest with Pi's message, and return the accepted value unchanged; a parsed call must decode to Pi's control call.
func TestServiceWireParsersMatchPiOnRandomCases(t *testing.T) {
	const count, seed = 4000, 20260702
	cmd := exec.CommandContext(t.Context(), "node", "testdata/wire_parse_differential.mjs", pigversion.UpstreamVersion, strconv.Itoa(count), strconv.Itoa(seed))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, stderr.String())
	}
	var cases []wireParseCase
	if err := json.Unmarshal(out, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != count {
		t.Fatalf("Pi oracle returned %d cases, want %d", len(cases), count)
	}
	parse := map[string]func(json.RawMessage) (any, error){
		"call":         func(raw json.RawMessage) (any, error) { return ParseServiceCall(raw) },
		"catalogue":    func(raw json.RawMessage) (any, error) { return ParseServiceCatalogue(raw) },
		"snapshot":     func(raw json.RawMessage) (any, error) { return ParseServiceSubscriptionSnapshot(raw) },
		"wireSnapshot": func(raw json.RawMessage) (any, error) { return ParseWireServiceSubscriptionSnapshot(raw) },
		"update":       func(raw json.RawMessage) (any, error) { return ParseServiceProviderUpdate(raw) },
		"wireUpdate":   func(raw json.RawMessage) (any, error) { return ParseWireServiceProviderUpdate(raw) },
	}
	mismatches := 0
	report := func(index int, c wireParseCase, format string, args ...any) {
		mismatches++
		if mismatches <= 20 {
			t.Errorf("case %d %s %s: "+format, append([]any{index, c.Kind, c.Input}, args...)...)
		}
	}
	for index, c := range cases {
		got, err := parse[c.Kind](c.Input)
		switch {
		case c.Result.Ok == nil:
			if err == nil || err.Error() != c.Result.Message {
				report(index, c, "got error %v, Pi threw %s %q", err, c.Result.Error, c.Result.Message)
			}
			continue
		case err != nil:
			report(index, c, "got error %v, Pi accepted it", err)
			continue
		}
		encoded, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		gotJSON, want := canonicalJSON(t, encoded), canonicalJSON(t, c.Result.Ok)
		if c.Kind == "call" {
			// ServiceCall is a Go struct, so it re-encodes in its field order, which is the order Pi's consumer builds a call in (consumer.ts:218-223 {serviceId, instance?, member, args}); a mutated input's own member order is compared as Jest's toEqual compares it.
			gotJSON, want = unorderedJSON(t, encoded), unorderedJSON(t, c.Result.Ok)
		}
		if gotJSON != want {
			report(index, c, "parsed to %s, Pi returned %s", gotJSON, want)
		}
		if c.Kind != "call" {
			continue
		}
		control, ok := DecodeServiceControlCall(got.(ServiceCall))
		want = "null"
		if c.Control != nil && c.Control.Ok != nil {
			want = canonicalJSON(t, c.Control.Ok)
		}
		gotControl := "null"
		if ok {
			fields := chordjson.ObjectOf("type", string(control.Type))
			if control.SubscriptionId != "" {
				fields.Set("subscriptionId", control.SubscriptionId)
			}
			if control.ServiceId != "" {
				fields.Set("serviceId", control.ServiceId)
			}
			if control.Mode != "" {
				fields.Set("mode", string(control.Mode))
			}
			encoded, _ := json.Marshal(fields)
			gotControl = string(encoded)
		}
		if gotControl != want {
			report(index, c, "decoded control call %s, Pi %s", gotControl, want)
		}
	}
	if mismatches > 0 {
		t.Errorf("%d of %d cases differ from Pi", mismatches, count)
	}
}
