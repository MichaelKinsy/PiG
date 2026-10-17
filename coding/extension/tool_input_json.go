package extension

import (
	"bytes"
	"encoding/json"
	"slices"
)

// Upstream's tool inputs are plain objects typed by a TypeBox schema, so a member the schema does not declare survives a
// round trip. Each typed input keeps such members in Extra and writes them after the declared ones in key order.

// marshalToolInput encodes declared (the input without methods) and appends extra.
func marshalToolInput(declared any, extra map[string]json.RawMessage) ([]byte, error) {
	base, err := noEscapeJSON(declared)
	if err != nil || len(extra) == 0 {
		return base, err
	}
	var out bytes.Buffer
	out.Write(base[:len(base)-1])
	for _, key := range slices.Sorted(mapKeys(extra)) {
		if out.Len() > 1 {
			out.WriteByte(',')
		}
		name, err := noEscapeJSON(key)
		if err != nil {
			return nil, err
		}
		out.Write(name)
		out.WriteByte(':')
		out.Write(extra[key])
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}

func mapKeys(m map[string]json.RawMessage) func(yield func(string) bool) {
	return func(yield func(string) bool) {
		for key := range m {
			if !yield(key) {
				return
			}
		}
	}
}

// unmarshalToolInput decodes data into declared (the input without methods) and returns the members named in known's complement.
func unmarshalToolInput(data []byte, declared any, known ...string) (map[string]json.RawMessage, error) {
	if err := json.Unmarshal(data, declared); err != nil {
		return nil, err
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(data, &all); err != nil || all == nil {
		return nil, nil
	}
	for _, key := range known {
		delete(all, key)
	}
	if len(all) == 0 {
		return nil, nil
	}
	return all, nil
}

func (in BashToolInput) MarshalJSON() ([]byte, error) {
	type declared BashToolInput
	return marshalToolInput(declared(in), in.Extra)
}

func (in *BashToolInput) UnmarshalJSON(data []byte) error {
	type declared BashToolInput
	var d declared
	extra, err := unmarshalToolInput(data, &d, "command", "timeout")
	*in = BashToolInput(d)
	in.Extra = extra
	return err
}

func (in ReadToolInput) MarshalJSON() ([]byte, error) {
	type declared ReadToolInput
	return marshalToolInput(declared(in), in.Extra)
}

func (in *ReadToolInput) UnmarshalJSON(data []byte) error {
	type declared ReadToolInput
	var d declared
	extra, err := unmarshalToolInput(data, &d, "path", "offset", "limit")
	*in = ReadToolInput(d)
	in.Extra = extra
	return err
}

func (in EditToolInput) MarshalJSON() ([]byte, error) {
	type declared EditToolInput
	return marshalToolInput(declared(in), in.Extra)
}

func (in *EditToolInput) UnmarshalJSON(data []byte) error {
	type declared EditToolInput
	var d declared
	extra, err := unmarshalToolInput(data, &d, "path", "edits")
	*in = EditToolInput(d)
	in.Extra = extra
	return err
}

func (in WriteToolInput) MarshalJSON() ([]byte, error) {
	type declared WriteToolInput
	return marshalToolInput(declared(in), in.Extra)
}

func (in *WriteToolInput) UnmarshalJSON(data []byte) error {
	type declared WriteToolInput
	var d declared
	extra, err := unmarshalToolInput(data, &d, "path", "content")
	*in = WriteToolInput(d)
	in.Extra = extra
	return err
}

func (in GrepToolInput) MarshalJSON() ([]byte, error) {
	type declared GrepToolInput
	return marshalToolInput(declared(in), in.Extra)
}

func (in *GrepToolInput) UnmarshalJSON(data []byte) error {
	type declared GrepToolInput
	var d declared
	extra, err := unmarshalToolInput(data, &d, "pattern", "path", "glob", "ignoreCase", "literal", "context", "limit")
	*in = GrepToolInput(d)
	in.Extra = extra
	return err
}

func (in FindToolInput) MarshalJSON() ([]byte, error) {
	type declared FindToolInput
	return marshalToolInput(declared(in), in.Extra)
}

func (in *FindToolInput) UnmarshalJSON(data []byte) error {
	type declared FindToolInput
	var d declared
	extra, err := unmarshalToolInput(data, &d, "pattern", "path", "limit")
	*in = FindToolInput(d)
	in.Extra = extra
	return err
}

func (in LsToolInput) MarshalJSON() ([]byte, error) {
	type declared LsToolInput
	return marshalToolInput(declared(in), in.Extra)
}

func (in *LsToolInput) UnmarshalJSON(data []byte) error {
	type declared LsToolInput
	var d declared
	extra, err := unmarshalToolInput(data, &d, "path", "limit")
	*in = LsToolInput(d)
	in.Extra = extra
	return err
}
