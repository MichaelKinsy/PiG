package plugins

import (
	"encoding/base64"
	"testing"
)

// Ports the behaviour of Node's `SourceMap` (`findEntry`) that build.ts's `freeGlobals` (and stack.ts's `inlineSourceMap`)
// rely on: the mappings' VLQ decoding and the lookup of the greatest mapping at or before a position.

const sourceMapFixture = `{"version":3,"sources":["a.ts","b.ts"],"names":[],"mappings":"AAAA,IAAI,CAAC;ACCA,E"}`

func TestSourceMapFindEntryIsTheGreatestMappingAtOrBeforeThePosition(t *testing.T) {
	m, err := parseSourceMap([]byte(sourceMapFixture))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		line, column     int
		source           string
		origLine, origCl int
	}{
		{0, 0, "a.ts", 0, 0},
		{0, 3, "a.ts", 0, 0},
		{0, 4, "a.ts", 0, 4},
		{0, 100, "a.ts", 0, 5},
		{1, 0, "b.ts", 1, 5},
		{1, 1, "b.ts", 1, 5},
	} {
		entry, found := m.findEntry(c.line, c.column)
		if !found || !entry.hasSource || entry.originalSource != c.source || entry.originalLine != c.origLine || entry.originalColumn != c.origCl {
			t.Errorf("findEntry(%d, %d) = %+v, %v; want %s:%d:%d", c.line, c.column, entry, found, c.source, c.origLine, c.origCl)
		}
	}
}

func TestSourceMapFindEntryHasNoEntryBeforeTheFirstMappingAndNoSourceForAMappingWithout(t *testing.T) {
	m, err := parseSourceMap([]byte(`{"version":3,"sources":["a.ts"],"names":[],"mappings":"EAAA"}`))
	if err != nil {
		t.Fatal(err)
	}
	if entry, found := m.findEntry(0, 1); found {
		t.Errorf("before the first mapping: %+v", entry)
	}
	if entry, found := m.findEntry(0, 2); !found || !entry.hasSource || entry.originalSource != "a.ts" {
		t.Errorf("at the first mapping: %+v, %v", entry, found)
	}
	// A mapping of one field (generated position only) maps to no source; so does a lookup after the last one.
	m, err = parseSourceMap([]byte(sourceMapFixture))
	if err != nil {
		t.Fatal(err)
	}
	for _, at := range [][2]int{{1, 2}, {1, 50}, {9, 0}} {
		if entry, found := m.findEntry(at[0], at[1]); found && entry.hasSource {
			t.Errorf("findEntry(%d, %d) has a source: %+v", at[0], at[1], entry)
		}
	}
}

func TestSourceMapDecodesNegativeAndMultiDigitVLQ(t *testing.T) {
	// "gBAAA": column 16 (two digits); "CAAC": column 17, source column 1; "CADCA": column 18, source line -1, source column 2, name 0.
	m, err := parseSourceMap([]byte(`{"version":3,"sources":["a.ts"],"names":["n"],"mappings":"gBAAA,CAAC,CADCA"}`))
	if err != nil {
		t.Fatal(err)
	}
	entry, found := m.findEntry(0, 17)
	if !found || entry.originalColumn != 1 || entry.originalLine != 0 {
		t.Fatalf("(0,17) = %+v, %v", entry, found)
	}
	entry, found = m.findEntry(0, 18)
	if !found || entry.originalLine != -1 || entry.originalColumn != 2 {
		t.Fatalf("(0,18) = %+v, %v", entry, found)
	}
}

func TestSourceMapInlineIsTheBase64SourceMapAtTheEndOfAScript(t *testing.T) {
	source := "var a;\n//# sourceMappingURL=data:application/json;base64," + base64.StdEncoding.EncodeToString([]byte(sourceMapFixture)) + "\n"
	m, err := inlineSourceMap(source)
	if err != nil || m == nil {
		t.Fatalf("inlineSourceMap = %v, %v", m, err)
	}
	if entry, found := m.findEntry(1, 0); !found || entry.originalSource != "b.ts" {
		t.Fatalf("(1,0) = %+v, %v", entry, found)
	}
	m, err = inlineSourceMap("var a;\n")
	if err != nil || m != nil {
		t.Fatalf("a script without a map: %v, %v", m, err)
	}
}
