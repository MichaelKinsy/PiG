package widthx

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The Python and Rust extension SDKs carry the property tables of unicode_tables.go as source (extensions/sdk-py/pig_sdk/tui/_tables.py and
// extensions/sdk-rs/src/tui/tables.rs), so a pi-tui helper there measures with the data Pi measures with. This test is the generator and
// the freshness check:
//
//	PIG_UPDATE_SDK_TUI=1 go test ./tui/widthx -run TestSDKTablesAreCurrent
func sdkPythonTables() string {
	var b strings.Builder
	b.WriteString("# Code generated from tui/widthx/unicode_tables.go by tui/widthx/sdk_tables_test.go; DO NOT EDIT.\n")
	b.WriteString("# " + unicodeTablesVersion + "\n\n")
	b.WriteString("# (lo, hi, props) triples, sorted, covering 0..0x10FFFF.\nPROP_RANGES = (\n")
	for i := 0; i+2 < len(propRanges); i += 3 {
		fmt.Fprintf(&b, "    (0x%x, 0x%x, 0x%x),\n", propRanges[i], propRanges[i+1], propRanges[i+2])
	}
	b.WriteString(")\n\n# Multi-code-point RGI emoji sequences.\nRGI_SEQUENCES = frozenset((\n")
	for _, s := range &rgiSequences {
		fmt.Fprintf(&b, "    %s,\n", pyString(s))
	}
	b.WriteString("))\n")
	return b.String()
}

func pyString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20 || r >= 0x7f:
			if r > 0xffff {
				fmt.Fprintf(&b, "\\U%08x", r)
			} else {
				fmt.Fprintf(&b, "\\u%04x", r)
			}
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func sdkRustTables() string {
	var b strings.Builder
	b.WriteString("// Code generated from tui/widthx/unicode_tables.go by tui/widthx/sdk_tables_test.go; DO NOT EDIT.\n")
	b.WriteString("// " + unicodeTablesVersion + "\n\n")
	n := len(propRanges) / 3
	fmt.Fprintf(&b, "/// `(lo, hi, props)` triples, sorted, covering 0..=0x10FFFF.\npub(crate) static PROP_RANGES: [(u32, u32, u32); %d] = [\n", n)
	for i := 0; i+2 < len(propRanges); i += 3 {
		fmt.Fprintf(&b, "    (0x%x, 0x%x, 0x%x),\n", propRanges[i], propRanges[i+1], propRanges[i+2])
	}
	b.WriteString("];\n\n")
	seqs := slices.Clone(rgiSequences[:])
	slices.Sort(seqs) // Go compares strings bytewise, as Rust's str ordering does, so the table can be binary searched.
	fmt.Fprintf(&b, "/// Multi-code-point RGI emoji sequences, sorted bytewise.\npub(crate) static RGI_SEQUENCES: [&str; %d] = [\n", len(seqs))
	for _, s := range seqs {
		b.WriteString("    \"")
		for _, r := range s {
			switch {
			case r == '"' || r == '\\':
				b.WriteByte('\\')
				b.WriteRune(r)
			case r < 0x20 || r >= 0x7f:
				b.WriteString("\\u{" + strconv.FormatInt(int64(r), 16) + "}")
			default:
				b.WriteRune(r)
			}
		}
		b.WriteString("\",\n")
	}
	b.WriteString("];\n")
	return b.String()
}

func TestSDKTablesAreCurrent(t *testing.T) {
	for _, c := range []struct{ path, want string }{
		{"../../extensions/sdk-py/pig_sdk/tui/_tables.py", sdkPythonTables()},
		{"../../extensions/sdk-rs/src/tui/tables.rs", sdkRustTables()},
	} {
		if os.Getenv("PIG_UPDATE_SDK_TUI") != "" {
			if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(c.path, []byte(c.want), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		got, err := os.ReadFile(c.path)
		if err != nil || string(got) != c.want {
			t.Errorf("%s is stale: run PIG_UPDATE_SDK_TUI=1 go test ./tui/widthx -run TestSDKTablesAreCurrent", c.path)
		}
	}
}
