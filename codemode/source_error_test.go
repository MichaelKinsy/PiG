package codemode

import (
	"errors"
	"testing"
)

// Pi packages/codemode/src/source.ts: invalid input throws a CodemodeSourceError whose name is "CodemodeSourceError" and whose message is the text.
func TestSourceErrorIsNamedLikeUpstreamAndComesFromTheParser(t *testing.T) {
	err := NewSourceError("boom")
	if err.Error() != "boom" || err.Name() != "CodemodeSourceError" {
		t.Fatalf("error %q name %q", err.Error(), err.Name())
	}
	_, parseErr := ParseCodemodeSource("")
	var sourceErr *SourceError
	if !errors.As(parseErr, &sourceErr) || sourceErr.Name() != "CodemodeSourceError" {
		t.Fatalf("ParseCodemodeSource(\"\") error = %#v", parseErr)
	}
}
