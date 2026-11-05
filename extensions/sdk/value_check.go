package sdk

import (
	"bytes"
	"fmt"
	"strconv"

	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// valueNamespace is TypeBox's `Value` of `typebox/value`: the members of it that run a schema's refinements.
type valueNamespace struct{}

// Value is TypeBox's `Value` namespace (the members Check and Errors; the other members are not served). Pi validates a tool's arguments as plain JSON Schema and runs no
// `Type.Refine` refinement (packages/coding-agent/docs/extensions.md:176: "`Type.Refine()` refinements are not run. Check such constraints in `execute()`"); an extension that wants
// them runs `Value.Check(schema, value)` itself, as a Node extension does with the `Value` of `typebox/value`.
var Value valueNamespace

// ValueError is one error of [valueNamespace.Errors], as TypeBox reports it: the keyword that failed (`~refine` for a refinement), the schema path of the failing schema, the instance path
// of the failing value, the keyword's params in TypeBox's member order and the en_US message (a refinement's is its error function's text).
type ValueError struct {
	Keyword      string
	SchemaPath   string
	InstancePath string
	Params       *OrderedObject
	Message      string
}

// UnmarshalJSON reads the host's wire form of an error.
func (e *ValueError) UnmarshalJSON(data []byte) error {
	var wire struct {
		Keyword      string         `json:"keyword"`
		SchemaPath   string         `json:"schemaPath"`
		InstancePath string         `json:"instancePath"`
		Params       *OrderedObject `json:"params"`
		Message      string         `json:"message"`
	}
	if err := sdkjson.Unmarshal(data, &wire); err != nil {
		return err
	}
	*e = ValueError{Keyword: wire.Keyword, SchemaPath: wire.SchemaPath, InstancePath: wire.InstancePath, Params: wire.Params, Message: wire.Message}
	return nil
}

// Check is `Value.Check(schema, value)`: whether value matches schema and every refinement of the schema passes. The keywords of a schema node run in TypeBox's order and stop at the
// first failure; a node's refinements run last, in the order they were added, and only when its other keywords passed, so the calls a refinement receives and their order are those of
// TypeBox's own check. A refinement that returns an error (or panics) is what a thrown JavaScript error is: it ends the check and Check returns it.
// pig divergence (D226): the host walks the schema and asks this process to run each refinement (see internal/typebox/engine/value_check.go); TypeBox runs them in its own process.
func (valueNamespace) Check(schema any, value any) (bool, error) {
	raw, err := valueEngineCall("Value.Check", schema, value)
	if err != nil {
		return false, err
	}
	var passed bool
	if err := sdkjson.Unmarshal(raw, &passed); err != nil {
		return false, fmt.Errorf("Value.Check: the host answered %s", raw)
	}
	return passed, nil
}

// Errors is `Value.Errors(schema, value)`: the errors of value against schema in TypeBox's order, at most eight; a refinement that fails adds an error of keyword `~refine` whose message is
// its error function's text ("Refine Error" without one). Refinements run as in [valueNamespace.Check], except that the keywords of a node do not stop at the first failure.
func (valueNamespace) Errors(schema any, value any) ([]ValueError, error) {
	raw, err := valueEngineCall("Value.Errors", schema, value)
	if err != nil {
		return nil, err
	}
	errors := []ValueError{}
	if err := sdkjson.Unmarshal(raw, &errors); err != nil {
		return nil, fmt.Errorf("Value.Errors: the host answered %s", raw)
	}
	return errors, nil
}

// valueEngineCall runs one Value member through the host: it asks, answers every refinement call the host replies with and asks again with the answers so far, until the host replies
// with the result (see internal/typebox/engine/value_check.go).
func valueEngineCall(member string, schema, value any) (result []byte, err error) {
	var refs []Refinement
	var wire bytes.Buffer
	if err := valueEncodeSchema(&wire, schema, &refs); err != nil {
		return nil, fmt.Errorf("%s: %w", member, err)
	}
	valueText, err := sdkjson.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("%s: the value has no JSON form: %w", member, err)
	}
	var answers bytes.Buffer
	answers.WriteByte('[')
	for {
		var request bytes.Buffer
		request.WriteString(`{"member":`)
		writeWireString(&request, member)
		request.WriteString(`,"args":[`)
		request.Write(wire.Bytes())
		request.WriteByte(',')
		request.Write(valueText)
		request.WriteByte(',')
		request.Write(answers.Bytes())
		request.WriteString("]]}")
		reply, err := typeEngineTransport(request.Bytes())
		if err != nil {
			return nil, fmt.Errorf("%s: %w", member, err)
		}
		var envelope struct {
			Value sdkjson.RawMessage `json:"value"`
			Call  *struct {
				ID      int                `json:"id"`
				Message bool               `json:"message"`
				Value   sdkjson.RawMessage `json:"value"`
			} `json:"call"`
			Error *struct {
				Name    string `json:"name"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := sdkjson.Unmarshal(reply, &envelope); err != nil {
			return nil, fmt.Errorf("%s: the host answered %s", member, reply)
		}
		switch {
		case envelope.Error != nil:
			return nil, fmt.Errorf("%s", envelope.Error.Message)
		case envelope.Call == nil:
			return envelope.Value, nil
		}
		if envelope.Call.ID < 0 || envelope.Call.ID >= len(refs) {
			return nil, fmt.Errorf("%s: the host asked for refinement %d of %d", member, envelope.Call.ID, len(refs))
		}
		answer, err := runValueRefinement(refs[envelope.Call.ID], envelope.Call.Message, envelope.Call.Value)
		if err != nil {
			return nil, err
		}
		if answers.Len() > 1 {
			answers.WriteByte(',')
		}
		answers.Write(answer)
	}
}

// valueEncodeSchema writes schema in the engine's wire form, collecting its refinements into refs. What cannot be written panics as the Type members do; it is returned as an error.
func valueEncodeSchema(out *bytes.Buffer, schema any, refs *[]Refinement) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("%v", recovered)
		}
	}()
	wireEncode(out, schema, refs)
	return nil
}

// runValueRefinement answers one call of the host: the check (message false: true or false) or the error text (message true) of refinement r on the JSON value. An error or a panic of
// the function is returned as what a thrown JavaScript error is.
func runValueRefinement(r Refinement, message bool, valueText []byte) (answer []byte, err error) {
	var value any
	if len(valueText) > 0 {
		if err := sdkjson.Unmarshal(valueText, &value); err != nil {
			return nil, fmt.Errorf("decode refinement value: %w", err)
		}
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			answer, err = nil, fmt.Errorf("%v", recovered)
		}
	}()
	if !message {
		if r.Check == nil {
			return nil, fmt.Errorf("a refinement has no check function")
		}
		passed, err := r.Check(value)
		if err != nil {
			return nil, err
		}
		return []byte(strconv.FormatBool(passed)), nil
	}
	text := "Refine Error"
	if r.Error != nil {
		if text, err = r.Error(value); err != nil {
			return nil, err
		}
	}
	return sdkjson.MarshalStringify(text)
}
