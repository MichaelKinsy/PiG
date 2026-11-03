package host

// Exhaustive handling of the ChordErrorCode union (errors.ts ChordErrorCode): every code errors.go declares is listed in
// chordErrorCodes, is a valid code on the wire and in a facet host's error, and has its retryability decided here. A code
// added to errors.go without any of those fails.

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

func declaredChordErrorCodes(t *testing.T) []ChordErrorCode {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "errors.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var codes []ChordErrorCode
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
			codes = append(codes, ChordErrorCode(text))
		}
	}
	slices.Sort(codes)
	return codes
}

func TestChordErrorCodeUnionExhaustiveEveryDeclaredCodeIsListedOnTheWireAndDecided(t *testing.T) {
	// retryable is errors.ts's rule: a superseded or upgrade-conflict failure may be retried by a new root; every code has
	// an entry, so a new code makes its retryability a decision here.
	retryable := map[ChordErrorCode]bool{
		CodeAborted: false, CodeContractMismatch: false, CodeForbidden: false, CodeGone: false, CodeInvalid: false,
		CodeNotInVariant: false, CodeNotReady: false, CodeReleased: false, CodeSuperseded: true, CodeUnavailable: false,
		CodeUpgradeConflict: true, CodeUpgradeRequired: false,
	}
	declared := declaredChordErrorCodes(t)
	listed := slices.Clone(chordErrorCodes)
	slices.Sort(listed)
	if !slices.Equal(declared, listed) {
		t.Fatalf("errors.go declares the codes %v, chordErrorCodes lists %v", declared, listed)
	}
	for _, code := range declared {
		want, decided := retryable[code]
		if !decided {
			t.Errorf("%q has no retryability decision in this test", code)
			continue
		}
		if got := NewChordError(code, "m", ChordErrorOptions{}); got.Code != code || got.Retryable != want || !IsChordError(got, code) {
			t.Errorf("NewChordError(%q) = %+v, want Retryable %v", code, got, want)
		}
		if !IsFrame(GoneFrame{ID: 1, Error: WireError{Code: code, Message: "m"}}.encode()) {
			t.Errorf("a gone frame with the code %q is not a frame", code)
		}
		var fromNode *ChordError
		ok := errors.As(nodeErrorOf(chordjson.ObjectOf("code", string(code), "message", "m")), &fromNode)
		if !ok || fromNode.Code != code || fromNode.Retryable != want {
			t.Errorf("nodeErrorOf(%q) = %+v, want a ChordError of that code, Retryable %v", code, fromNode, want)
		}
	}
	if len(retryable) != len(declared) {
		t.Errorf("the retryability table has %d entries for %d codes", len(retryable), len(declared))
	}
}
