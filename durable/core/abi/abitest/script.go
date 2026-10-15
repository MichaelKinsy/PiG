// SPDX-License-Identifier: MIT

// Package abitest reads ABI event scripts and runs them through a native core. It is test tooling and may use
// encoding/json; core packages never import it outside tests.
package abitest

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/abi/spec"
)

// Stepper is the native binding of a core (ABI section 11).
type Stepper interface {
	Step(ev abi.Event) *abi.Step
}

// RunScript feeds events to s and returns the wire form of each step.
func RunScript(s Stepper, events []abi.Event) [][]byte {
	out := make([][]byte, len(events))
	for i, ev := range events {
		out[i] = abi.AppendStep(nil, s.Step(ev))
	}
	return out
}

// ParseScript reads an event script: one event per line, `name [now=F] [id=N] [phase=N] [skipped=N] [wait=N] [payload]`.
// The payload is the rest of the line: JSON, `hex:<digits>`, or for a rows event a JSON array of
// {"id":N,"cols":N,"rows":[[value...]...]} whose values are null, a string (text), {"i":N}, {"f":X} or {"b":"hex"}.
// Blank lines and lines starting with # are skipped.
func ParseScript(r io.Reader) ([]abi.Event, error) {
	var out []abi.Event
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		ev, err := parseEventLine(line)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", n, err)
		}
		out = append(out, ev)
	}
	return out, sc.Err()
}

func parseEventLine(line string) (abi.Event, error) {
	name, rest, _ := strings.Cut(line, " ")
	kind, ok := spec.EventKindByName(name)
	if !ok {
		return abi.Event{}, fmt.Errorf("unknown event %q", name)
	}
	ev := abi.Event{Kind: kind}
	for {
		rest = strings.TrimLeft(rest, " ")
		word, tail, _ := strings.Cut(rest, " ")
		key, val, isOption := strings.Cut(word, "=")
		if !isOption || strings.ContainsAny(key, "{[\"") {
			break
		}
		switch key {
		case "now":
			f, err := strconv.ParseFloat(val, 64)
			if err != nil {
				return ev, err
			}
			ev.Now = f
		case "id", "phase", "skipped", "wait":
			u, err := strconv.ParseUint(val, 10, 32)
			if err != nil {
				return ev, err
			}
			switch key {
			case "id":
				ev.ID = uint32(u)
			case "phase":
				ev.Phase = uint8(u)
			case "wait":
				ev.WaitID = uint32(u)
			default:
				ev.Skipped = uint32(u)
			}
		default:
			return ev, fmt.Errorf("unknown option %q", key)
		}
		rest = tail
	}
	payload := strings.TrimSpace(rest)
	switch {
	case kind == abi.EventRows:
		answers, err := parseRowsJSON([]byte(payload))
		if err != nil {
			return ev, err
		}
		ev.Rows = answers
	case strings.HasPrefix(payload, "hex:"):
		b, err := hex.DecodeString(payload[4:])
		if err != nil {
			return ev, err
		}
		ev.Payload = b
	case payload != "":
		ev.Payload = []byte(payload)
	}
	return ev, nil
}

func parseRowsJSON(b []byte) ([]abi.ReadRows, error) {
	var raw []struct {
		ID   uint32              `json:"id"`
		Cols uint8               `json:"cols"`
		Rows [][]json.RawMessage `json:"rows"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	out := make([]abi.ReadRows, len(raw))
	for i, a := range raw {
		out[i] = abi.ReadRows{ID: a.ID, Cols: a.Cols}
		for _, row := range a.Rows {
			if len(row) != int(a.Cols) {
				return nil, fmt.Errorf("read %d: row has %d values, want %d", a.ID, len(row), a.Cols)
			}
			vals := make([]abi.Value, len(row))
			for j, cell := range row {
				v, err := ParseValueJSON(cell)
				if err != nil {
					return nil, err
				}
				vals[j] = v
			}
			out[i].Rows = append(out[i].Rows, vals)
		}
	}
	return out, nil
}

// ParseValueJSON reads null, a JSON string (text), {"i":N}, {"f":X} or {"b":"hex"}.
func ParseValueJSON(cell []byte) (abi.Value, error) {
	cell = bytes.TrimSpace(cell)
	if string(cell) == "null" {
		return abi.Null(), nil
	}
	if len(cell) > 0 && cell[0] == '"' {
		var s string
		if err := json.Unmarshal(cell, &s); err != nil {
			return abi.Value{}, err
		}
		return abi.Text(s), nil
	}
	var typed struct {
		I *int64   `json:"i"`
		F *float64 `json:"f"`
		B *string  `json:"b"`
	}
	if err := json.Unmarshal(cell, &typed); err != nil {
		return abi.Value{}, err
	}
	switch {
	case typed.I != nil:
		return abi.Int(*typed.I), nil
	case typed.F != nil:
		return abi.Float(*typed.F), nil
	case typed.B != nil:
		b, err := hex.DecodeString(*typed.B)
		return abi.Blob(b), err
	}
	return abi.Value{}, fmt.Errorf("unknown value %s", cell)
}
