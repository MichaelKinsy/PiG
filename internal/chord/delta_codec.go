package chord

// Ports packages/chord/src/delta/index.ts

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"

	"github.com/MichaelKinsy/PiG/agent/harness/pico3"
)

// WireOp is a delta tuple with optional path interning or batch-local path omission. Decode it before applying operations to a replica.
type WireOp []any

// Encoder owns the path dictionary for one ordered state stream.
type Encoder struct {
	seen   map[string]bool
	ids    map[string]int
	nextID int
}

func NewEncoder() *Encoder { return &Encoder{seen: make(map[string]bool), ids: make(map[string]int)} }

func (e *Encoder) Encode(ops []pico3.Op) ([]WireOp, error) {
	previous := ""
	out := make([]WireOp, 0, len(ops))
	for _, op := range ops {
		if len(op) == 0 {
			return nil, errors.New("op is not a tuple")
		}
		if op.Verb() == "r" {
			out = append(out, WireOp(op))
			clear(e.seen)
			clear(e.ids)
			e.nextID = 0
			previous = ""
			continue
		}
		if len(op) < 2 {
			return nil, errors.New("path is not an array")
		}
		path, ok := op[1].([]any)
		if !ok {
			return nil, errors.New("path is not an array")
		}
		if path == nil {
			path = []any{}
		}
		encoded, err := json.Marshal(path)
		if err != nil {
			return nil, err
		}
		key := string(encoded)
		if key == previous {
			out = append(out, append(WireOp{op[0]}, op[2:]...))
			continue
		}
		var ref any = path
		if id, found := e.ids[key]; found {
			ref = id
		} else if e.seen[key] {
			id := e.nextID
			e.nextID++
			e.ids[key] = id
			out = append(out, WireOp{"#", id, path})
			ref = id
		} else {
			e.seen[key] = true
		}
		out = append(out, append(WireOp{op[0], ref}, op[2:]...))
		previous = key
	}
	return out, nil
}

// Decoder retains defined path IDs across batches. A replacement clears the dictionary; path omission never crosses a batch boundary.
type Decoder struct{ paths map[float64][]any }

func NewDecoder() *Decoder { return &Decoder{paths: make(map[float64][]any)} }
func (d *Decoder) Decode(wire []WireOp) ([]pico3.Op, error) {
	var previous []any
	hasPrevious := false
	out := make([]pico3.Op, 0, len(wire))
	for _, op := range wire {
		if err := AssertValidWireOp(op); err != nil {
			return nil, err
		}
		verb := op[0].(string)
		if verb == "#" {
			id, _ := deltaNumber(op[1])
			d.paths[id] = op[2].([]any)
			continue
		}
		if verb == "r" {
			out = append(out, pico3.Op(op))
			clear(d.paths)
			previous = nil
			hasPrevious = false
			continue
		}
		short := verb == "d" && len(op) == 1 || verb != "d" && verb != "p" && len(op) == 2 || verb == "p" && len(op) == 4
		var path []any
		if short {
			if !hasPrevious {
				return nil, &PathError{Path: []any{}}
			}
			path = previous
		} else {
			if id, number := deltaNumber(op[1]); number {
				var found bool
				path, found = d.paths[id]
				if !found {
					return nil, &PathError{Path: op[1]}
				}
			} else {
				path = op[1].([]any)
			}
			previous, hasPrevious = path, true
		}
		if verb != "p" && verb != "m" && len(path) == 0 {
			return nil, &PathError{Path: path}
		}
		firstPayload := 2
		if short {
			firstPayload = 1
		}
		out = append(out, append(pico3.Op{verb, path}, op[firstPayload:]...))
	}
	return out, nil
}

// PathError reports an unresolved path or dictionary reference.
type PathError struct{ Path any }

func (e *PathError) Error() string {
	value, _ := json.Marshal(e.Path)
	return "unresolvable path: " + string(value)
}

// UnsafePathError rejects prototype-chain keys and invalid array indices.
type UnsafePathError struct{ Segment any }

func (e *UnsafePathError) Error() string { return "unsafe path segment: " + deltaString(e.Segment) }

func deltaString(value any) string {
	if value == nil {
		return "null"
	}
	if s, ok := value.(string); ok {
		return s
	}
	return fmt.Sprint(value)
}
func deltaNumber(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case int32:
		return float64(v), true
	case uint:
		return float64(v), true
	case uint64:
		return float64(v), true
	case uint32:
		return float64(v), true
	case json.Number:
		n, err := strconv.ParseFloat(string(v), 64)
		return n, err == nil
	default:
		return 0, false
	}
}
func deltaInteger(value any, minimum float64) bool {
	n, ok := deltaNumber(value)
	return ok && !math.IsInf(n, 0) && !math.IsNaN(n) && n >= minimum && math.Trunc(n) == n
}
func deltaTuple(value any) ([]any, bool) {
	switch v := value.(type) {
	case []any:
		return v, true
	case WireOp:
		return []any(v), true
	case pico3.Op:
		return []any(v), true
	default:
		return nil, false
	}
}
func assertDeltaPath(value any, nonempty bool) error {
	path, ok := value.([]any)
	if !ok {
		return errors.New("path is not an array")
	}
	if nonempty && len(path) == 0 {
		return errors.New("path is empty")
	}
	for _, segment := range path {
		if key, ok := segment.(string); ok {
			if key == "__proto__" || key == "constructor" || key == "prototype" {
				return &UnsafePathError{segment}
			}
		} else if !deltaInteger(segment, 0) {
			return &UnsafePathError{segment}
		}
	}
	return nil
}
func assertDeltaRef(value any) error {
	if _, ok := deltaNumber(value); ok {
		if !deltaInteger(value, 0) {
			return errors.New("bad path id")
		}
		return nil
	}
	return assertDeltaPath(value, false)
}
func assertDeltaPermutation(value any) error {
	permutation, ok := value.([]any)
	if !ok {
		return errors.New("m permutation is not an array")
	}
	seen := make([]bool, len(permutation))
	for _, item := range permutation {
		n, _ := deltaNumber(item)
		if !deltaInteger(item, 0) || n >= float64(len(permutation)) || seen[int(n)] {
			return errors.New("m permutation is not a bijection")
		}
		seen[int(n)] = true
	}
	return nil
}

// AssertValidOp validates decoded tuples without recursively inspecting payload objects.
func AssertValidOp(value any) error {
	op, ok := deltaTuple(value)
	if !ok || len(op) == 0 {
		return errors.New("op is not a tuple")
	}
	verb, _ := op[0].(string)
	switch verb {
	case "r":
		if len(op) != 2 {
			return errors.New("r arity")
		}
		return nil
	case "s", "d":
		size := 3
		if verb == "d" {
			size = 2
		}
		if len(op) != size {
			return fmt.Errorf("%s arity", verb)
		}
		return assertDeltaPath(op[1], true)
	case "a":
		if len(op) != 3 {
			return errors.New("a shape")
		}
		if _, ok := op[2].(string); !ok {
			return errors.New("a shape")
		}
		return assertDeltaPath(op[1], true)
	case "t":
		if len(op) != 3 || !deltaInteger(op[2], 0) {
			return errors.New("t shape")
		}
		return assertDeltaPath(op[1], true)
	case "p":
		if len(op) != 5 {
			return errors.New("p arity")
		}
		if err := assertDeltaPath(op[1], false); err != nil {
			return err
		}
		if !deltaInteger(op[2], 0) {
			return errors.New("p index")
		}
		if !deltaInteger(op[3], 0) {
			return errors.New("p remove")
		}
		if _, ok := op[4].([]any); !ok {
			return errors.New("p items")
		}
		return nil
	case "m":
		if len(op) != 3 {
			return errors.New("m arity")
		}
		if err := assertDeltaPath(op[1], false); err != nil {
			return err
		}
		return assertDeltaPermutation(op[2])
	default:
		return fmt.Errorf("unknown op verb: %s", deltaString(op[0]))
	}
}

// AssertValidWireOp validates the distinct path-reference/short-form vocabulary and all index/permutation constraints.
func AssertValidWireOp(value any) error {
	op, ok := deltaTuple(value)
	if !ok || len(op) == 0 {
		return errors.New("op is not a tuple")
	}
	verb, _ := op[0].(string)
	switch verb {
	case "r":
		if len(op) != 2 {
			return errors.New("r arity")
		}
		return nil
	case "s", "d", "a", "t", "m":
		long := 3
		if verb == "d" {
			long = 2
		}
		if len(op) == long {
			if err := assertDeltaRef(op[1]); err != nil {
				return err
			}
		} else if len(op) != long-1 {
			return fmt.Errorf("%s arity", verb)
		}
		switch verb {
		case "a":
			if _, ok := op[len(op)-1].(string); !ok {
				return errors.New("a value")
			}
		case "t":
			if !deltaInteger(op[len(op)-1], 0) {
				return errors.New("t count")
			}
		case "m":
			return assertDeltaPermutation(op[len(op)-1])
		}
		return nil
	case "p":
		first := 1
		if len(op) == 5 {
			first = 2
			if err := assertDeltaRef(op[1]); err != nil {
				return err
			}
		} else if len(op) != 4 {
			return errors.New("p arity")
		}
		if !deltaInteger(op[first], 0) {
			return errors.New("p index")
		}
		if !deltaInteger(op[first+1], 0) {
			return errors.New("p remove")
		}
		if _, ok := op[first+2].([]any); !ok {
			return errors.New("p items")
		}
		return nil
	case "#":
		if len(op) != 3 || !deltaInteger(op[1], 0) {
			return errors.New("# shape")
		}
		if _, ok := op[2].([]any); !ok {
			return errors.New("# shape")
		}
		return assertDeltaPath(op[2], false)
	default:
		return fmt.Errorf("unknown op verb: %s", deltaString(op[0]))
	}
}
