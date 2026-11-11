package json

import (
	"strconv"
	"testing"
)

// goStdlibQuoteChar is encoding/json's quoteChar in Go 1.27.1, which this package's scanner error text must keep spelling.
func goStdlibQuoteChar(c byte) string {
	if c == '\'' {
		return `'\''`
	}
	if c == '"' {
		return `'"'`
	}
	s := strconv.Quote(string(c))
	return "'" + s[1:len(s)-1] + "'"
}

func TestQuoteCharMatchesTheStandardLibraryForEveryByte(t *testing.T) {
	for c := range 256 {
		if got, want := quoteChar(byte(c)), goStdlibQuoteChar(byte(c)); got != want {
			t.Errorf("quoteChar(%#x) = %s, want %s", c, got, want)
		}
	}
}
