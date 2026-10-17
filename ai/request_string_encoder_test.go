package ai

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

// A payload type marked requestStringEncoder has its large strings written as tokens by the encoder, which skips a struct tag option that would write the string in another form. No field reachable from the type may carry one.
func TestRequestStringEncoderTypesCarryNoStringFormatOption(t *testing.T) {
	seen := map[reflect.Type]bool{}
	var walk func(path string, typ reflect.Type)
	walk = func(path string, typ reflect.Type) {
		if seen[typ] {
			return
		}
		seen[typ] = true
		switch typ.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array:
			walk(path, typ.Elem())
		case reflect.Map:
			walk(path, typ.Elem())
		case reflect.Struct:
			for field := range typ.Fields() {
				if !field.IsExported() {
					continue
				}
				options := strings.Split(field.Tag.Get("json"), ",")[1:]
				for _, option := range options {
					if option == "string" || strings.HasPrefix(option, "format:") {
						t.Errorf("%s.%s has the option %q", path, field.Name, option)
					}
				}
				if field.Tag.Get("jsonv2") != "" {
					t.Errorf("%s.%s has a jsonv2 tag", path, field.Name)
				}
				walk(path+"."+field.Name, field.Type)
			}
		}
	}
	for _, typ := range []reflect.Type{reflect.TypeFor[oaiRequest]()} {
		if !typ.Implements(reflect.TypeFor[requestStringEncoder]()) {
			t.Fatalf("%v is not a requestStringEncoder", typ)
		}
		walk(typ.String(), typ)
	}
}

// The encoder sorts the members of a map by the names it writes, so a key long enough to be cached must be written as it is and not as its token.
func TestRequestStringEncoderLeavesALongMapKeyAlone(t *testing.T) {
	long := strings.Repeat("z", requestSpliceMinBytes)
	payload := oaiRequest{Model: "m", ProviderOptions: map[string]any{"q": 1, long: strings.Repeat("v", requestSpliceMinBytes)}}
	want, err := marshalRequestBody(payload)
	if err != nil {
		t.Fatal(err)
	}
	body, err := marshalSpliced(payload, newQuotedStringCache(1<<30), marshalRequestBody)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body.Bytes(), want) {
		t.Fatal("a long map key changed the order of the members")
	}
}
