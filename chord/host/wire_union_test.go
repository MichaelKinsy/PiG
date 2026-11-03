package host

// Exhaustive handling of the Frame union (wire.ts Frame): every frame variant the package declares is built by the
// samples below, encodes under its own "t", and decodes back to the same variant. A variant added anywhere in the package without
// a sample fails the first check; one without a decode case or a frame schema entry fails the second.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// declaredFrameVariants are the types the package's non-test files give a T() method: the variants of Frame.
func declaredFrameVariants(t *testing.T) []string {
	t.Helper()
	var names []string
	for _, file := range parsedSources(t) {
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv == nil || function.Name.Name != "T" || len(function.Recv.List) != 1 {
				continue
			}
			if receiver, ok := function.Recv.List[0].Type.(*ast.Ident); ok {
				names = append(names, receiver.Name)
			}
		}
	}
	slices.Sort(names)
	return names
}

func TestFrameUnionExhaustiveEveryVariantEncodesAndDecodesUnderItsOwnDiscriminant(t *testing.T) {
	reason, deadline := "bye", 1.5
	samples := []Frame{
		HelloFrame{Name: "h", Kind: "host", CodeVersion: 1},
		AckFrame{N: 1},
		HoldFrame{ID: 1, At: "a", Token: chatV1.Description, Input: nil},
		ReleaseFrame{ID: 1},
		ShapeFrame{ID: 1, Members: []string{"view"}, Contract: "f", State: []string{"view"}},
		GoneFrame{ID: 1, Error: WireError{Code: CodeGone, Message: "gone: x"}},
		CallFrame{ID: 1, Hold: 2, Member: "ping"},
		ResultFrame{ID: 1},
		CancelFrame{ID: 1},
		SubFrame{Hold: 1},
		UnsubFrame{Hold: 1},
		ResyncFrame{Hold: 1, M: "view"},
		StateFrame{Hold: 1, M: "view", As: "ops", Ops: []delta.WireOp{{"r", "x"}}},
		PingFrame{N: 1},
		PongFrame{N: 1},
		ByeFrame{Reason: &reason},
		RootEndFrame{RootID: "r"},
		InvalidateFrame{Pattern: chordjson.ObjectOf("id", "a")},
		PrepareFrame{Reload: 1, CodeVersion: 2, Plugins: []string{"p"}, Options: WireReloadOptions{GateDeadline: &deadline}},
		VoteFrame{Reload: 1, Report: &WireReloadReport{CodeVersion: 2, Previous: 1, Changed: []string{}, Removed: []string{}, Prepared: []string{}, Gated: []string{}}},
		CommitFrame{Reload: 1},
		RetireFrame{CodeVersion: 1},
		AbortFrame{Reload: 1},
	}
	sampled := make([]string, len(samples))
	seen := map[string]string{}
	for index, sample := range samples {
		sampled[index] = reflect.TypeOf(sample).Name()
		if other, twice := seen[sample.T()]; twice {
			t.Errorf("%s and %s share the discriminant %q", sampled[index], other, sample.T())
		}
		seen[sample.T()] = sampled[index]
	}
	slices.Sort(sampled)
	// MalformedFrame (link.go) is no variant: Pi delivers any JSON object with a string "t" and its connection rejects what
	// fails isFrame; a Go Frame carries only valid frames, so the rest arrives as this value. It is never a valid frame.
	declared := slices.DeleteFunc(declaredFrameVariants(t), func(name string) bool { return name == "MalformedFrame" })
	if _, malformed := frameOfValue(chordjson.ObjectOf("t", "hello")).(MalformedFrame); !malformed {
		t.Fatal("an invalid hello frame was not delivered as a MalformedFrame")
	}
	if !slices.Equal(declared, sampled) {
		t.Fatalf("the package declares the Frame variants %v, the samples cover %v: a variant needs a sample, a decode case and a schema entry", declared, sampled)
	}
	for _, sample := range samples {
		data, err := EncodeFrame(sample)
		if err != nil {
			t.Fatalf("%T: %v", sample, err)
		}
		decoded, err := DecodeFrame(data)
		if err != nil {
			t.Fatalf("%T encoded as %s does not decode: %v", sample, data, err)
		}
		if reflect.TypeOf(decoded) != reflect.TypeOf(sample) || decoded.T() != sample.T() {
			t.Fatalf("%s decoded as %T, want %T", data, decoded, sample)
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
