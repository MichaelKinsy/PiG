package sdk

import (
	"bytes"
	"math/big"
	"strings"
	"testing"
)

// A schema holds a JavaScript bigint the way Pi's value does, and only writing it as JSON fails, with JSON.stringify's message.
func TestBigIntLiteralIsHeldAndRefusedByJSON(t *testing.T) {
	huge, _ := new(big.Int).SetString("123456789012345678901234567890", 10)
	schema := Type.Literal(huge)
	if !Type.IsLiteral(schema) || Type.IsKind(schema, "Literal") != true {
		t.Fatalf("a bigint literal is not a Literal: %v", schema.Kind())
	}
	if kind, _ := schema.Get("type"); kind != "bigint" {
		t.Fatalf("type = %v", kind)
	}
	held, _ := schema.Get("const")
	if bigint, ok := held.(BigInt); !ok || bigint.Int().Cmp(huge) != 0 || bigint.String() != huge.String() {
		t.Fatalf("const = %#v", held)
	}
	huge.SetInt64(1) // the schema holds a copy
	if held.(BigInt).String() != "123456789012345678901234567890" {
		t.Fatalf("the schema shares the caller's *big.Int: %v", held)
	}
	if text, err := schema.MarshalJSON(); err == nil || !strings.Contains(err.Error(), "Do not know how to serialize a BigInt") {
		t.Fatalf("MarshalJSON gave %q, %v", text, err)
	}
	nested := Type.Object(NewOrderedObject("a", schema))
	if _, err := nested.MarshalJSON(); err == nil || !strings.Contains(err.Error(), "Do not know how to serialize a BigInt") {
		t.Fatalf("a nested bigint was written: %v", err)
	}
	if _, err := schemaRoot(nested.Schema()).MarshalJSON(); err == nil || !strings.Contains(err.Error(), "Do not know how to serialize a BigInt") {
		t.Fatalf("a tool's parameters that hold a bigint were written: %v", err)
	}
	// The wire form carries it as Pi's value does, and reads back as the same schema.
	var written bytes.Buffer
	wireEncode(&written, schema, new([]Refinement))
	again, _ := (&wireReader{text: written.String()}).value().(*TSchema)
	held, _ = again.Get("const")
	if again == nil || held.(BigInt).String() != "123456789012345678901234567890" {
		t.Fatalf("the wire text %s reads back as %#v", written.String(), again)
	}
}

func TestBigIntOtherLiteralsStillPanic(t *testing.T) {
	defer func() {
		if recovered := recover(); recovered != "Invalid Literal value" {
			t.Fatalf("recovered %v", recovered)
		}
	}()
	Type.Literal(struct{}{})
}
