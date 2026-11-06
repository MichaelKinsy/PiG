package tui

import "testing"

// TestTextBeforePrefixSubtractsUTF16UnitsLikeUpstream: upstream's currentLine.slice(0, cursorCol - prefix.length) subtracts the prefix's UTF-16 length from the cursor's UTF-16 column. Pig's cursor column is a byte column, so the byte subtraction stands in for it only when the prefix is exactly the bytes before the cursor.
func TestTextBeforePrefixSubtractsUTF16UnitsLikeUpstream(t *testing.T) {
	for _, row := range []struct {
		line      string
		cursorCol int
		prefix    string
		want      string
	}{
		// The prefix is the text before the cursor: the byte subtraction and the unit subtraction agree.
		{"/load-skills ab", 15, "ab", "/load-skills "},
		{"/load-skills éa", 16, "éa", "/load-skills "},
		{"日本x", 7, "x", "日本"},
		// The cursor moved off the popup's prefix. The units before the cursor are one (日), two (ab) come off, and the negative end counts from the line's end.
		{"日本x", 7, "ab", "日"},
		// Three units before the cursor, four off the end: -1 counts back one unit from the end of the line.
		{"ééx", 5, "abcd", "éé"},
		// The prefix is longer than the line's units: the end is negative and larger than the line.
		{"é", 2, "abcd", ""},
		// Bytes line up with a different slice of the line than the prefix: the units rule.
		{"aé日x", 7, "éé", "aé"},
	} {
		if got := textBeforePrefix(row.line, row.cursorCol, row.prefix); got != row.want {
			t.Errorf("textBeforePrefix(%q, %d, %q) = %q, want %q", row.line, row.cursorCol, row.prefix, got, row.want)
		}
	}
}
