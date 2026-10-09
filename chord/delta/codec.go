package delta

import "encoding/json"

// Ports packages/chord/src/delta/index.ts (encoder, decoder).
//
// Path interning and arity omission live between the tracker and a boundary; Op and Apply know nothing about them. Use one encoder and decoder pair per independent state stream: every decoder must observe exactly the batches its matching encoder produced, beginning with that state's base. Sharing a transport connection does not make separately hydrated states one stream.

// Encoder owns the path dictionary for one ordered state stream. It interns a path on its second use: a definition costs more than the path it replaces, so interning on first use loses on the many paths written exactly once.
type Encoder struct {
	seen   map[string]bool
	ids    map[string]int
	nextID int
}

// NewEncoder returns an encoder with an empty dictionary.
func NewEncoder() *Encoder { return &Encoder{seen: map[string]bool{}, ids: map[string]int{}} }

// Encode converts one batch. Arity omission is scoped to the batch, so a reader that skips or reorders a batch never decodes into the wrong path; ids are the only cross-batch state. An "r" is a recovery point, so it clears the dictionary and every later batch is self-contained.
func (e *Encoder) Encode(ops []Op) ([]WireOp, error) {
	previous := ""
	hasPrevious := false
	out := make([]WireOp, 0, len(ops))
	for _, op := range ops {
		if len(op) == 0 {
			return nil, typeError("op is not a tuple")
		}
		if Verb(op) == "r" {
			out = append(out, WireOp(op))
			clear(e.seen)
			clear(e.ids)
			e.nextID = 0
			previous, hasPrevious = "", false
			continue
		}
		if len(op) < 2 {
			return nil, typeError("path is not an array")
		}
		path, ok := op[1].([]any)
		if !ok {
			return nil, typeError("path is not an array")
		}
		if path == nil {
			path = []any{}
		}
		encoded, err := json.Marshal(path)
		if err != nil {
			return nil, err
		}
		key := string(encoded)
		if hasPrevious && key == previous {
			out = append(out, append(WireOp{op[0]}, op[2:]...))
			continue
		}
		var ref PathRef = path
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
		previous, hasPrevious = key, true
	}
	return out, nil
}

// Decoder retains defined path ids across batches. A replacement clears the dictionary; path omission never crosses a batch boundary. After a decode error discard the decoder and its replica and recover from a later "r".
type Decoder struct{ paths map[int][]any }

// NewDecoder returns a decoder with an empty dictionary.
func NewDecoder() *Decoder { return &Decoder{paths: map[int][]any{}} }

// Decode validates and restores Op tuples.
func (d *Decoder) Decode(wire []WireOp) ([]Op, error) {
	var previous []any
	hasPrevious := false
	out := make([]Op, 0, len(wire))
	for _, op := range wire {
		if err := AssertValidWireOp(op); err != nil {
			return nil, err
		}
		verb := op[0].(string)
		if verb == "#" {
			id, _ := index(op[1])
			d.paths[id] = op[2].([]any)
			continue
		}
		if verb == "r" {
			out = append(out, Op(op))
			clear(d.paths)
			previous, hasPrevious = nil, false
			continue
		}
		short := verb == "d" && len(op) == 1 || verb != "d" && verb != "p" && len(op) == 2 || verb == "p" && len(op) == 4
		var path []any
		if short {
			if !hasPrevious {
				return nil, NewPathError([]any{})
			}
			path = previous
		} else {
			if _, isRef := number(op[1]); isRef {
				id, _ := index(op[1])
				var found bool
				if path, found = d.paths[id]; !found {
					return nil, NewPathError(op[1])
				}
			} else {
				path = op[1].([]any)
			}
			previous, hasPrevious = path, true
		}
		if verb != "p" && verb != "m" && len(path) == 0 {
			return nil, NewPathError(path)
		}
		first := 2
		if short {
			first = 1
		}
		out = append(out, append(Op{verb, path}, op[first:]...))
	}
	return out, nil
}
