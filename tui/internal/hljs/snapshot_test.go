package hljs

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// jsonSnapshot decodes a snapshot with encoding/json into the snapshot the decoder builds, as the oracle for it.
func jsonSnapshot(t *testing.T, data []byte, sections snapshotSections) *snapshot {
	t.Helper()
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		FirstNode int `json:"firstNode"`
		Nodes     []struct {
			Kind   string   `json:"k"`
			Props  [][2]any `json:"p"`
			Frozen bool     `json:"frozen"`
		} `json:"nodes"`
		Registrations [][2]any `json:"registrations"`
		SystemSymbols []string `json:"systemSymbols"`
		Canonicalize  [][2]int `json:"canonicalize"`
		LowerCase     [][2]any `json:"lowerCase"`
	}
	if err := json.NewDecoder(reader).Decode(&document); err != nil {
		t.Fatal(err)
	}
	var convert func(value any) snapshotValue
	convert = func(value any) snapshotValue {
		switch v := value.(type) {
		case nil:
			return snapshotValue{kind: valueNull}
		case bool:
			if v {
				return snapshotValue{kind: valueTrue}
			}
			return snapshotValue{kind: valueFalse}
		case float64:
			return snapshotValue{kind: valueNumber, number: v}
		case string:
			return snapshotValue{kind: valueString, text: v}
		case map[string]any:
			if len(v) != 1 {
				t.Fatalf("object value %v", v)
			}
			switch {
			case v["ref"] != nil:
				return snapshotValue{kind: valueRef, number: v["ref"].(float64)}
			case v["undef"] == true:
				return snapshotValue{kind: valueUndefined}
			case v["re"] != nil:
				return snapshotValue{kind: valueRegExp, text: v["re"].(string)}
			case v["fn"] != nil:
				return snapshotValue{kind: valueFunction, text: v["fn"].(string)}
			case v["arr"] != nil:
				out := snapshotValue{kind: valueArray}
				for _, item := range v["arr"].([]any) {
					out.list = append(out.list, convert(item))
				}
				return out
			}
		}
		t.Fatalf("value %v", value)
		return snapshotValue{}
	}
	s := &snapshot{}
	if sections&snapshotGrammars != 0 {
		s.FirstNode = document.FirstNode
		for _, node := range document.Nodes {
			out := snapshotNode{Kind: node.Kind, Frozen: node.Frozen}
			for _, prop := range node.Props {
				out.Props = append(out.Props, snapshotProp{key: prop[0].(string), value: convert(prop[1])})
			}
			s.Nodes = append(s.Nodes, out)
		}
		for _, registration := range document.Registrations {
			s.Registrations = append(s.Registrations, snapshotRegistration{name: registration[0].(string), node: int(registration[1].(float64))})
		}
	}
	if sections&snapshotTables != 0 {
		s.SystemSymbols = document.SystemSymbols
		s.Canonicalize = document.Canonicalize
		for _, mapping := range document.LowerCase {
			s.LowerCase = append(s.LowerCase, lowerCaseMapping{unit: rune(mapping[0].(float64)), text: mapping[1].(string)})
		}
	}
	return s
}

// Both embedded snapshots decode to what encoding/json reads from them, section by section.
func TestSnapshotDecodesAsEncodingJSON(t *testing.T) {
	for _, tc := range []struct {
		name     string
		data     []byte
		sections snapshotSections
	}{
		{"eager grammars", eagerGrammars, snapshotGrammars},
		{"eager tables", eagerGrammars, snapshotTables},
		{"all grammars", allGrammars, snapshotGrammars},
		{"all, every section", allGrammars, snapshotGrammars | snapshotTables},
	} {
		got := decodeSnapshot(tc.data, tc.sections)
		want := jsonSnapshot(t, tc.data, tc.sections)
		if len(got.Nodes)+len(got.SystemSymbols) == 0 {
			t.Fatalf("%s: decoded nothing", tc.name)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: the decoder and encoding/json disagree", tc.name)
		}
	}
}

func gzipText(t *testing.T, text string) []byte {
	t.Helper()
	var out bytes.Buffer
	writer := gzip.NewWriter(&out)
	if _, err := writer.Write([]byte(text)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// Escapes, invalid UTF-8 and numbers decode as encoding/json decodes them: a lone surrogate escape and an invalid byte become U+FFFD.
func TestSnapshotDecoderMatchesEncodingJSONOnEdgeValues(t *testing.T) {
	values := []string{
		`"plain"`, `"tab\tquote\"slash\/back\\"`, `"\u00e9\u4e2d\ud83d\ude00"`, `"lone \ud800 surrogate"`, "\"bad \xff byte\"", `"é中😀"`, `""`,
		`0`, `-0`, `7`, `-12`, `0.5`, `1e3`, `-2.5E-2`, `123456789012345678`, `true`, `false`, `null`,
		`"\b\f\n\r\t\/\\\""`, `"\\"`, `"mixed \t then \u00e9"`, `"é then \n"`, "\"bad \xff then \\t\"", `{"arr":["a\tb","a\tb","c\nd",{"re":"\\s"},{"re":"\\w\\d"}]}`,
		`{"ref":3}`, `{"undef":true}`, `{"re":"\\d+\\.\\w"}`, `{"fn":"endSameAsBegin.onBegin"}`, `{"arr":[]}`, `{"arr":["a",{"re":"b"},{"arr":[1,{"ref":2}]}]}`,
	}
	for _, value := range values {
		// The second node's escaped key and value decode after the first node's, through the same reused buffer.
		text := `{"firstNode":0,"nodes":[{"k":"mode","p":[["className",` + value + `]]},{"k":"mode","p":[["class\tName",` + value + `],["n\\",` + value + `]]}],"registrations":[]}`
		got := decodeSnapshot(gzipText(t, text), snapshotGrammars)
		want := jsonSnapshot(t, gzipText(t, text), snapshotGrammars)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %+v, want %+v", value, got.Nodes, want.Nodes)
		}
	}
}

// A snapshot outside the generated shape is a build defect: the decoder panics instead of guessing.
func TestSnapshotDecoderRejectsMalformedSnapshots(t *testing.T) {
	for _, text := range []string{
		`{"nodes":[{"k":"mode","p":[["begin",{"ref":1,"re":"x"}]]}]}`,
		`{"nodes":[{"k":"mode","p":[["begin",{"other":1}]]}]}`,
		`{"nodes":[{"k":"mode","p":[["begin",{"undef":false}]]}]}`,
		`{"nodes":[{"k":"mode","p":[["begin",["bare"]]]}]}`,
		`{"nodes":[{"k":"mode","p":[["begin","x","extra"]]}]}`,
		`{"nodes":[]} trailing`,
		`{"nodes":[{"k":"mode","p":[["begin","unterminated]]}]}`,
		`{"firstNode":01}`,
	} {
		func() {
			defer func() {
				recovered := recover()
				if recovered == nil || !strings.HasPrefix(recovered.(string), "hljs: grammar snapshot: ") {
					t.Errorf("%s: recovered %v", text, recovered)
				}
			}()
			decodeSnapshot(gzipText(t, text), snapshotGrammars)
		}()
	}
	truncated := gzipText(t, `{"nodes":[]}`)
	truncated[len(truncated)-4]++
	func() {
		defer func() {
			if recover() == nil {
				t.Error("a gzip trailer with the wrong size was accepted")
			}
		}()
		decodeSnapshot(truncated, snapshotGrammars)
	}()
}

// Strings are cut from shared text chunks: strings longer than a chunk, strings that do not fit the rest of a chunk, and repeated strings still decode as encoding/json decodes them.
func TestSnapshotDecoderStringsSpanTextChunks(t *testing.T) {
	var text strings.Builder
	text.WriteString(`{"firstNode":0,"nodes":[`)
	for i := range 3000 {
		if i > 0 {
			text.WriteByte(',')
		}
		value := strings.Repeat(string(rune('a'+i%26)), 1+i%37) + strconv.Itoa(i)
		switch i {
		case 7:
			value = strings.Repeat("long", snapshotTextChunk/2)
		case 8:
			value = strings.Repeat("x", snapshotTextChunk-3)
		}
		fmt.Fprintf(&text, `{"k":"mode","p":[["className",%q],["k%d",%q],["begin",{"re":%q}]]}`, value, i%50, value, value+"\\d")
	}
	text.WriteString(`],"registrations":[]}`)
	got := decodeSnapshot(gzipText(t, text.String()), snapshotGrammars)
	want := jsonSnapshot(t, gzipText(t, text.String()), snapshotGrammars)
	if len(got.Nodes) != 3000 || !reflect.DeepEqual(got, want) {
		t.Fatal("the decoder and encoding/json disagree on strings across text chunks")
	}
}

// Each mode's "contains" list is a window of one shared array, clipped so that growing one list can never write into the next.
func TestRegistryContainsListsHaveNoSpareCapacity(t *testing.T) {
	r := NewRegistry()
	r.LoadAllLanguages()
	lists := 0
	for _, node := range r.nodes {
		if mode, ok := node.(*Mode); ok && mode.contains != nil {
			lists++
			if cap(mode.contains) != len(mode.contains) {
				t.Fatalf("a contains list of %d modes has capacity %d", len(mode.contains), cap(mode.contains))
			}
		}
	}
	if lists == 0 {
		t.Fatal("no mode has a contains list")
	}
}
