package delta

import (
	"bytes"
	"encoding/json"
	"errors"
	"os/exec"
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// piDifferentialOutcome is one apply result: the JSON value, or the error class name and message Pi throws.
type piDifferentialOutcome struct {
	Ok      json.RawMessage `json:"ok,omitempty"`
	Error   string          `json:"error,omitempty"`
	Message string          `json:"message,omitempty"`
}

// piDifferentialCases is the output of testdata/delta_differential.mjs.
type piDifferentialCases struct {
	Apply []struct {
		Target, Ops        json.RawMessage
		Mutable, Immutable piDifferentialOutcome
	}
	Diff  []struct{ Before, After, Ops json.RawMessage }
	Codec [][]struct{ Ops, Encoded, Wire, Decoded json.RawMessage }
}

// errorClass is the JavaScript class name of an error Pi's delta module throws.
func errorClass(err error) string {
	if _, ok := errors.AsType[*PathError](err); ok {
		return "PathError"
	}
	if _, ok := errors.AsType[*UnsafePathError](err); ok {
		return "UnsafePathError"
	}
	if _, ok := errors.AsType[*TypeError](err); ok {
		return "TypeError"
	}
	return "unexpected Go error type"
}

// Pi packages/chord/src/delta/index.ts (apply 326, applyImmutable 409, encoder 523, decoder 622) and delta/diff.ts (diffRevisions 513): the
// installed Pi chord module runs seeded random cases (testdata/delta_differential.mjs) and Go must return the same value, operations, wire
// batches and error class and message for each. The apply cases mix valid and invalid operations, the diff cases compare revisions a
// few operations apart, and each codec stream shares one encoder and one decoder, with some invalid wire operations.
func TestDeltaMatchesPiOnRandomCases(t *testing.T) {
	const count, seed = 3000, 20260701
	cmd := exec.CommandContext(t.Context(), "node", "testdata/delta_differential.mjs", pigversion.UpstreamVersion, strconv.Itoa(count), strconv.Itoa(seed))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var cases piDifferentialCases
	if err := json.Unmarshal(output, &cases); err != nil {
		t.Fatalf("Pi oracle output: %v", err)
	}
	if len(cases.Apply) != count || len(cases.Diff) != count || len(cases.Codec) == 0 {
		t.Fatalf("Pi oracle returned %d apply, %d diff and %d codec cases, want %d, %d and some", len(cases.Apply), len(cases.Diff), len(cases.Codec), count, count)
	}
	failures := 0
	fail := func(format string, args ...any) {
		t.Helper()
		if failures++; failures <= 20 {
			t.Errorf(format, args...)
		}
	}
	text := func(value any) string { encoded, _ := json.Marshal(value); return string(encoded) }

	for index, c := range cases.Apply {
		for _, mode := range []struct {
			name  string
			apply func(JsonValue, []Op) (JsonValue, error)
			want  piDifferentialOutcome
		}{{"apply", Apply, c.Mutable}, {"applyImmutable", ApplyImmutable, c.Immutable}} {
			var got piDifferentialOutcome
			value, err := mode.apply(parse(t, string(c.Target)), ops(t, string(c.Ops)))
			if err != nil {
				got = piDifferentialOutcome{Error: errorClass(err), Message: err.Error()}
			} else {
				got.Ok = json.RawMessage(text(value))
			}
			// The result is compared as text, so its object keys must be in Pi's order. Pi's text goes through the ordered
			// decoder, which keeps the order and turns a lone surrogate into U+FFFD as a Go string must.
			want := mode.want
			if len(want.Ok) > 0 {
				want.Ok = json.RawMessage(text(parse(t, string(want.Ok))))
			}
			if text(got) != text(want) {
				fail("apply case %d %s(%s, %s):\n got %s\nwant %s", index, mode.name, c.Target, c.Ops, text(got), text(want))
			}
		}
	}

	for index, c := range cases.Diff {
		// The operations are compared as text: their order, and the key order of the values they carry, must be Pi's.
		got := text(DiffRevisions(parse(t, string(c.Before)), parse(t, string(c.After))))
		if want := text(parse(t, string(c.Ops))); got != want {
			fail("diff case %d diffRevisions(%s, %s):\n got %s\nwant %s", index, c.Before, c.After, text(got), c.Ops)
		}
	}

	for streamIndex, stream := range cases.Codec {
		encoder, decoder := NewEncoder(), NewDecoder()
		for batchIndex, step := range stream {
			var got any
			if encoded, err := encoder.Encode(ops(t, string(step.Ops))); err != nil {
				got = chordjson.ObjectOf("error", err.Error())
			} else {
				got = canon(t, encoded)
			}
			if want := parse(t, string(step.Encoded)); text(got) != text(want) {
				fail("codec stream %d batch %d encode(%s):\n got %s\nwant %s", streamIndex, batchIndex, step.Ops, text(got), step.Encoded)
			}
			items, isBatch := parse(t, string(step.Wire)).([]any)
			if !isBatch {
				continue
			}
			wire := make([]WireOp, len(items))
			for at, item := range items {
				wire[at] = WireOp(item.([]any))
			}
			if decoded, err := decoder.Decode(wire); err != nil {
				got = chordjson.ObjectOf("error", errorClass(err)+": "+err.Error())
			} else {
				got = canon(t, decoded)
			}
			if want := parse(t, string(step.Decoded)); text(got) != text(want) {
				fail("codec stream %d batch %d decode(%s):\n got %s\nwant %s", streamIndex, batchIndex, step.Wire, text(got), step.Decoded)
			}
		}
	}
	if failures > 20 {
		t.Errorf("%d more mismatches", failures-20)
	}
}
