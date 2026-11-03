package data_test

// Exhaustive handling of the ChordErrorCode union (errors.ts CHORD_ERROR_REASONS): every code the package declares is listed
// by ChordErrorCodes, known to ChordErrorReasons and accepted by ChordErrorFromJson with a reason of its own. A code
// added to the package without all three fails.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/data"
)

func TestChordErrorCodeUnionExhaustiveEveryDeclaredCodeIsListedKnownAndDecoded(t *testing.T) {
	var declared []data.ChordErrorCode
	for _, file := range parsedSources(t) {
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
	}
	listed := data.ChordErrorCodes()
	slices.Sort(declared)
	slices.Sort(listed)
	if !slices.Equal(declared, listed) {
		t.Fatalf("the package declares the codes %v, ChordErrorCodes lists %v", declared, listed)
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

// parsedSources are the package's non-test Go files, parsed.
func parsedSources(t *testing.T) []*ast.File {
	t.Helper()
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var files []*ast.File
	fileSet := token.NewFileSet()
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fileSet, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, file)
	}
	return files
}
