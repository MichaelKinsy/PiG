package data_test

// Exhaustive handling of the ChordErrorCode union (errors.ts CHORD_ERROR_REASONS): every code errors.go declares is listed
// by ChordErrorCodes, known to ChordErrorReasons and accepted by ChordErrorFromJson with a reason of its own. A code
// added to errors.go without all three fails.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/data"
)

func TestChordErrorCodeUnionExhaustiveEveryDeclaredCodeIsListedKnownAndDecoded(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "errors.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var declared []data.ChordErrorCode
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.CONST {
			continue
		}
		for _, spec := range general.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok || len(value.Values) != 1 {
				continue
			}
			if typ, ok := value.Type.(*ast.Ident); !ok || typ.Name != "ChordErrorCode" {
				continue
			}
			text, err := strconv.Unquote(value.Values[0].(*ast.BasicLit).Value)
			if err != nil {
				t.Fatal(err)
			}
			declared = append(declared, data.ChordErrorCode(text))
		}
	}
	listed := data.ChordErrorCodes()
	slices.Sort(declared)
	slices.Sort(listed)
	if !slices.Equal(declared, listed) {
		t.Fatalf("errors.go declares the codes %v, ChordErrorCodes lists %v", declared, listed)
	}
	for _, code := range declared {
		reasons, known := data.ChordErrorReasons(code)
		if !known {
			t.Errorf("ChordErrorReasons does not know %q", code)
			continue
		}
		envelope := map[string]any{"code": string(code), "message": "m"}
		if code == data.CodeInvalid {
			// `invalid` requires a reason.
			envelope["reason"] = string(reasons[0])
		}
		decoded, err := data.ChordErrorFromJson(envelope)
		if err != nil || decoded.Code != code {
			t.Errorf("ChordErrorFromJson(%v) = %+v, %v", envelope, decoded, err)
		}
		for _, reason := range reasons {
			envelope["reason"] = string(reason)
			if decoded, err := data.ChordErrorFromJson(envelope); err != nil || decoded.Code != code || decoded.Reason != reason {
				t.Errorf("ChordErrorFromJson(%v) = %+v, %v", envelope, decoded, err)
			}
		}
	}
}
